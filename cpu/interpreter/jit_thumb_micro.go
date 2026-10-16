//go:generate go run ./internal/genappmicro -source jit_thumb_micro.go -output jit_thumb_application_gen.go

package interpreter

import (
	"encoding/binary"
	"math/bits"

	"github.com/mirusu400/aram-core/cpu"
)

// translateThumbMicroOp validates one already-classified Thumb instruction and
// records whether it ends a translated block. The compact instruction stored in
// jitInstr is executed by one direct switch instead of a captured Go closure.
func translateThumbMicroOp(instruction uint16) (thumbInstructionClass, bool, bool) {
	op := thumbInstructionClasses[instruction]
	switch op {
	case thumbMultipleTransfer:
		if instruction&0xff == 0 {
			return op, false, false
		}
		return op, false, true
	case thumbConditionalBranch:
		condition := uint8(instruction>>8) & 0xf
		return op, true, condition != 0xe && condition != 0xf
	case thumbHighRegister:
		opcode := instruction >> 8 & 3
		destination := uint32(instruction)&7 | uint32(instruction>>7)&1<<3
		return op, opcode == 3 || opcode != 1 && destination == cpu.RegisterPC, true
	case thumbPop, thumbUnconditionalBranch, thumbBreakpoint, thumbLongBranch:
		return op, true, true
	case thumbShiftImmediate, thumbMoveImmediate, thumbCompareImmediate,
		thumbAddImmediate, thumbSubtractImmediate, thumbAddSubtract, thumbALU,
		thumbLiteralLoad, thumbRegisterTransfer, thumbImmediateTransfer,
		thumbHalfwordTransfer, thumbStackTransfer, thumbAdjustStack, thumbPush,
		thumbAddPCSP:
		return op, false, true
	default:
		return op, false, false
	}
}

// thumbMicroInstructionStale records whole-system access attribution and trace
// state for one translated instruction. It reports true, after retiring the
// block, when the resident I-cache line no longer holds the decoded opcode.
func (b *Backend) thumbMicroInstructionStale(
	block *jitBlock,
	index int,
	pc uint32,
	in *thumbMicroInstr,
	wholeSystem, traced bool,
) bool {
	instruction := in.raw
	if wholeSystem {
		b.instructionRaw = uint32(instruction)
	}
	if traced || wholeSystem && block.instructionCacheEnabled {
		resident, residentOK := b.residentInstructionCache16(pc)
		if traced {
			b.lastJITResidentRaw = uint32(resident)
			b.lastJITResident = residentOK
		}
		if wholeSystem && block.instructionCacheEnabled && residentOK && resident != instruction {
			b.dropStaleJITBlock(block.start, block, false)
			return true
		}
	}
	if traced {
		b.lastJITBlock = block
		b.lastJITRuntimePC = pc
		b.lastJITDecodedPC = in.pc
		b.lastJITRaw = uint32(instruction)
		b.lastJITIndex = uint32(index)
	}
	return false
}

