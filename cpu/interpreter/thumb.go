package interpreter

import (
	"fmt"
	"math/bits"

	"github.com/mirusu400/aram-core/cpu"
)

type thumbInstructionClass uint8

const (
	thumbUnsupported thumbInstructionClass = iota
	thumbBreakpoint
	thumbShiftImmediate
	thumbMoveImmediate
	thumbCompareImmediate
	thumbAddImmediate
	thumbSubtractImmediate
	thumbAddSubtract
	thumbALU
	thumbHighRegister
	thumbLiteralLoad
	thumbRegisterTransfer
	thumbImmediateTransfer
	thumbHalfwordTransfer
	thumbStackTransfer
	thumbAdjustStack
	thumbPush
	thumbPop
	thumbAddPCSP
	thumbMultipleTransfer
	thumbConditionalBranch
	thumbLongBranch
	thumbLongBranchSuffix
	thumbUnconditionalBranch
)

var thumbInstructionClasses = func() [1 << 16]thumbInstructionClass {
	var classes [1 << 16]thumbInstructionClass
	for raw := range classes {
		instruction := uint16(raw)
		switch {
		case instruction&0xff00 == 0xbe00:
			classes[raw] = thumbBreakpoint
		case instruction&0xe000 == 0x0000 &&
			instruction&0x1800 != 0x1800:
			classes[raw] = thumbShiftImmediate
		case instruction&0xf800 == 0x2000:
			classes[raw] = thumbMoveImmediate
		case instruction&0xf800 == 0x2800:
			classes[raw] = thumbCompareImmediate
		case instruction&0xf800 == 0x3000:
			classes[raw] = thumbAddImmediate
		case instruction&0xf800 == 0x3800:
			classes[raw] = thumbSubtractImmediate
		case instruction&0xf800 == 0x1800:
			classes[raw] = thumbAddSubtract
		case instruction&0xfc00 == 0x4000:
			classes[raw] = thumbALU
		case instruction&0xfc00 == 0x4400:
			classes[raw] = thumbHighRegister
		case instruction&0xf800 == 0x4800:
			classes[raw] = thumbLiteralLoad
		case instruction&0xf000 == 0x5000:
			classes[raw] = thumbRegisterTransfer
		case instruction&0xe000 == 0x6000:
			classes[raw] = thumbImmediateTransfer
		case instruction&0xf000 == 0x8000:
			classes[raw] = thumbHalfwordTransfer
		case instruction&0xf000 == 0x9000:
			classes[raw] = thumbStackTransfer
		case instruction&0xff00 == 0xb000:
			classes[raw] = thumbAdjustStack
		case instruction&0xfe00 == 0xb400:
			classes[raw] = thumbPush
		case instruction&0xfe00 == 0xbc00:
			classes[raw] = thumbPop
		case instruction&0xf000 == 0xa000:
			classes[raw] = thumbAddPCSP
		case instruction&0xf000 == 0xc000:
			classes[raw] = thumbMultipleTransfer
		case instruction&0xf000 == 0xd000:
			classes[raw] = thumbConditionalBranch
		case instruction&0xf800 == 0xf000:
			classes[raw] = thumbLongBranch
		case instruction&0xf800 == 0xf800:
			classes[raw] = thumbLongBranchSuffix
		case instruction&0xf800 == 0xe000:
			classes[raw] = thumbUnconditionalBranch
		}
	}
	return classes
}()

