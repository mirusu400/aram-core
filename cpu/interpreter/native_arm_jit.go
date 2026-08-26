//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package interpreter

import "github.com/mirusu400/aram-core/cpu"

// runARMNative is the ARM counterpart of runThumbNative. Unsupported native
// encodings retain the portable ARM translated-block tier as their oracle and
// fallback, so adding emitter coverage never creates a second semantic path.
func (b *Backend) runARMNative(limit uint64) (uint64, *cpu.StopReason, error) {
	wholeSystem := b.systemBus != nil
	if wholeSystem && b.tracing() {
		return b.runARMJIT(limit)
	}
	b.nativeRemain = uint32(limit)
	for b.nativeRemain > 0 {
		pc := b.regs[cpu.RegisterPC]
		if wholeSystem {
			if b.takePendingInterrupt() {
				return limit - uint64(b.nativeRemain), nil, nil
			}
			pc = b.regs[cpu.RegisterPC]
			if b.executionTrapAt(cpu.ModeARM, pc) {
				reason := cpu.StopExecutionTrap
				return limit - uint64(b.nativeRemain), &reason, nil
			}
			b.instructionAddress = pc
		}
		block := b.nativeARMBlockAt(pc)
		if block == nil {
			if reason, err, done := b.interpretARMNative(1); done {
				return limit - uint64(b.nativeRemain), reason, err
			}
			continue
		}
		b.resolveFlags()
		status := callNativeBlock(block.entry, &b.regs[0], &b.nativeRemain)
		b.flags.dirty = false
		switch status & 0xff {
		case nativeStatusBail:
			b.refundNativeTail(status)
			bailPC, bailAddress := b.regs[cpu.RegisterPC], b.nativeBailAddress
			if reason, err, done := b.interpretARMNative(1); done {
				return limit - uint64(b.nativeRemain), reason, err
			}
			b.noteNativeBail(cpu.ModeARM, bailPC, bailAddress)
		case nativeStatusIRQ:
			b.refundNativeTail(status)
			if b.takePendingInterrupt() {
				return limit - uint64(b.nativeRemain), nil, nil
			}
			if reason, err, done := b.interpretARMNative(1); done {
				return limit - uint64(b.nativeRemain), reason, err
			}
		case nativeStatusBKPT:
			reason := cpu.StopBreakpoint
			return limit - uint64(b.nativeRemain), &reason, nil
		case nativeStatusBudget:
			if b.nativeRemain > 0 {
				if reason, err, done := b.interpretARMNative(b.nativeRemain); done {
					return limit - uint64(b.nativeRemain), reason, err
				}
			}
		}
	}
	return limit - uint64(b.nativeRemain), nil, nil
}

func (b *Backend) interpretARMNative(count uint32) (*cpu.StopReason, error, bool) {
	n, reason, err := b.runARMJIT(uint64(count))
	b.nativeRemain -= uint32(n)
	if err != nil {
		return nil, err, true
	}
	if reason != nil {
		return reason, nil, true
	}
	return nil, nil, b.mode != cpu.ModeARM
}

func (b *Backend) nativeARMBlockAt(pc uint32) *nativeBlock {
	slot := &b.nativeARMCache[pc>>2&(nativeCacheSize-1)]
	if slot.pc == pc && slot.gen == b.nativeGen {
		return slot.block
	}
	block, ok := b.nativeARMBlocks[pc]
	if !ok {
		block = b.translateNativeARMBlock(pc)
		b.cacheNativeARMBlock(pc, block)
	}
	slot.pc, slot.gen, slot.block = pc, b.nativeGen, block
	return block
}

func (b *Backend) translateNativeARMBlock(pc uint32) *nativeBlock {
	body := b.newEmitter()
	cur := pc
	count := 0
	end := pc
	term := terminator{kind: termNone, next: pc}
	for count < nativeMaxBlock {
		if b.systemBus != nil && b.executionTrapAt(cpu.ModeARM, cur) {
			break
		}
		if b.nativeSlowAt(cpu.ModeARM, cur) {
			break
		}
		instruction, err := b.fetch32(cur)
		if err != nil {
			break
		}
		if b.systemBus != nil {
			body.interruptPoll(cur, count)
		}
		kind, decoded := b.translateOneNativeARM(body, instruction, cur, count)
		if kind == translateTerminator {
			count++
			term = decoded
			end = cur + 4
			break
		}
		if kind == translateBail {
			break
		}
		count++
		cur += 4
		end = cur
	}
	if count == 0 {
		return nil
	}
	if term.kind == termNone {
		term.next = cur
	}

	main := b.newEmitter()
	main.prologue()
	gateOff := main.mark()
	main.gate(count, pc)
	main.appendCode(body.code())
	b.emitNativeTerminator(main, cpu.ModeARM, term, pc, gateOff)
	entry := b.arenaAppend(main.code())
	if entry == 0 {
		b.nativeInvalidate()
		if entry = b.arenaAppend(main.code()); entry == 0 {
			return nil
		}
	}
	grew := b.markCodePages(pc, end-pc)
	if pc < b.nativeCodeLo {
		b.nativeCodeLo, grew = pc, true
	}
	if end > b.nativeCodeHi {
		b.nativeCodeHi, grew = end, true
	}
	if grew {
		b.tlbClearWrite()
	}
	gate := entry + uintptr(gateOff)
	b.publishNativeLink(cpu.ModeARM, pc, gate)
	return &nativeBlock{
		start: pc, end: end, mode: cpu.ModeARM,
		count: count, entry: entry, gate: gate,
	}
}