// executeThumbMicroBlock executes pre-classified Thumb instructions. Keeping the
// raw 16-bit encoding makes translated blocks compact while the class byte
// removes the decoder and, most importantly, the per-instruction indirect
// closure call from runThumbJIT.
func (b *Backend) executeThumbMicroBlock(
	block *jitBlock,
	blockInstructions int,
	wholeSystem, hasExecutionTraps, traced bool,
) (int, bool, *cpu.StopReason, error) {
	// Bound the translated prefix once so the hot loop needs no per-op slice check.
	instructions := block.thumb[:blockInstructions]
	// A writer may retire this translation before a later opcode executes.
	// Poll only after complete writing instructions in the application path;
	// pure ALU instructions retain their generation-check-free hot loop.
	blockGeneration := b.jitGen
	for index := range instructions {
		in := &instructions[index]
		pc := in.pc
		if collectThumbMicro {
			b.recordThumbMicro(in)
		}
		if wholeSystem {
			if index != 0 {
				// An earlier instruction may have retired this translation,
				// for example by evicting its I-cache line.
				if b.jitGen != blockGeneration ||
					traced && b.stopped.Load() || b.takePendingInterrupt() {
					return index, false, nil, nil
				}
				pc = b.regs[cpu.RegisterPC]
				if hasExecutionTraps && b.executionTrapAt(cpu.ModeThumb, pc) {
					reason := cpu.StopExecutionTrap
					return index, false, &reason, nil
				}
			}
			if traced {
				b.recordPC(pc)
			}
			b.instructionAddress = pc
		} else if traced {
			if index != 0 && b.stopped.Load() {
				return index, false, nil, nil
			}
			b.recordPC(pc)
		}
		instruction := in.raw
		// Application blocks run untraced without an architectural I-cache;
		// keep their per-instruction path to this single branch.
		if (wholeSystem || traced) &&
			b.thumbMicroInstructionStale(block, index, pc, in, wholeSystem, traced) {
			return index, false, nil, nil
		}
		b.regs[cpu.RegisterPC] = pc + 2
		switch in.op {
		case thumbShiftImmediate:
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
				return index, false, nil, b.unsupportedThumb(pc, instruction)
			}
			b.regs[rd] = result
			b.setNZC(result, carry)

		case thumbMoveImmediate:
			rd := uint32(instruction>>8) & 7
			value := uint32(instruction & 0xff)
			b.regs[rd] = value
			b.setNZ(value)

		case thumbCompareImmediate:
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)
			b.setNZCV(result, carry, overflow)

		case thumbAddImmediate:
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], uint32(instruction&0xff), 0)
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)

		case thumbSubtractImmediate:
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)

		case thumbAddSubtract:
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

		case thumbALU:
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

		case thumbHighRegister:
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
					^b.readOperandRegister(rs, pc, cpu.ModeThumb), 1,
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
			if op == 3 || op != 1 && rd == cpu.RegisterPC {
				return index + 1, true, nil, nil
			}

		case thumbLiteralLoad:
			rd := uint32(instruction>>8) & 7
			address := ((pc + 4) &^ uint32(3)) + uint32(instruction&0xff)*4
			value, hit := uint32(0), false
			if !b.physicalAccess && b.tlb == nil {
				entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
				value, hit = cachedJITWord(entry, address)
			}
			if !hit {
				var err error
				value, err = b.read32(address, cpu.PermissionRead)
				if err != nil {
					return index, false, nil, err
				}
			}
			b.regs[rd] = value

		case thumbRegisterTransfer:
			op := uint32(instruction>>9) & 7
			ro := uint32(instruction>>6) & 7
			rb := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			address := b.regs[rb] + b.regs[ro]
			switch op {
			case 0:
				if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil {
					return index, false, nil, err
				}
				if b.jitGen != blockGeneration {
					return index + 1, false, nil, nil
				}
			case 1:
				if err := b.write16(address, uint16(b.regs[rd]), cpu.PermissionWrite); err != nil {
					return index, false, nil, err
				}
				if b.jitGen != blockGeneration {
					return index + 1, false, nil, nil
				}
			case 2:
				if err := b.write8(address, byte(b.regs[rd]), cpu.PermissionWrite); err != nil {
					return index, false, nil, err
				}
				if b.jitGen != blockGeneration {
					return index + 1, false, nil, nil
				}
			case 3:
				value, hit := byte(0), false
				if !b.physicalAccess && b.tlb == nil {
					entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
					value, hit = cachedJITRead8(entry, address)
				}
				if !hit {
					var err error
					value, err = b.read8(address, cpu.PermissionRead)
					if err != nil {
						return index, false, nil, err
					}
				}
				b.regs[rd] = uint32(int32(int8(value)))
			case 4:
				value, hit := uint32(0), false
				if !b.physicalAccess && b.tlb == nil {
					entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
					value, hit = cachedJITWord(entry, address)
				}
				if !hit {
					var err error
					value, err = b.read32(address, cpu.PermissionRead)
					if err != nil {
						return index, false, nil, err
					}
				}
				b.regs[rd] = value
			case 5:
				value, hit := uint16(0), false
				if !b.physicalAccess && b.tlb == nil {
					entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
					value, hit = cachedJITRead16(entry, address)
				}
				if !hit {
					var err error
					value, err = b.read16(address, cpu.PermissionRead)
					if err != nil {
						return index, false, nil, err
					}
				}
				b.regs[rd] = uint32(value)
			case 6:
				value, hit := byte(0), false
				if !b.physicalAccess && b.tlb == nil {
					entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
					value, hit = cachedJITRead8(entry, address)
				}
				if !hit {
					var err error
					value, err = b.read8(address, cpu.PermissionRead)
					if err != nil {
						return index, false, nil, err
					}
				}
				b.regs[rd] = uint32(value)
			case 7:
				value, hit := uint16(0), false
				if !b.physicalAccess && b.tlb == nil {
					entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
					value, hit = cachedJITRead16(entry, address)
				}
				if !hit {
					var err error
					value, err = b.read16(address, cpu.PermissionRead)
					if err != nil {
						return index, false, nil, err
					}
				}
				b.regs[rd] = uint32(int32(int16(value)))
			}

		case thumbImmediateTransfer:
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
				value, hit := byte(0), false
				if !b.physicalAccess && b.tlb == nil {
					entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
					value, hit = cachedJITRead8(entry, address)
				}
				if !hit {
					var err error
					value, err = b.read8(address, cpu.PermissionRead)
					if err != nil {
						return index, false, nil, err
					}
				}
				b.regs[rd] = uint32(value)
			case load:
				value, hit := uint32(0), false
				if !b.physicalAccess && b.tlb == nil {
					entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
					value, hit = cachedJITWord(entry, address)
				}
				if !hit {
					var err error
					value, err = b.read32(address, cpu.PermissionRead)
					if err != nil {
						return index, false, nil, err
					}
				}
				b.regs[rd] = value
			case byteTransfer:
				if err := b.write8(address, byte(b.regs[rd]), cpu.PermissionWrite); err != nil {
					return index, false, nil, err
				}
				if b.jitGen != blockGeneration {
					return index + 1, false, nil, nil
				}
			default:
				if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil {
					return index, false, nil, err
				}
				if b.jitGen != blockGeneration {
					return index + 1, false, nil, nil
				}
			}

		case thumbHalfwordTransfer:
			load := instruction&(1<<11) != 0
			offset := uint32(instruction>>6) & 0x1f
			rb := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			address := b.regs[rb] + offset*2
			if load {
				value, hit := uint16(0), false
				if !b.physicalAccess && b.tlb == nil {
					entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
					value, hit = cachedJITRead16(entry, address)
				}
				if !hit {
					var err error
					value, err = b.read16(address, cpu.PermissionRead)
					if err != nil {
						return index, false, nil, err
					}
				}
				b.regs[rd] = uint32(value)
			} else {
				if err := b.write16(address, uint16(b.regs[rd]), cpu.PermissionWrite); err != nil {
					return index, false, nil, err
				}
				if b.jitGen != blockGeneration {
					return index + 1, false, nil, nil
				}
			}

		case thumbStackTransfer:
			load := instruction&(1<<11) != 0
			rd := uint32(instruction>>8) & 7
			address := b.regs[cpu.RegisterSP] + uint32(instruction&0xff)*4
			if load {
				value, hit := uint32(0), false
				if !b.physicalAccess && b.tlb == nil {
					entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
					value, hit = cachedJITWord(entry, address)
				}
				if !hit {
					var err error
					value, err = b.read32(address, cpu.PermissionRead)
					if err != nil {
						return index, false, nil, err
					}
				}
				b.regs[rd] = value
			} else {
				if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil {
					return index, false, nil, err
				}
				if b.jitGen != blockGeneration {
					return index + 1, false, nil, nil
				}
			}

		case thumbAdjustStack:
			offset := uint32(instruction&0x7f) * 4
			if instruction&(1<<7) != 0 {
				b.regs[cpu.RegisterSP] -= offset
			} else {
				b.regs[cpu.RegisterSP] += offset
			}

		case thumbPush:
			if b.pushPrivateThumbRegisters(instruction) {
				break
			}
			registers := uint16(instruction & 0xff)
			includeLR := instruction&(1<<8) != 0
			count := bits.OnesCount16(registers)
			if includeLR {
				count++
			}
			start := b.regs[cpu.RegisterSP] - uint32(count*4)
			address := b.blockTransferAddress(start)
			for register := uint32(0); register < 8; register++ {
				if registers&(1<<register) == 0 {
					continue
				}
				if err := b.write32(address, b.regs[register], cpu.PermissionWrite); err != nil {
					return index, false, nil, err
				}
				address += 4
			}
			if includeLR {
				if err := b.write32(address, b.regs[cpu.RegisterLR], cpu.PermissionWrite); err != nil {
					return index, false, nil, err
				}
			}
			b.regs[cpu.RegisterSP] = start
			if b.jitGen != blockGeneration {
				return index + 1, false, nil, nil
			}

		case thumbPop:
			if b.popPrivateThumbRegisters(instruction) {
				return index + 1, instruction&(1<<8) != 0, nil, nil
			}
			registers := uint16(instruction & 0xff)
			includePC := instruction&(1<<8) != 0
			start := b.regs[cpu.RegisterSP]
			first := b.blockTransferAddress(start)
			address := first
			for register := uint32(0); register < 8; register++ {
				if registers&(1<<register) == 0 {
					continue
				}
				value, hit := uint32(0), false
				if !b.physicalAccess && b.tlb == nil {
					entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
					value, hit = cachedJITWord(entry, address)
				}
				if !hit {
					var err error
					value, err = b.read32(address, cpu.PermissionRead)
					if err != nil {
						return index, false, nil, err
					}
				}
				b.regs[register] = value
				address += 4
			}
			if includePC {
				value, hit := uint32(0), false
				if !b.physicalAccess && b.tlb == nil {
					entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
					value, hit = cachedJITWord(entry, address)
				}
				if !hit {
					var err error
					value, err = b.read32(address, cpu.PermissionRead)
					if err != nil {
						return index, false, nil, err
					}
				}
				b.branchExchange(value)
				address += 4
			}
			b.regs[cpu.RegisterSP] = start + (address - first)
			return index + 1, includePC, nil, nil

		case thumbAddPCSP:
			rd := uint32(instruction>>8) & 7
			base := b.regs[cpu.RegisterSP]
			if instruction&(1<<11) == 0 {
				base = (pc + 4) &^ uint32(3)
			}
			b.regs[rd] = base + uint32(instruction&0xff)*4

		case thumbMultipleTransfer:
			load := instruction&(1<<11) != 0
			rb := uint32(instruction>>8) & 7
			registers := uint16(instruction & 0xff)
			base := b.regs[rb]
			first := b.blockTransferAddress(base)
			address := first
			for register := uint32(0); register < 8; register++ {
				if registers&(1<<register) == 0 {
					continue
				}
				if load {
					value, hit := uint32(0), false
					if !b.physicalAccess && b.tlb == nil {
						entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
						value, hit = cachedJITWord(entry, address)
					}
					if !hit {
						var err error
						value, err = b.read32(address, cpu.PermissionRead)
						if err != nil {
							return index, false, nil, err
						}
					}
					b.regs[register] = value
				} else if err := b.write32(address, b.regs[register], cpu.PermissionWrite); err != nil {
					return index, false, nil, err
				}
				address += 4
			}
			if !load || registers&(1<<rb) == 0 {
				b.regs[rb] = base + (address - first)
			}
			if !load && b.jitGen != blockGeneration {
				return index + 1, false, nil, nil
			}

		case thumbConditionalBranch:
			condition := uint8(instruction>>8) & 0xf
			if b.conditionPassed(condition) {
				offset := int32(int8(instruction&0xff)) << 1
				b.regs[cpu.RegisterPC] = uint32(int32(pc+4) + offset)
				return index + 1, true, nil, nil
			}

		case thumbLongBranch:
			// A Thumb BL/BLX pair is one architectural instruction. Fetch
			// its suffix at execution time, just as the precise executor
			// does: it may fault or come from a replaced I-cache line.
			suffix, err := b.fetch16(pc + 2)
			if err != nil {
				return index, false, nil, err
			}
			high := int32(instruction & 0x7ff)
			if high&(1<<10) != 0 {
				high |= ^int32(0x7ff)
			}
			if suffix&0xf801 == 0xe800 {
				target := uint32(int32((pc+4)&^uint32(3))+(high<<12)) + uint32(suffix&0x7fe)*2
				b.regs[cpu.RegisterLR] = (pc + 4) | 1
				b.branchExchange(target)
				return index + 1, true, nil, nil
			}
			if suffix&0xf800 != 0xf800 {
				return index, false, nil, b.unsupportedThumb(pc, instruction)
			}
			b.regs[cpu.RegisterLR] = (pc + 4) | 1
			b.regs[cpu.RegisterPC] = uint32(int32(pc+4)+(high<<12)) + uint32(suffix&0x7ff)*2
			return index + 1, true, nil, nil

		case thumbUnconditionalBranch:
			offset := int32(instruction & 0x7ff)
			if offset&(1<<10) != 0 {
				offset |= ^int32(0x7ff)
			}
			b.regs[cpu.RegisterPC] = uint32(int32(pc+4) + (offset << 1))
			return index + 1, true, nil, nil

		case thumbBreakpoint:
			reason := cpu.StopBreakpoint
			return index + 1, false, &reason, nil

		default:
			return index, false, nil, b.unsupportedThumb(pc, instruction)
		}
	}
	return blockInstructions, false, nil, nil
}