// runThumb executes Thumb instructions starting at b.regs[PC] until it reaches
// the instruction limit, hits a stop reason or fault, or the guest switches to
// ARM mode. Executing a straight run in one call - and following intra-Thumb
// branches without returning - amortizes the per-instruction call and dispatch
// overhead that dominated a tight guest render loop (issue #54). It returns the
// number of instructions retired; a fault does not count the faulting one, a
// stop reason does, matching the previous one-instruction-per-call contract.
func (b *Backend) runThumb(limit uint64) (uint64, *cpu.StopReason, error) {
	var executed uint64
	for executed < limit {
		if b.takePendingInterrupt() {
			return executed, nil, nil
		}
		if b.executionTrapAt(cpu.ModeThumb, b.regs[cpu.RegisterPC]) {
			reason := cpu.StopExecutionTrap
			return executed, &reason, nil
		}
		if b.pcHits != nil {
			b.pcHits[b.regs[cpu.RegisterPC]]++
		}
		pc := b.regs[cpu.RegisterPC]
		b.accessContext = cpu.MemoryAccessContext{
			InstructionAddress: pc, Mode: cpu.ModeThumb, Attributed: true,
		}
		var instruction uint16
		var err error
		if !b.mmuEnabled() && pc >= b.executeAddress {
			offset := uint64(pc - b.executeAddress)
			if offset+2 <= uint64(len(b.executeData)) {
				index := int(offset)
				instruction = uint16(b.executeData[index]) |
					uint16(b.executeData[index+1])<<8
			} else {
				instruction, err = b.fetch16(pc)
			}
		} else {
			instruction, err = b.fetch16(pc)
		}
		if err != nil {
			if b.handleMMUFault(err, pc) {
				return executed, nil, nil
			}
			return executed, nil, fmt.Errorf("Thumb fetch at 0x%08x: %w", pc, err)
		}
		next := pc + 2
		b.regs[cpu.RegisterPC] = next

		switch thumbInstructionClasses[instruction] {
		case thumbBreakpoint: // BKPT
			reason := cpu.StopBreakpoint
			return executed + 1, &reason, nil

		case thumbShiftImmediate: // LSL/LSR/ASR immediate
			op := uint32(instruction>>11) & 3
			shift := uint32(instruction>>6) & 0x1f
			rs := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			value := b.regs[rs]
			var result uint32
			var carry bool
			switch op {
			case 0: // LSL
				if shift == 0 {
					result = value
					carry = b.carry()
				} else {
					result = value << shift
					carry = value&(uint32(1)<<(32-shift)) != 0
				}
			case 1: // LSR, immediate zero encodes a shift of 32
				if shift == 0 {
					result = 0
					carry = value&flagN != 0
				} else {
					result = value >> shift
					carry = value&(uint32(1)<<(shift-1)) != 0
				}
			case 2: // ASR, immediate zero encodes a shift of 32
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
				return executed, nil, b.unsupportedThumb(pc, instruction)
			}
			b.regs[rd] = result
			b.setNZC(result, carry)
			break

		case thumbMoveImmediate: // MOVS Rd, #imm8
			rd := uint32(instruction>>8) & 7
			value := uint32(instruction & 0xff)
			b.regs[rd] = value
			b.setNZ(value)
			break

		case thumbCompareImmediate: // CMP Rd, #imm8
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)
			b.setNZCV(result, carry, overflow)
			break

		case thumbAddImmediate: // ADDS Rd, #imm8
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], uint32(instruction&0xff), 0)
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)
			break

		case thumbSubtractImmediate: // SUBS Rd, #imm8
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)
			break

		case thumbAddSubtract: // ADD/SUB register or immediate3
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
			break

		case thumbALU: // ALU operations
			op := (instruction >> 6) & 0xf
			rs := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			left, right := b.regs[rd], b.regs[rs]
			switch op {
			case 0x0: // AND
				b.regs[rd] = left & right
				b.setNZ(b.regs[rd])
			case 0x1: // EOR
				b.regs[rd] = left ^ right
				b.setNZ(b.regs[rd])
			case 0x2: // LSL by register
				result, carry := shiftLSL(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x3: // LSR by register
				result, carry := shiftLSR(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x4: // ASR by register
				result, carry := shiftASR(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x5: // ADC
				carryIn := uint32(0)
				if b.carry() {
					carryIn = 1
				}
				result, carry, overflow := addWithCarry(left, right, carryIn)
				b.regs[rd] = result
				b.setNZCV(result, carry, overflow)
			case 0x6: // SBC
				carryIn := uint32(0)
				if b.carry() {
					carryIn = 1
				}
				result, carry, overflow := addWithCarry(left, ^right, carryIn)
				b.regs[rd] = result
				b.setNZCV(result, carry, overflow)
			case 0x7: // ROR by register
				result, carry := shiftROR(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x8: // TST
				b.setNZ(left & right)
			case 0x9: // NEG
				result, carry, overflow := addWithCarry(0, ^right, 1)
				b.regs[rd] = result
				b.setNZCV(result, carry, overflow)
			case 0xa: // CMP
				result, carry, overflow := addWithCarry(left, ^right, 1)
				b.setNZCV(result, carry, overflow)
			case 0xb: // CMN
				result, carry, overflow := addWithCarry(left, right, 0)
				b.setNZCV(result, carry, overflow)
			case 0xc: // ORR
				b.regs[rd] = left | right
				b.setNZ(b.regs[rd])
			case 0xd: // MUL
				b.regs[rd] = left * right
				b.setNZ(b.regs[rd])
			case 0xe: // BIC
				b.regs[rd] = left &^ right
				b.setNZ(b.regs[rd])
			case 0xf: // MVN
				b.regs[rd] = ^right
				b.setNZ(b.regs[rd])
			default:
				return executed, nil, b.unsupportedThumb(pc, instruction)
			}
			break

		case thumbHighRegister: // high-register ops / BX
			op := (instruction >> 8) & 3
			rs := uint32(instruction>>3)&7 | uint32(instruction>>6)&1<<3
			rd := uint32(instruction)&7 | uint32(instruction>>7)&1<<3
			switch op {
			case 0: // ADD
				result := b.readOperandRegister(rd, pc, cpu.ModeThumb) +
					b.readOperandRegister(rs, pc, cpu.ModeThumb)
				if rd == cpu.RegisterPC {
					result &^= 1
				}
				b.regs[rd] = result
			case 1: // CMP
				result, carry, overflow := addWithCarry(
					b.readOperandRegister(rd, pc, cpu.ModeThumb),
					^b.readOperandRegister(rs, pc, cpu.ModeThumb),
					1,
				)
				b.setNZCV(result, carry, overflow)
			case 2: // MOV
				result := b.readOperandRegister(rs, pc, cpu.ModeThumb)
				if rd == cpu.RegisterPC {
					result &^= 1
				}
				b.regs[rd] = result
			case 3: // BX
				target := b.readOperandRegister(rs, pc, cpu.ModeThumb)
				if instruction&(1<<7) != 0 { // BLX
					b.regs[cpu.RegisterLR] = (pc + 2) | 1
				}
				b.branchExchange(target)
			}
			break

		case thumbLiteralLoad: // LDR Rd, [PC, #imm]
			rd := uint32(instruction>>8) & 7
			address := ((pc + 4) &^ uint32(3)) +
				uint32(instruction&0xff)*4
			value, readErr := b.read32(address, cpu.PermissionRead)
			if readErr != nil {
				return executed, nil, readErr
			}
			b.regs[rd] = value
			break

		case thumbRegisterTransfer: // register-offset load/store
			op := uint32(instruction>>9) & 7
			ro := uint32(instruction>>6) & 7
			rb := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			address := b.regs[rb] + b.regs[ro]
			switch op {
			case 0: // STR
				if writeErr := b.write32(address, b.regs[rd], cpu.PermissionWrite); writeErr != nil {
					return executed, nil, writeErr
				}
			case 1: // STRH
				if writeErr := b.write16(address, uint16(b.regs[rd]), cpu.PermissionWrite); writeErr != nil {
					return executed, nil, writeErr
				}
			case 2: // STRB
				if writeErr := b.write8(
					address,
					byte(b.regs[rd]),
					cpu.PermissionWrite,
				); writeErr != nil {
					return executed, nil, writeErr
				}
			case 3: // LDRSB
				value, readErr := b.read8(address, cpu.PermissionRead)
				if readErr != nil {
					return executed, nil, readErr
				}
				b.regs[rd] = uint32(int32(int8(value)))
			case 4: // LDR
				value, readErr := b.read32(address, cpu.PermissionRead)
				if readErr != nil {
					return executed, nil, readErr
				}
				b.regs[rd] = value
			case 5: // LDRH
				value, readErr := b.read16(address, cpu.PermissionRead)
				if readErr != nil {
					return executed, nil, readErr
				}
				b.regs[rd] = uint32(value)
			case 6: // LDRB
				value, readErr := b.read8(address, cpu.PermissionRead)
				if readErr != nil {
					return executed, nil, readErr
				}
				b.regs[rd] = uint32(value)
			case 7: // LDRSH
				value, readErr := b.read16(address, cpu.PermissionRead)
				if readErr != nil {
					return executed, nil, readErr
				}
				b.regs[rd] = uint32(int32(int16(value)))
			}
			break

		case thumbImmediateTransfer: // immediate word/byte load/store
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
				value, readErr := b.read8(address, cpu.PermissionRead)
				if readErr != nil {
					return executed, nil, readErr
				}
				b.regs[rd] = uint32(value)
			case load:
				value, readErr := b.read32(address, cpu.PermissionRead)
				if readErr != nil {
					return executed, nil, readErr
				}
				b.regs[rd] = value
			case byteTransfer:
				if writeErr := b.write8(
					address,
					byte(b.regs[rd]),
					cpu.PermissionWrite,
				); writeErr != nil {
					return executed, nil, writeErr
				}
			default:
				if writeErr := b.write32(address, b.regs[rd], cpu.PermissionWrite); writeErr != nil {
					return executed, nil, writeErr
				}
			}
			break

		case thumbHalfwordTransfer: // immediate halfword load/store
			load := instruction&(1<<11) != 0
			offset := uint32(instruction>>6) & 0x1f
			rb := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			address := b.regs[rb] + offset*2
			if load {
				value, readErr := b.read16(address, cpu.PermissionRead)
				if readErr != nil {
					return executed, nil, readErr
				}
				b.regs[rd] = uint32(value)
			} else if writeErr := b.write16(
				address,
				uint16(b.regs[rd]),
				cpu.PermissionWrite,
			); writeErr != nil {
				return executed, nil, writeErr
			}
			break

		case thumbStackTransfer: // SP-relative word load/store
			load := instruction&(1<<11) != 0
			rd := uint32(instruction>>8) & 7
			address := b.regs[cpu.RegisterSP] + uint32(instruction&0xff)*4
			if load {
				value, readErr := b.read32(address, cpu.PermissionRead)
				if readErr != nil {
					return executed, nil, readErr
				}
				b.regs[rd] = value
			} else if writeErr := b.write32(
				address,
				b.regs[rd],
				cpu.PermissionWrite,
			); writeErr != nil {
				return executed, nil, writeErr
			}
			break

		case thumbAdjustStack: // ADD/SUB SP, #imm7*4
			offset := uint32(instruction&0x7f) * 4
			if instruction&(1<<7) != 0 {
				b.regs[cpu.RegisterSP] -= offset
			} else {
				b.regs[cpu.RegisterSP] += offset
			}
			break

		case thumbPush: // PUSH
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
				if writeErr := b.write32(address, b.regs[register], cpu.PermissionWrite); writeErr != nil {
					return executed, nil, writeErr
				}
				address += 4
			}
			if includeLR {
				if writeErr := b.write32(address, b.regs[cpu.RegisterLR], cpu.PermissionWrite); writeErr != nil {
					return executed, nil, writeErr
				}
			}
			b.regs[cpu.RegisterSP] = start
			break

		case thumbPop: // POP
			registers := uint16(instruction & 0xff)
			includePC := instruction&(1<<8) != 0
			address := b.regs[cpu.RegisterSP]
			for register := uint32(0); register < 8; register++ {
				if registers&(1<<register) == 0 {
					continue
				}
				value, readErr := b.read32(address, cpu.PermissionRead)
				if readErr != nil {
					return executed, nil, readErr
				}
				b.regs[register] = value
				address += 4
			}
			if includePC {
				value, readErr := b.read32(address, cpu.PermissionRead)
				if readErr != nil {
					return executed, nil, readErr
				}
				b.branchExchange(value)
				address += 4
			}
			b.regs[cpu.RegisterSP] = address
			break

		case thumbAddPCSP: // ADD Rd, PC/SP, #imm
			rd := uint32(instruction>>8) & 7
			base := b.regs[cpu.RegisterSP]
			if instruction&(1<<11) == 0 {
				base = (pc + 4) &^ uint32(3)
			}
			b.regs[rd] = base + uint32(instruction&0xff)*4
			break

		case thumbMultipleTransfer: // STMIA/LDMIA Rb!, register list
			load := instruction&(1<<11) != 0
			rb := uint32(instruction>>8) & 7
			registers := uint16(instruction & 0xff)
			if registers == 0 {
				return executed, nil, b.unsupportedThumb(pc, instruction)
			}
			address := b.regs[rb]
			for register := uint32(0); register < 8; register++ {
				if registers&(1<<register) == 0 {
					continue
				}
				if load {
					value, readErr := b.read32(address, cpu.PermissionRead)
					if readErr != nil {
						return executed, nil, readErr
					}
					b.regs[register] = value
				} else if writeErr := b.write32(
					address,
					b.regs[register],
					cpu.PermissionWrite,
				); writeErr != nil {
					return executed, nil, writeErr
				}
				address += 4
			}
			// ARMv5 Thumb LDM suppresses writeback when the base register is in
			// the list; STM always performs writeback for the encodings used here.
			if !load || registers&(1<<rb) == 0 {
				b.regs[rb] = address
			}
			break

		case thumbConditionalBranch: // conditional branch / SWI
			condition := uint8(instruction>>8) & 0xf
			if condition == 0xf {
				if uint8(instruction) == semihostingThumbImmediate && b.handleSemihosting() {
					break
				}
				if b.systemBus != nil {
					b.enterException(processorModeSupervisor, vectorSoftware, pc+2)
					break
				}
				reason := cpu.StopBreakpoint
				return executed + 1, &reason, nil
			}
			if condition == 0xe {
				return executed, nil, b.unsupportedThumb(pc, instruction)
			}
			if b.conditionPassed(condition) {
				offset := int32(int8(instruction&0xff)) << 1
				b.regs[cpu.RegisterPC] = uint32(int32(pc+4) + offset)
			}
			break

		case thumbLongBranch: // BL (two-halfword Thumb instruction)
			suffix, readErr := b.fetch16(pc + 2)
			if readErr != nil {
				return executed, nil, readErr
			}
			if suffix&0xf801 == 0xe800 { // BLX immediate
				high := int32(instruction & 0x7ff)
				if high&(1<<10) != 0 {
					high |= ^int32(0x7ff)
				}
				target := uint32(int32((pc+4)&^uint32(3))+(high<<12)) +
					uint32(suffix&0x7fe)*2
				b.regs[cpu.RegisterLR] = (pc + 4) | 1
				b.branchExchange(target)
				break
			}
			if suffix&0xf800 != 0xf800 {
				return executed, nil, b.unsupportedThumb(pc, instruction)
			}
			high := int32(instruction & 0x7ff)
			if high&(1<<10) != 0 {
				high |= ^int32(0x7ff)
			}
			target := uint32(int32(pc+4)+(high<<12)) +
				uint32(suffix&0x7ff)*2
			b.regs[cpu.RegisterLR] = (pc + 4) | 1
			b.regs[cpu.RegisterPC] = target
			break

		case thumbLongBranchSuffix:
			return executed, nil, b.unsupportedThumb(pc, instruction)

		case thumbUnconditionalBranch: // unconditional branch
			offset := int32(instruction & 0x7ff)
			if offset&(1<<10) != 0 {
				offset |= ^int32(0x7ff)
			}
			b.regs[cpu.RegisterPC] = uint32(int32(pc+4) + (offset << 1))
			break

		default:
			return executed, nil, b.unsupportedThumb(pc, instruction)
		}
		executed++
		if b.mode != cpu.ModeThumb {
			// A branch-exchange handed control to ARM; let the caller resume
			// dispatch in the other decoder.
			return executed, nil, nil
		}
	}
	return executed, nil, nil
}