func (b *Backend) translateOneNativeARM(
	e emitter,
	instruction, pc uint32,
	retired int,
) (int, terminator) {
	condition := uint8(instruction >> 28)
	if condition == 0xf {
		return translateBail, terminator{}
	}
	switch {
	case instruction&0x0ff000f0 == 0x01200070: // BKPT
		if condition != 0xe {
			return translateBail, terminator{}
		}
		return translateTerminator, terminator{kind: termBkpt, next: pc + 4}
	case instruction&0x0e000000 == 0x0a000000: // B / BL immediate
		offset := int32(instruction&0x00ffffff) << 2
		if offset&(1<<25) != 0 {
			offset |= ^int32(0x03ffffff)
		}
		target := uint32(int32(pc+8) + offset)
		if instruction&(1<<24) != 0 {
			if condition != 0xe {
				return translateBail, terminator{}
			}
			return translateTerminator, terminator{
				kind: termBranchLink, link: pc + 4, target: target,
			}
		}
		if condition == 0xe {
			return translateTerminator, terminator{kind: termUncond, target: target}
		}
		return translateTerminator, terminator{
			kind: termCond, cond: condition, target: target, next: pc + 4,
		}
	case instruction&0x0fbf0fff == 0x010f0000, // MRS
		instruction&0x0fb0fff0 == 0x0120f000, // MSR register
		instruction&0x0fb0f000 == 0x0320f000: // MSR immediate
		return translateBail, terminator{}
	case instruction&0x0c000000 == 0x04000000: // immediate LDR/STR
		if instruction&(1<<25) != 0 || instruction&(1<<24) == 0 ||
			instruction&(1<<21) != 0 {
			return translateBail, terminator{}
		}
		load := instruction&(1<<20) != 0
		rn := instruction >> 16 & 0xf
		rd := instruction >> 12 & 0xf
		if rd == cpu.RegisterPC {
			return translateBail, terminator{}
		}
		access := memAccess{
			store:    !load,
			size:     4,
			rd:       rd,
			base:     rn,
			offset:   instruction & 0xfff,
			subtract: instruction&(1<<23) == 0,
		}
		if instruction&(1<<22) != 0 {
			access.size = 1
		}
		if rn == cpu.RegisterPC {
			address := pc + 8
			if access.subtract {
				address -= access.offset
			} else {
				address += access.offset
			}
			access.absolute, access.offset, access.subtract = true, address, false
		}
		site := e.conditionStart(condition)
		e.memory(access, pc, retired)
		e.conditionEnd(site)
		return translateBody, terminator{}
	case instruction&0x0c000000 != 0:
		return translateBail, terminator{}
	}

	shifter, ok := decodeARMJITShifter(instruction)
	if !ok {
		return translateBail, terminator{}
	}
	opcode := uint8(instruction >> 21 & 0xf)
	if !nativeARMDataOpEmittable(opcode) {
		return translateBail, terminator{}
	}
	writes := opcode < 8 || opcode >= 12
	rd := instruction >> 12 & 0xf
	if writes && rd == cpu.RegisterPC {
		return translateBail, terminator{}
	}
	op := nativeARMDataOp{
		opcode:   opcode,
		setFlags: instruction&(1<<20) != 0 || !writes,
		rd:       rd,
		rn:       instruction >> 16 & 0xf,
		pcValue:  pc + 8,
		carry:    -1,
	}
	if shifter.immediate {
		op.operand = shifter.immediateValue
		if shifter.rotate != 0 {
			op.carry = int8(shifter.immediateValue >> 31)
		}
	} else {
		if shifter.registerShift || shifter.shiftType != 0 || shifter.amount != 0 {
			return translateBail, terminator{}
		}
		op.operandReg = true
		op.operand = shifter.rm
	}
	site := e.conditionStart(condition)
	if !e.armDataProcessing(op) {
		// Unreachable while the emitters agree with nativeARMDataOpEmittable.
		// Closing the site regardless keeps a future divergence a wasted block
		// instead of an unpatched branch: the emitters only touch host scratch
		// before they decide, so the skipped-over bytes write no guest state.
		e.conditionEnd(site)
		return translateBail, terminator{}
	}
	e.conditionEnd(site)
	return translateBody, terminator{}
}
