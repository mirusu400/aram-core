package interpreter

// This file implements ARAM's optional pure-Go dynamic recompiler ("JIT"): a
// second CPU execution strategy behind the same Backend. Instead of decoding
// and dispatching each Thumb instruction on every execution, it translates a
// straight run of instructions into a cached slice of closures (a translated
// block) once, then re-runs the closures on subsequent executions — the
// translate-cache-execute model of a dynamic binary translator, expressed in
// pure Go (no host code emission, so it stays Android/iOS-portable).
//
// This file holds the Thumb decoder; arm_jit.go adds ARM blocks behind the same
// cache/fallback contract. Any instruction they do not translate falls back to
// the interpreter one at a time. Every closure body mirrors the corresponding
// interpreter case exactly, and
// cpu/conformance plus the real-game reference tests guard bit-for-bit
// equivalence with the interpreter oracle.

import (
	"math/bits"

	"github.com/mirusu400/aram-core/cpu"
)

// jitExec runs one translated instruction. branched is true when it redirected
// control (so the run must re-dispatch at the new PC); reason/err mirror the
// interpreter's stop/fault contract.
type jitExec func(b *Backend) (branched bool, reason *cpu.StopReason, err error)

type jitInstr struct {
	pc        uint32
	condition uint8 // ARM only; Thumb's runner ignores it
	exec      jitExec
}

type jitBlock struct {
	start  uint32
	end    uint32
	instrs []jitInstr
}

const jitMaxBlock = 256

// smcInvalidate drops the translated-block cache when a write lands in
// executable memory, so self-modifying or freshly loaded code never runs from a
// stale translation. It is a cheap nil check when the JIT is disabled.
func (b *Backend) smcInvalidate(address, size uint32, perms cpu.Permissions) {
	if perms&cpu.PermissionExecute == 0 {
		return
	}
	if b.jitBlocks != nil || b.armJITBlocks != nil {
		// KTF/WIPI map the guest image read-write-execute, so the blitter's
		// thousands of framebuffer writes each land in an executable region.
		// Only a write that overlaps the span of code we have actually
		// translated can invalidate a block; a write outside it (framebuffer,
		// heap, stack) leaves the cache intact. jitCodeLo/Hi bound every
		// translated block, so this never keeps a stale translation.
		if size != 0 && uint64(address) < uint64(b.jitCodeHi) &&
			uint64(address)+uint64(size) > uint64(b.jitCodeLo) &&
			b.hasJITCodePages(address, size) {
			b.invalidateJITRange(address, size)
		}
	}
	if b.nativeBlocks != nil {
		// Same reasoning as the pure-Go JIT above: only a write that overlaps
		// the span of code the native JIT has actually translated can leave a
		// stale block behind. The guest blitter's stores into the same
		// read-write-execute image are not self-modifying code.
		if size != 0 && uint64(address) < uint64(b.nativeCodeHi) &&
			uint64(address)+uint64(size) > uint64(b.nativeCodeLo) &&
			b.hasCodePages(address, size) {
			b.nativeInvalidateRange(address, size)
		}
	}
}

func (b *Backend) invalidateJITRange(address, size uint32) bool {
	changed := false
	for pc, block := range b.jitBlocks {
		overlaps := block != nil && rangesOverlap(block.start, block.end, address, size)
		if block == nil {
			overlaps = widthRangeOverlap(pc, 2, address, size)
		}
		if overlaps {
			delete(b.jitBlocks, pc)
			changed = true
		}
	}
	for pc, block := range b.armJITBlocks {
		overlaps := block != nil && rangesOverlap(block.start, block.end, address, size)
		if block == nil {
			overlaps = widthRangeOverlap(pc, 4, address, size)
		}
		if overlaps {
			delete(b.armJITBlocks, pc)
			changed = true
		}
	}
	if changed {
		b.jitGen++
	}
	return changed
}

// invalidateTranslationRange is used by line-granular I-cache maintenance and
// persistent native MMIO bail handling. It deliberately leaves conservative
// code-page bitmaps intact; doing so can cause a later harmless scan, but can
// never retain stale translated code or reopen a native write TLB entry.
func (b *Backend) invalidateTranslationRange(address, size uint32) {
	b.invalidateJITRange(address, size)
	b.nativeInvalidateRange(address, size)
}

