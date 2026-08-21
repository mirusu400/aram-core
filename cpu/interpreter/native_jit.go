//go:build (windows && amd64) || ((android || linux) && arm64)

package interpreter

// Shared, host-independent core of the native Thumb JIT: the Run loop, the
// block cache/translator, and the Thumb decoder. Everything here is identical
// across hosts and is exercised by the conformance differential on whichever
// host runs the tests (windows/amd64), so a second host emitter (android/arm64)
// reuses the same decode and accounting logic and can only differ in the bytes
// each primitive emits ??which its own encoding tests validate against a real
// assembler. The per-host pieces are the emitter (native_emit_*.go), the
// executable arena, and callNativeBlock.
//
// A translated block is a leaf host function called with two pointers: the guest
// register file (&regs[0]) and the remaining instruction budget (&nativeRemain).
// It starts with a budget gate (subtract this block's instruction count from the
// budget, or exit if it does not fit), runs the body, then a terminator. When the
// terminating branch targets the block's own start it is a self-loop: the
// terminator jumps back to the gate and the loop runs entirely in native code,
// re-checking the budget each iteration ??this is what removes the per-iteration
// Go<->native transition. The budget is decremented in code so the run still
// stops exactly at the limit (frame pacing depends on an exact retired count).

import "github.com/mirusu400/aram-core/cpu"

// termKind classifies how a block ends.
type termKind uint8

const (
	termNone   termKind = iota // fell off the end (untranslatable next / max) -> exit at cur
	termUncond                 // unconditional branch to a constant target
	termCond                   // conditional branch (constant taken target + fall-through)
	termBkpt                   // BKPT
)

type terminator struct {
	kind   termKind
	cond   uint8  // termCond
	target uint32 // termUncond target / termCond taken target
	next   uint32 // termCond fall-through PC / termBkpt next PC / termNone cur
}

// runThumbNative executes Thumb from b.regs[PC] using translated native blocks,
// retiring up to limit instructions. Blocks decrement b.nativeRemain in code;
// on a budget exit (the next block does not fit the remaining budget) it
// interprets one instruction so the exact tail of the batch runs on the oracle.
func (b *Backend) runThumbNative(limit uint64) (uint64, *cpu.StopReason, error) {
	b.nativeRemain = uint32(limit)
	for b.nativeRemain > 0 {
		pc := b.regs[cpu.RegisterPC]
		block := b.nativeBlockAt(pc)
		if block == nil {
			// Untranslatable here: interpret exactly one instruction.
			if reason, err, done := b.interpretOneNative(); done {
				return limit - uint64(b.nativeRemain), reason, err
			}
			continue
		}
		// Materialize deferred flags into CPSR (conditional branches read them;
		// a pending update from a prior interpreted instruction must not be lost).
		b.resolveFlags()
		status := callNativeBlock(block.entry, &b.regs[0], &b.nativeRemain)
		b.flags.dirty = false
		switch status {
		case nativeStatusBKPT:
			reason := cpu.StopBreakpoint
			return limit - uint64(b.nativeRemain), &reason, nil
		case nativeStatusBudget:
			// Remaining budget < block.count. If any budget is left, interpret one
			// to make progress on the batch tail (< block.count instructions); if
			// it is exactly zero the loop condition ends the run without
			// underflowing the unsigned counter.
			if b.nativeRemain > 0 {
				if reason, err, done := b.interpretOneNative(); done {
					return limit - uint64(b.nativeRemain), reason, err
				}
			}
		}
		// nativeStatusNorm: the block advanced regs (and remain); loop again.
	}
	return limit - uint64(b.nativeRemain), nil, nil
}

// interpretOneNative runs one interpreter instruction, decrements the shared
// budget, and reports whether runThumbNative must return (fault, stop, or a
// switch to ARM). done==false means keep looping.
func (b *Backend) interpretOneNative() (*cpu.StopReason, error, bool) {
	n, reason, err := b.runThumb(1)
	b.nativeRemain -= uint32(n)
	if err != nil {
		return nil, err, true
	}
	if reason != nil {
		return reason, nil, true
	}
	if b.mode != cpu.ModeThumb {
		return nil, nil, true
	}
	return nil, nil, false
}

// nativeBlockAt returns the translated block at pc, translating and caching on a
// miss. A nil entry is cached for a PC whose first instruction is untranslatable
// so it is not re-translated each time.
func (b *Backend) nativeBlockAt(pc uint32) *nativeBlock {
	if block, ok := b.nativeBlocks[pc]; ok {
		return block
	}
	block := b.translateNativeBlock(pc)
	b.nativeBlocks[pc] = block
	return block
}