// cachedJITWord is the small, inlineable private-cache portion of a Thumb word
// load. Callers retain physical/bus reads and native TLB bookkeeping in read32.
// Unaligned loads also use read32 so ARM byte-lane rotation stays architectural.
func cachedJITWord(entry *dataRegionCache, address uint32) (uint32, bool) {
	end := int(address-entry.address) + 4
	// On a 32-bit host, negative/wrapped int endpoints cannot describe a
	// four-byte slice. Prove the same int bounds that the slice expression uses.
	if address&3 != 0 || entry.perms&cpu.PermissionRead == 0 || end < 4 || end > len(entry.data) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(entry.data[end-4 : end]), true
}

// cachedJITRead8 handles only private mapped reads. Physical accesses and
// native TLB bookkeeping remain in the ordinary memory implementation.
func cachedJITRead8(entry *dataRegionCache, address uint32) (byte, bool) {
	offset := int(address - entry.address)
	if entry.perms&cpu.PermissionRead == 0 || uint(offset) >= uint(len(entry.data)) {
		return 0, false
	}
	return entry.data[offset], true
}

// cachedJITRead16 handles only private mapped reads. Physical accesses and
// native TLB bookkeeping remain in the ordinary memory implementation.
func cachedJITRead16(entry *dataRegionCache, address uint32) (uint16, bool) {
	end := int(address-entry.address) + 2
	if entry.perms&cpu.PermissionRead == 0 || end < 2 || end > len(entry.data) {
		return 0, false
	}
	return binary.LittleEndian.Uint16(entry.data[end-2 : end]), true
}