// invalidateTranslations drops code decoded under an older virtual mapping or
// instruction-cache view. Whole-system guests can change both through CP15
// without writing a private mapped region, so ordinary SMC invalidation alone
// is not sufficient for them.
func (b *Backend) invalidateTranslations() {
	if b.jitBlocks != nil {
		clear(b.jitBlocks)
	}
	if b.armJITBlocks != nil {
		clear(b.armJITBlocks)
	}
	if b.jitBlocks != nil || b.armJITBlocks != nil {
		b.jitGen++
		clear(b.jitCodePages)
		b.jitCodeLo, b.jitCodeHi = ^uint32(0), 0
	}
	b.nativeInvalidate()
}

// runThumbJIT executes Thumb from b.regs[PC] using translated blocks, retiring
// up to limit instructions. It falls back to the interpreter for any PC whose
// instruction it cannot translate.
func (b *Backend) runThumbJIT(limit uint64) (uint64, *cpu.StopReason, error) {
	var executed uint64
	wholeSystem := b.systemBus != nil
	traced := b.tracing()
outer:
	for executed < limit {
		pc := b.regs[cpu.RegisterPC]
		if wholeSystem {
			if b.takePendingInterrupt() {
				return executed, nil, nil
			}
			pc = b.regs[cpu.RegisterPC]
			if b.executionTrapAt(cpu.ModeThumb, pc) {
				reason := cpu.StopExecutionTrap
				return executed, &reason, nil
			}
		}
		block := b.jitBlockAt(pc)
		if block == nil {
			// Untranslatable at pc: interpret exactly one instruction.
			n, reason, err := b.runThumb(1)
			executed += n
			if err != nil {
				return executed, nil, err
			}
			if reason != nil {
				return executed, reason, nil
			}
			if b.mode != cpu.ModeThumb {
				return executed, nil, nil
			}
			continue
		}
		for i := range block.instrs {
			if executed >= limit {
				return executed, nil, nil
			}
			in := &block.instrs[i]
			if wholeSystem {
				if b.takePendingInterrupt() {
					return executed, nil, nil
				}
				pc := b.regs[cpu.RegisterPC]
				if b.executionTrapAt(cpu.ModeThumb, pc) {
					reason := cpu.StopExecutionTrap
					return executed, &reason, nil
				}
				if traced {
					b.recordPC(pc)
				}
				b.instructionAddress = pc
			} else if traced {
				b.recordPC(in.pc)
			}
			branched, reason, err := in.exec(b)
			if err != nil {
				return executed, nil, err
			}
			executed++
			if reason != nil {
				return executed, reason, nil
			}
			if branched {
				if b.mode != cpu.ModeThumb {
					return executed, nil, nil
				}
				continue outer
			}
		}
	}
	return executed, nil, nil
}

// jitBlockAt returns the translated block at pc, translating and caching on a
// miss. A nil entry is cached for a PC whose first instruction is untranslatable
// so it is not re-translated each time.
// jitCacheSize is the direct-mapped block-dispatch cache depth. A power of two
// so the index is a mask; large enough that a title's hot block set rarely
// collides, small enough to stay resident (each entry is 24 bytes).
const jitCacheSize = 8192

// jitCacheEntry caches one translated block for direct-mapped dispatch. gen ties
// it to the jitBlocks generation so an invalidation (which bumps b.jitGen) makes
// every stale entry miss without the invalidation path having to walk the array.
type jitCacheEntry struct {
	pc    uint32
	gen   uint64
	block *jitBlock
}

func (b *Backend) jitBlockAt(pc uint32) *jitBlock {
	slot := &b.jitCache[int(pc>>1)&(jitCacheSize-1)]
	if slot.block != nil && slot.pc == pc && slot.gen == b.jitGen {
		return slot.block
	}
	block, ok := b.jitBlocks[pc]
	if !ok {
		block = b.translateThumbBlock(pc)
		b.jitBlocks[pc] = block
		if block != nil {
			b.markJITCodePages(block.start, block.end-block.start)
			// Grow the translated-code span so smcInvalidate can tell a real
			// code write from an ordinary data/framebuffer write.
			if block.start < b.jitCodeLo {
				b.jitCodeLo = block.start
			}
			if block.end > b.jitCodeHi {
				b.jitCodeHi = block.end
			}
		}
	}
	slot.pc = pc
	slot.gen = b.jitGen
	slot.block = block
	return block
}