func (b *Backend) translateNativeBlock(pc uint32) *nativeBlock {
	// Phase 1: emit the straight-line body into a separate emitter and find the
	// terminator, counting retired instructions.
	body := b.newEmitter()
	cur := pc
	count := 0
	var term terminator
	term.kind = termNone
	term.next = pc // overwritten below; the fall-through target if we run out
	for count < nativeMaxBlock {
		word, err := b.fetch16(cur)
		if err != nil {
			break
		}
		kind, t := b.translateOne(body, word, cur)
		if kind == translateTerminator {
			count++ // the terminator retires one instruction
			term = t
			break
		}
		if kind == translateBail {
			break // untranslatable: fall-through exit at cur
		}
		count++
		cur += 2
	}
	if count == 0 {
		return nil
	}
	if term.kind == termNone {
		term.next = cur // exit to the next (untranslated) instruction
	}

	// Phase 2: prologue, budget gate (the self-loop header), body, terminator.
	main := b.newEmitter()
	main.prologue()
	gateOff := main.mark()
	main.gate(count, pc)
	main.appendCode(body.code())
	emitTerminator(main, term, pc, gateOff)

	entry := b.arenaAppend(main.code())
	if entry == 0 {
		b.nativeInvalidate()
		if entry = b.arenaAppend(main.code()); entry == 0 {
			return nil
		}
	}
	return &nativeBlock{start: pc, count: count, entry: entry}
}

func emitTerminator(e emitter, t terminator, startPC uint32, gateOff int) {
	switch t.kind {
	case termUncond:
		if t.target == startPC {
			e.selfLoopUncond(gateOff)
		} else {
			e.exitBranch(t.target)
		}
	case termCond:
		if t.target == startPC {
			e.selfLoopCond(t.cond, gateOff, t.next)
		} else {
			e.exitCondBranch(t.cond, t.target, t.next)
		}
	case termBkpt:
		e.exitBkpt(t.next)
	default: // termNone: fell off the end
		e.exitBranch(t.next)
	}
}

const (
	translateBody       = iota // emitted a body op
	translateTerminator        // returned terminator info (nothing emitted)
	translateBail              // untranslatable / not offered here
)

// translateOne emits one non-terminator Thumb instruction into e and returns
// translateBody, or returns translateTerminator/translateBail (emitting nothing)
// for a control-flow or unhandled instruction. It mirrors the interpreter and
// the pure-Go JIT class handling exactly.
func (b *Backend) translateOne(e emitter, instruction uint16, pc uint32) (int, terminator) {
	switch thumbInstructionClasses[instruction] {
	case thumbMoveImmediate:
		e.moveImm(uint32(instruction>>8)&7, uint32(instruction&0xff))
		return translateBody, terminator{}
	case thumbAddImmediate:
		e.addImm(uint32(instruction>>8)&7, uint32(instruction&0xff))
		return translateBody, terminator{}
	case thumbSubtractImmediate:
		e.subImm(uint32(instruction>>8)&7, uint32(instruction&0xff))
		return translateBody, terminator{}
	case thumbCompareImmediate:
		e.cmpImm(uint32(instruction>>8)&7, uint32(instruction&0xff))
		return translateBody, terminator{}
	case thumbAddSubtract:
		e.addSub(uint32(instruction)&7, uint32(instruction>>3)&7, uint32(instruction>>6)&7,
			instruction&(1<<10) != 0, instruction&(1<<9) != 0)
		return translateBody, terminator{}
	case thumbShiftImmediate:
		e.shiftImm(uint32(instruction)&7, uint32(instruction>>3)&7,
			uint32(instruction>>11)&3, uint32(instruction>>6)&0x1f)
		return translateBody, terminator{}
	case thumbALU:
		if e.alu(uint32(instruction>>6)&0xf, uint32(instruction)&7, uint32(instruction>>3)&7) {
			return translateBody, terminator{}
		}
		return translateBail, terminator{}
	case thumbAdjustStack:
		e.adjustStack(instruction&(1<<7) != 0, uint32(instruction&0x7f)*4)
		return translateBody, terminator{}
	case thumbAddPCSP:
		rd := uint32(instruction>>8) & 7
		offset := uint32(instruction&0xff) * 4
		if instruction&(1<<11) != 0 {
			e.addSPImm(rd, offset)
		} else {
			e.setRegConst(rd, ((pc+4)&^uint32(3))+offset)
		}
		return translateBody, terminator{}
	case thumbConditionalBranch:
		condition := uint8(instruction>>8) & 0xf
		if condition == 0xe || condition == 0xf {
			return translateBail, terminator{}
		}
		taken := uint32(int32(pc+4) + (int32(int8(instruction&0xff)) << 1))
		return translateTerminator, terminator{kind: termCond, cond: condition, target: taken, next: pc + 2}
	case thumbUnconditionalBranch:
		offset := int32(instruction & 0x7ff)
		if offset&(1<<10) != 0 {
			offset |= ^int32(0x7ff)
		}
		target := uint32(int32(pc+4) + (offset << 1))
		return translateTerminator, terminator{kind: termUncond, target: target}
	case thumbBreakpoint:
		return translateTerminator, terminator{kind: termBkpt, next: pc + 2}
	default:
		// Memory transfers, high-register ops, BL, semihosting SWI, etc.
		return translateBail, terminator{}
	}
}