func (b *Backend) markJITCodePages(address, size uint32) bool {
	if b.jitCodePages == nil || size == 0 {
		return false
	}
	grew := false
	last := (uint64(address) + uint64(size) - 1) >> tlbPageBits
	for page := uint64(address) >> tlbPageBits; page <= last; page++ {
		word, mask := page>>6, uint64(1)<<(page&63)
		if b.jitCodePages[word]&mask == 0 {
			b.jitCodePages[word] |= mask
			grew = true
		}
	}
	return grew
}

func (b *Backend) hasJITCodePages(address, size uint32) bool {
	if b.jitCodePages == nil || size == 0 {
		return false
	}
	last := (uint64(address) + uint64(size) - 1) >> tlbPageBits
	for page := uint64(address) >> tlbPageBits; page <= last; page++ {
		if b.jitCodePages[page>>6]&(1<<(page&63)) != 0 {
			return true
		}
	}
	return false
}

func (b *Backend) translateThumbBlock(pc uint32) *jitBlock {
	var instrs []jitInstr
	cur := pc
	for len(instrs) < jitMaxBlock {
		word, err := b.fetch16(cur)
		if err != nil {
			break
		}
		exec, terminates, ok := b.translateThumbInstr(word, cur)
		if !ok {
			break
		}
		instrs = append(instrs, jitInstr{pc: cur, exec: exec})
		cur += 2
		if terminates {
			break
		}
	}
	if len(instrs) == 0 {
		return nil
	}
	return &jitBlock{start: pc, end: cur, instrs: instrs}
}

// translateThumbInstr returns a closure executing one Thumb instruction, whether
// it terminates a block (redirects control), and whether it could be translated
// at all. Each closure sets b.regs[PC] to the sequential successor first, then
// mirrors the interpreter's handling of the same encoding.
func (b *Backend) translateThumbInstr(instruction uint16, pc uint32) (jitExec, bool, bool) {
	next := pc + 2
	switch thumbInstructionClasses[instruction] {
	case thumbShiftImmediate:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			op := uint32(instruction>>11) & 3
			shift := uint32(instruction>>6) & 0x1f
			rs := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			value := b.regs[rs]
			var result uint32
			var carry bool
			switch op {
			case 0:
				if shift == 0 {
					result = value
					carry = b.carry()
				} else {
					result = value << shift
					carry = value&(uint32(1)<<(32-shift)) != 0
				}
			case 1:
				if shift == 0 {
					result = 0
					carry = value&flagN != 0
				} else {
					result = value >> shift
					carry = value&(uint32(1)<<(shift-1)) != 0
				}
			case 2:
				if shift == 0 {
					carry = value&flagN != 0
					if carry {
						result = ^uint32(0)
					}
				} else {
					result = uint32(int32(value) >> shift)
					carry = value&(uint32(1)<<(shift-1)) != 0
				}
			default:
				return false, nil, b.unsupportedThumb(pc, instruction)
			}
			b.regs[rd] = result
			b.setNZC(result, carry)
			return false, nil, nil
		}, false, true

	case thumbMoveImmediate:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			value := uint32(instruction & 0xff)
			b.regs[rd] = value
			b.setNZ(value)
			return false, nil, nil
		}, false, true

	case thumbCompareImmediate:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)
			b.setNZCV(result, carry, overflow)
			return false, nil, nil
		}, false, true

	case thumbAddImmediate:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], uint32(instruction&0xff), 0)
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)
			return false, nil, nil
		}, false, true

	case thumbSubtractImmediate:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)
			return false, nil, nil
		}, false, true

	case thumbAddSubtract:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			immediate := instruction&(1<<10) != 0
			subtract := instruction&(1<<9) != 0
			rnOrImmediate := uint32(instruction>>6) & 7
			rs := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			right := rnOrImmediate
			if !immediate {
				right = b.regs[rnOrImmediate]
			}
			var result uint32
			var carry, overflow bool
			if subtract {
				result, carry, overflow = addWithCarry(b.regs[rs], ^right, 1)
			} else {
				result, carry, overflow = addWithCarry(b.regs[rs], right, 0)
			}
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)
			return false, nil, nil
		}, false, true

	case thumbALU:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			op := (instruction >> 6) & 0xf
			rs := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			left, right := b.regs[rd], b.regs[rs]
			switch op {
			case 0x0:
				b.regs[rd] = left & right
				b.setNZ(b.regs[rd])
			case 0x1:
				b.regs[rd] = left ^ right
				b.setNZ(b.regs[rd])
			case 0x2:
				result, carry := shiftLSL(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x3:
				result, carry := shiftLSR(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x4:
				result, carry := shiftASR(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x5:
				carryIn := uint32(0)
				if b.carry() {
					carryIn = 1
				}
				result, carry, overflow := addWithCarry(left, right, carryIn)
				b.regs[rd] = result
				b.setNZCV(result, carry, overflow)
			case 0x6:
				carryIn := uint32(0)
				if b.carry() {
					carryIn = 1
				}
				result, carry, overflow := addWithCarry(left, ^right, carryIn)
				b.regs[rd] = result
				b.setNZCV(result, carry, overflow)
			case 0x7:
				result, carry := shiftROR(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x8:
				b.setNZ(left & right)
			case 0x9:
				result, carry, overflow := addWithCarry(0, ^right, 1)
				b.regs[rd] = result
				b.setNZCV(result, carry, overflow)
			case 0xa:
				result, carry, overflow := addWithCarry(left, ^right, 1)
				b.setNZCV(result, carry, overflow)
			case 0xb:
				result, carry, overflow := addWithCarry(left, right, 0)
				b.setNZCV(result, carry, overflow)
			case 0xc:
				b.regs[rd] = left | right
				b.setNZ(b.regs[rd])
			case 0xd:
				b.regs[rd] = left * right
				b.setNZ(b.regs[rd])
			case 0xe:
				b.regs[rd] = left &^ right
				b.setNZ(b.regs[rd])
			case 0xf:
				b.regs[rd] = ^right
				b.setNZ(b.regs[rd])
			}
			return false, nil, nil
		}, false, true

	case thumbHighRegister:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			op := (instruction >> 8) & 3
			rs := uint32(instruction>>3)&7 | uint32(instruction>>6)&1<<3
			rd := uint32(instruction)&7 | uint32(instruction>>7)&1<<3
			switch op {
			case 0:
				result := b.readOperandRegister(rd, pc, cpu.ModeThumb) +
					b.readOperandRegister(rs, pc, cpu.ModeThumb)
				if rd == cpu.RegisterPC {
					result &^= 1
				}
				b.regs[rd] = result
			case 1:
				result, carry, overflow := addWithCarry(
					b.readOperandRegister(rd, pc, cpu.ModeThumb),
					^b.readOperandRegister(rs, pc, cpu.ModeThumb),
					1,
				)
				b.setNZCV(result, carry, overflow)
			case 2:
				result := b.readOperandRegister(rs, pc, cpu.ModeThumb)
				if rd == cpu.RegisterPC {
					result &^= 1
				}
				b.regs[rd] = result
			case 3:
				target := b.readOperandRegister(rs, pc, cpu.ModeThumb)
				if instruction&(1<<7) != 0 {
					b.regs[cpu.RegisterLR] = (pc + 2) | 1
				}
				b.branchExchange(target)
			}
			return true, nil, nil
		}, true, true

	case thumbLiteralLoad:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			address := ((pc + 4) &^ uint32(3)) + uint32(instruction&0xff)*4
			value, err := b.read32(address, cpu.PermissionRead)
			if err != nil {
				return false, nil, err
			}
			b.regs[rd] = value
			return false, nil, nil
		}, false, true

	case thumbRegisterTransfer:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			op := uint32(instruction>>9) & 7
			ro := uint32(instruction>>6) & 7
			rb := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			address := b.regs[rb] + b.regs[ro]
			switch op {
			case 0:
				if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			case 1:
				if err := b.write16(address, uint16(b.regs[rd]), cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			case 2:
				if err := b.write8(address, byte(b.regs[rd]), cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			case 3:
				value, err := b.read8(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(int32(int8(value)))
			case 4:
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = value
			case 5:
				value, err := b.read16(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(value)
			case 6:
				value, err := b.read8(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(value)
			case 7:
				value, err := b.read16(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(int32(int16(value)))
			}
			return false, nil, nil
		}, false, true

	case thumbImmediateTransfer:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			byteTransfer := instruction&(1<<12) != 0
			load := instruction&(1<<11) != 0
			offset := uint32(instruction>>6) & 0x1f
			rb := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			if !byteTransfer {
				offset *= 4
			}
			address := b.regs[rb] + offset
			switch {
			case load && byteTransfer:
				value, err := b.read8(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(value)
			case load:
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = value
			case byteTransfer:
				if err := b.write8(address, byte(b.regs[rd]), cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			default:
				if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			}
			return false, nil, nil
		}, false, true

	case thumbHalfwordTransfer:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			load := instruction&(1<<11) != 0
			offset := uint32(instruction>>6) & 0x1f
			rb := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			address := b.regs[rb] + offset*2
			if load {
				value, err := b.read16(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(value)
			} else if err := b.write16(address, uint16(b.regs[rd]), cpu.PermissionWrite); err != nil {
				return false, nil, err
			}
			return false, nil, nil
		}, false, true

	case thumbStackTransfer:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			load := instruction&(1<<11) != 0
			rd := uint32(instruction>>8) & 7
			address := b.regs[cpu.RegisterSP] + uint32(instruction&0xff)*4
			if load {
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = value
			} else if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil {
				return false, nil, err
			}
			return false, nil, nil
		}, false, true

	case thumbAdjustStack:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			offset := uint32(instruction&0x7f) * 4
			if instruction&(1<<7) != 0 {
				b.regs[cpu.RegisterSP] -= offset
			} else {
				b.regs[cpu.RegisterSP] += offset
			}
			return false, nil, nil
		}, false, true

	case thumbPush:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			registers := uint16(instruction & 0xff)
			includeLR := instruction&(1<<8) != 0
			count := bits.OnesCount16(registers)
			if includeLR {
				count++
			}
			start := b.regs[cpu.RegisterSP] - uint32(count*4)
			address := start
			for register := uint32(0); register < 8; register++ {
				if registers&(1<<register) == 0 {
					continue
				}
				if err := b.write32(address, b.regs[register], cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
				address += 4
			}
			if includeLR {
				if err := b.write32(address, b.regs[cpu.RegisterLR], cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			}
			b.regs[cpu.RegisterSP] = start
			return false, nil, nil
		}, false, true

	case thumbPop:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			registers := uint16(instruction & 0xff)
			includePC := instruction&(1<<8) != 0
			address := b.regs[cpu.RegisterSP]
			for register := uint32(0); register < 8; register++ {
				if registers&(1<<register) == 0 {
					continue
				}
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[register] = value
				address += 4
			}
			if includePC {
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.branchExchange(value)
				address += 4
			}
			b.regs[cpu.RegisterSP] = address
			return includePC, nil, nil
		}, true, true

	case thumbAddPCSP:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			base := b.regs[cpu.RegisterSP]
			if instruction&(1<<11) == 0 {
				base = (pc + 4) &^ uint32(3)
			}
			b.regs[rd] = base + uint32(instruction&0xff)*4
			return false, nil, nil
		}, false, true

	case thumbMultipleTransfer:
		if instruction&0xff == 0 {
			return nil, false, false
		}
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			load := instruction&(1<<11) != 0
			rb := uint32(instruction>>8) & 7
			registers := uint16(instruction & 0xff)
			address := b.regs[rb]
			for register := uint32(0); register < 8; register++ {
				if registers&(1<<register) == 0 {
					continue
				}
				if load {
					value, err := b.read32(address, cpu.PermissionRead)
					if err != nil {
						return false, nil, err
					}
					b.regs[register] = value
				} else if err := b.write32(address, b.regs[register], cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
				address += 4
			}
			if !load || registers&(1<<rb) == 0 {
				b.regs[rb] = address
			}
			return false, nil, nil
		}, false, true

	case thumbConditionalBranch:
		condition := uint8(instruction>>8) & 0xf
		if condition == 0xe || condition == 0xf {
			// SWI / undefined: let the interpreter handle it precisely.
			return nil, false, false
		}
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			if b.conditionPassed(condition) {
				offset := int32(int8(instruction&0xff)) << 1
				b.regs[cpu.RegisterPC] = uint32(int32(pc+4) + offset)
				return true, nil, nil
			}
			return false, nil, nil
		}, true, true

	case thumbUnconditionalBranch:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			offset := int32(instruction & 0x7ff)
			if offset&(1<<10) != 0 {
				offset |= ^int32(0x7ff)
			}
			b.regs[cpu.RegisterPC] = uint32(int32(pc+4) + (offset << 1))
			return true, nil, nil
		}, true, true

	case thumbBreakpoint:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			reason := cpu.StopBreakpoint
			return false, &reason, nil
		}, true, true

	default:
		// thumbLongBranch / thumbLongBranchSuffix / anything else: end the block
		// and let the interpreter execute it.
		return nil, false, false
	}
}
