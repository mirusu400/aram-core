package interpreter

import (
	"fmt"
	"math/bits"

	"github.com/mirusu400/aram-core/cpu"
)

// runARM executes up to limit ARM instructions from b.regs[PC], stopping early
// on a stop reason, a fault, or a switch to Thumb mode. ARM decode is a linear
// mask match rather than a table lookup and is not the hot path for the current
// corpus, so it keeps the per-instruction stepARM call; batching still amortizes
// the outer cancellation poll. It returns the number of instructions retired.
func (b *Backend) runARM(limit uint64) (uint64, *cpu.StopReason, error) {
	var executed uint64
	for executed < limit && b.mode == cpu.ModeARM {
		if b.takePendingInterrupt() {
			continue
		}
		if b.executionTrapAt(cpu.ModeARM, b.regs[cpu.RegisterPC]) {
			reason := cpu.StopExecutionTrap
			return executed, &reason, nil
		}
		b.recordPC(b.regs[cpu.RegisterPC])
		pc := b.regs[cpu.RegisterPC]
		b.accessContext = cpu.MemoryAccessContext{
			InstructionAddress: pc, LinkAddress: b.regs[cpu.RegisterLR],
			StackAddress: b.regs[cpu.RegisterSP], Mode: cpu.ModeARM, Attributed: true,
		}
		reason, err := b.stepARM()
		if err != nil {
			if b.handleMMUFault(err, pc) {
				continue
			}
			return executed, nil, err
		}
		executed++
		if reason != nil {
			return executed, reason, nil
		}
	}
	return executed, nil, nil
}

func (b *Backend) stepARM() (*cpu.StopReason, error) {
	pc := b.regs[cpu.RegisterPC]
	var instruction uint32
	var err error
	if !b.mmuEnabled() && pc >= b.executeAddress {
		offset := uint64(pc - b.executeAddress)
		if offset+4 <= uint64(len(b.executeData)) {
			index := int(offset)
			instruction = uint32(b.executeData[index]) |
				uint32(b.executeData[index+1])<<8 |
				uint32(b.executeData[index+2])<<16 |
				uint32(b.executeData[index+3])<<24
		} else {
			instruction, err = b.fetch32(pc)
		}
	} else {
		instruction, err = b.fetch32(pc)
	}
	if err != nil {
		return nil, fmt.Errorf("ARM fetch at 0x%08x: %w", pc, err)
	}
	b.regs[cpu.RegisterPC] = pc + 4
	condition := uint8(instruction >> 28)
	if condition != 0xf && !b.conditionPassed(condition) {
		return nil, nil
	}

	switch {
	case instruction&0x0ff000f0 == 0x01200070: // BKPT
		reason := cpu.StopBreakpoint
		return &reason, nil

	case instruction&0x0fbf0fff == 0x010f0000: // MRS Rd, CPSR/SPSR
		rd := uint32(instruction>>12) & 0xf
		if rd == cpu.RegisterPC {
			return nil, b.unsupportedARM(pc, instruction)
		}
		value, statusErr := b.readProgramStatus(instruction&(1<<22) != 0)
		if statusErr != nil {
			return nil, fmt.Errorf("ARM MRS at 0x%08x: %w", pc, statusErr)
		}
		b.regs[rd] = value
		return nil, nil

	case instruction&0x0fb0fff0 == 0x0120f000: // MSR CPSR/SPSR_fields, Rm
		rm := uint32(instruction) & 0xf
		if rm == cpu.RegisterPC {
			return nil, b.unsupportedARM(pc, instruction)
		}
		if statusErr := b.writeProgramStatus(
			instruction&(1<<22) != 0,
			uint32(instruction>>16)&0xf,
			b.regs[rm],
		); statusErr != nil {
			return nil, fmt.Errorf("ARM MSR at 0x%08x: %w", pc, statusErr)
		}
		return nil, nil

	case instruction&0x0fb0f000 == 0x0320f000: // MSR CPSR/SPSR_fields, #imm
		rotate := int((instruction >> 8 & 0xf) * 2)
		value := bits.RotateLeft32(uint32(instruction&0xff), -rotate)
		if statusErr := b.writeProgramStatus(
			instruction&(1<<22) != 0,
			uint32(instruction>>16)&0xf,
			value,
		); statusErr != nil {
			return nil, fmt.Errorf("ARM MSR at 0x%08x: %w", pc, statusErr)
		}
		return nil, nil

	case instruction&0x0ffffff0 == 0x012fff10: // BX Rm
		b.branchExchange(b.readOperandRegister(instruction&0xf, pc, cpu.ModeARM))
		return nil, nil

	case instruction&0x0ffffff0 == 0x012fff30: // BLX Rm
		target := b.readOperandRegister(instruction&0xf, pc, cpu.ModeARM)
		b.regs[cpu.RegisterLR] = pc + 4
		b.branchExchange(target)
		return nil, nil

	case instruction&0xfe000000 == 0xfa000000: // BLX immediate
		offset := int32(instruction&0x00ffffff) << 2
		if offset&(1<<25) != 0 {
			offset |= ^int32(0x03ffffff)
		}
		if instruction&(1<<24) != 0 {
			offset += 2
		}
		b.regs[cpu.RegisterLR] = pc + 4
		b.regs[cpu.RegisterPC] = uint32(int32(pc+8) + offset)
		b.mode = cpu.ModeThumb
		b.setModeFlag()
		return nil, nil

	case instruction&0x0f000010 == 0x0e000010: // MRC/MCR coprocessor transfer
		if cpErr := b.executeCP15(pc, instruction); cpErr != nil {
			return nil, cpErr
		}
		return nil, nil

	case instruction&0x0e000000 == 0x0a000000: // B / BL
		offset := int32(instruction&0x00ffffff) << 2
		if offset&(1<<25) != 0 {
			offset |= ^int32(0x03ffffff)
		}
		if instruction&(1<<24) != 0 {
			b.regs[cpu.RegisterLR] = pc + 4
		}
		b.regs[cpu.RegisterPC] = uint32(int32(pc+8) + offset)
		return nil, nil

	case instruction&0x0fff0ff0 == 0x016f0f10: // CLZ
		rd := uint32(instruction>>12) & 0xf
		rm := uint32(instruction) & 0xf
		if rd == cpu.RegisterPC || rm == cpu.RegisterPC {
			return nil, b.unsupportedARM(pc, instruction)
		}
		b.regs[rd] = uint32(bits.LeadingZeros32(b.regs[rm]))
		return nil, nil

	case instruction&0x0fb00ff0 == 0x01000090: // SWP / SWPB
		byteTransfer := instruction&(1<<22) != 0
		rn := uint32(instruction>>16) & 0xf
		rd := uint32(instruction>>12) & 0xf
		rm := uint32(instruction) & 0xf
		if rn == cpu.RegisterPC || rd == cpu.RegisterPC || rm == cpu.RegisterPC {
			return nil, b.unsupportedARM(pc, instruction)
		}
		address := b.regs[rn]
		stored := b.regs[rm]
		if byteTransfer {
			loaded, readErr := b.read8(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			if writeErr := b.write8(
				address,
				byte(stored),
				cpu.PermissionWrite,
			); writeErr != nil {
				return nil, writeErr
			}
			b.regs[rd] = uint32(loaded)
			return nil, nil
		}
		loaded, readErr := b.read32(address, cpu.PermissionRead)
		if readErr != nil {
			return nil, readErr
		}
		if writeErr := b.write32(
			address,
			stored,
			cpu.PermissionWrite,
		); writeErr != nil {
			return nil, writeErr
		}
		b.regs[rd] = loaded
		return nil, nil

	case instruction&0x0fc000f0 == 0x00000090: // MUL / MLA
		accumulate := instruction&(1<<21) != 0
		setFlags := instruction&(1<<20) != 0
		rd := uint32(instruction>>16) & 0xf
		rn := uint32(instruction>>12) & 0xf
		rs := uint32(instruction>>8) & 0xf
		rm := uint32(instruction) & 0xf
		if rd == cpu.RegisterPC || rs == cpu.RegisterPC ||
			rm == cpu.RegisterPC || (accumulate && rn == cpu.RegisterPC) {
			return nil, b.unsupportedARM(pc, instruction)
		}
		result := b.regs[rm] * b.regs[rs]
		if accumulate {
			result += b.regs[rn]
		}
		b.regs[rd] = result
		if setFlags {
			b.setNZ(result)
		}
		return nil, nil

	case instruction&0x0f8000f0 == 0x00800090: // UMULL / UMLAL / SMULL / SMLAL
		signed := instruction&(1<<22) != 0
		accumulate := instruction&(1<<21) != 0
		setFlags := instruction&(1<<20) != 0
		rdHi := uint32(instruction>>16) & 0xf
		rdLo := uint32(instruction>>12) & 0xf
		rs := uint32(instruction>>8) & 0xf
		rm := uint32(instruction) & 0xf
		if rdHi == cpu.RegisterPC || rdLo == cpu.RegisterPC ||
			rs == cpu.RegisterPC || rm == cpu.RegisterPC || rdHi == rdLo {
			return nil, b.unsupportedARM(pc, instruction)
		}
		var product uint64
		if signed {
			product = uint64(int64(int32(b.regs[rm])) * int64(int32(b.regs[rs])))
		} else {
			product = uint64(b.regs[rm]) * uint64(b.regs[rs])
		}
		if accumulate {
			product += uint64(b.regs[rdHi])<<32 | uint64(b.regs[rdLo])
		}
		b.regs[rdHi] = uint32(product >> 32)
		b.regs[rdLo] = uint32(product)
		if setFlags {
			// Long multiply sets only N and Z (from the 64-bit product) and
			// leaves C and V; materialize any deferred update first so those
			// surviving bits are correct.
			b.resolveFlags()
			b.regs[cpu.RegisterCPSR] &^= flagN | flagZ
			if product == 0 {
				b.regs[cpu.RegisterCPSR] |= flagZ
			}
			if product&(uint64(1)<<63) != 0 {
				b.regs[cpu.RegisterCPSR] |= flagN
			}
		}
		return nil, nil

	case instruction&0x0e000090 == 0x00000090 &&
		instruction&0x00000060 != 0: // halfword / signed byte transfer
		preIndex := instruction&(1<<24) != 0
		up := instruction&(1<<23) != 0
		immediate := instruction&(1<<22) != 0
		writeBack := instruction&(1<<21) != 0
		load := instruction&(1<<20) != 0
		rn := uint32(instruction>>16) & 0xf
		rd := uint32(instruction>>12) & 0xf
		operation := uint8(instruction>>5) & 3
		// LDRD and STRD transfer a register pair and are not part of the
		// ARMv5TE subset the WIPI toolchains emit for these titles.
		if rd == cpu.RegisterPC || (!load && operation != 1) {
			return nil, b.unsupportedARM(pc, instruction)
		}
		var offset uint32
		if immediate {
			offset = uint32(instruction>>4)&0xf0 | uint32(instruction&0xf)
		} else {
			rm := uint32(instruction) & 0xf
			if instruction&0x00000f00 != 0 || rm == cpu.RegisterPC {
				return nil, b.unsupportedARM(pc, instruction)
			}
			offset = b.regs[rm]
		}
		base := b.readOperandRegister(rn, pc, cpu.ModeARM)
		indexedAddress := base
		if up {
			indexedAddress += offset
		} else {
			indexedAddress -= offset
		}
		address := base
		if preIndex {
			address = indexedAddress
		}
		switch {
		case !load: // STRH
			if writeErr := b.write16(
				address,
				uint16(b.regs[rd]),
				cpu.PermissionWrite,
			); writeErr != nil {
				return nil, writeErr
			}
		case operation == 1: // LDRH
			value, readErr := b.read16(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = uint32(value)
		case operation == 2: // LDRSB
			value, readErr := b.read8(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = uint32(int32(int8(value)))
		default: // LDRSH
			value, readErr := b.read16(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = uint32(int32(int16(value)))
		}
		if (!preIndex || writeBack) && !(load && rd == rn) {
			b.regs[rn] = indexedAddress
		}
		return nil, nil

	case instruction&0x0c000000 == 0x00000000: // data processing
		immediate := instruction&(1<<25) != 0
		opcode := uint8(instruction >> 21 & 0xf)
		setFlags := instruction&(1<<20) != 0
		rn := uint32(instruction>>16) & 0xf
		rd := uint32(instruction>>12) & 0xf
		var operand2 uint32
		operandCarry := b.carry()
		if immediate {
			rotate := int((instruction >> 8 & 0xf) * 2)
			operand2 = bits.RotateLeft32(uint32(instruction&0xff), -rotate)
			if rotate != 0 {
				operandCarry = operand2&flagN != 0
			}
		} else {
			rm := uint32(instruction & 0xf)
			value := b.readOperandRegister(rm, pc, cpu.ModeARM)
			shiftType := uint8(instruction>>5) & 3
			if instruction&(1<<4) == 0 {
				amount := uint8(instruction >> 7 & 0x1f)
				switch shiftType {
				case 0:
					operand2, operandCarry = shiftLSL(value, amount, operandCarry)
				case 1:
					if amount == 0 {
						amount = 32
					}
					operand2, operandCarry = shiftLSR(value, amount, operandCarry)
				case 2:
					if amount == 0 {
						amount = 32
					}
					operand2, operandCarry = shiftASR(value, amount, operandCarry)
				case 3:
					if amount == 0 {
						oldCarry := operandCarry
						operandCarry = value&1 != 0
						operand2 = value >> 1
						if oldCarry {
							operand2 |= flagN
						}
					} else {
						operand2, operandCarry = shiftROR(value, amount, operandCarry)
					}
				}
			} else {
				if instruction&(1<<7) != 0 {
					return nil, b.unsupportedARM(pc, instruction)
				}
				rs := uint32(instruction>>8) & 0xf
				amount := uint8(b.readOperandRegister(rs, pc, cpu.ModeARM))
				switch shiftType {
				case 0:
					operand2, operandCarry = shiftLSL(value, amount, operandCarry)
				case 1:
					operand2, operandCarry = shiftLSR(value, amount, operandCarry)
				case 2:
					operand2, operandCarry = shiftASR(value, amount, operandCarry)
				case 3:
					operand2, operandCarry = shiftROR(value, amount, operandCarry)
				}
			}
		}
		left := b.readOperandRegister(rn, pc, cpu.ModeARM)
		var result uint32
		var carry, overflow bool
		writeResult := true
		arithmeticFlags := false
		switch opcode {
		case 0x0: // AND
			result = left & operand2
		case 0x1: // EOR
			result = left ^ operand2
		case 0x2: // SUB
			result, carry, overflow = addWithCarry(left, ^operand2, 1)
			arithmeticFlags = true
		case 0x3: // RSB
			result, carry, overflow = addWithCarry(operand2, ^left, 1)
			arithmeticFlags = true
		case 0x4: // ADD
			result, carry, overflow = addWithCarry(left, operand2, 0)
			arithmeticFlags = true
		case 0x5: // ADC
			carryIn := uint32(0)
			if b.carry() {
				carryIn = 1
			}
			result, carry, overflow = addWithCarry(left, operand2, carryIn)
			arithmeticFlags = true
		case 0x6: // SBC
			carryIn := uint32(0)
			if b.carry() {
				carryIn = 1
			}
			result, carry, overflow = addWithCarry(left, ^operand2, carryIn)
			arithmeticFlags = true
		case 0x7: // RSC
			carryIn := uint32(0)
			if b.carry() {
				carryIn = 1
			}
			result, carry, overflow = addWithCarry(operand2, ^left, carryIn)
			arithmeticFlags = true
		case 0x8: // TST
			result = left & operand2
			setFlags = true
			writeResult = false
		case 0x9: // TEQ
			result = left ^ operand2
			setFlags = true
			writeResult = false
		case 0xa: // CMP
			result, carry, overflow = addWithCarry(left, ^operand2, 1)
			setFlags = true
			writeResult = false
			arithmeticFlags = true
		case 0xb: // CMN
			result, carry, overflow = addWithCarry(left, operand2, 0)
			setFlags = true
			writeResult = false
			arithmeticFlags = true
		case 0xc: // ORR
			result = left | operand2
		case 0xd: // MOV
			result = operand2
		case 0xe: // BIC
			result = left &^ operand2
		case 0xf: // MVN
			result = ^operand2
		default:
			return nil, b.unsupportedARM(pc, instruction)
		}
		exceptionReturn := writeResult && rd == cpu.RegisterPC && setFlags
		if exceptionReturn {
			status := b.savedStatus(b.currentProcessorMode())
			if status == nil {
				return nil, b.unsupportedARM(pc, instruction)
			}
			if statusErr := b.writeProgramStatus(false, 0xf, *status); statusErr != nil {
				return nil, fmt.Errorf("ARM data-processing exception return at 0x%08x: %w", pc, statusErr)
			}
			if b.mode == cpu.ModeThumb {
				b.regs[cpu.RegisterPC] = result &^ 1
			} else {
				b.regs[cpu.RegisterPC] = result &^ 3
			}
		} else if writeResult {
			if rd == cpu.RegisterPC {
				b.regs[rd] = result &^ 3
			} else {
				b.regs[rd] = result
			}
		}
		if setFlags && !exceptionReturn {
			if arithmeticFlags {
				b.setNZCV(result, carry, overflow)
			} else {
				b.setNZC(result, operandCarry)
			}
		}
		return nil, nil

	case instruction&0x0c000000 == 0x04000000: // LDR/STR
		registerOffset := instruction&(1<<25) != 0
		preIndex := instruction&(1<<24) != 0
		up := instruction&(1<<23) != 0
		byteTransfer := instruction&(1<<22) != 0
		writeBack := instruction&(1<<21) != 0
		load := instruction&(1<<20) != 0
		rn := uint32(instruction>>16) & 0xf
		rd := uint32(instruction>>12) & 0xf
		base := b.readOperandRegister(rn, pc, cpu.ModeARM)
		var offset uint32
		if !registerOffset {
			offset = uint32(instruction & 0xfff)
		} else {
			if instruction&(1<<4) != 0 {
				return nil, b.unsupportedARM(pc, instruction)
			}
			rm := uint32(instruction & 0xf)
			value := b.readOperandRegister(rm, pc, cpu.ModeARM)
			shiftType := uint8(instruction>>5) & 3
			amount := uint8(instruction >> 7 & 0x1f)
			switch shiftType {
			case 0:
				offset, _ = shiftLSL(value, amount, b.carry())
			case 1:
				if amount == 0 {
					amount = 32
				}
				offset, _ = shiftLSR(value, amount, b.carry())
			case 2:
				if amount == 0 {
					amount = 32
				}
				offset, _ = shiftASR(value, amount, b.carry())
			case 3:
				if amount == 0 {
					offset = value >> 1
					if b.carry() {
						offset |= flagN
					}
				} else {
					offset, _ = shiftROR(value, amount, b.carry())
				}
			}
		}
		indexedAddress := base
		if up {
			indexedAddress += offset
		} else {
			indexedAddress -= offset
		}
		address := base
		if preIndex {
			address = indexedAddress
		}
		if load {
			if byteTransfer {
				value, readErr := b.read8(address, cpu.PermissionRead)
				if readErr != nil {
					return nil, readErr
				}
				if rd == cpu.RegisterPC {
					b.branchExchange(uint32(value))
				} else {
					b.regs[rd] = uint32(value)
				}
			} else {
				value, readErr := b.read32(address, cpu.PermissionRead)
				if readErr != nil {
					return nil, readErr
				}
				if rd == cpu.RegisterPC {
					b.branchExchange(value)
				} else {
					b.regs[rd] = value
				}
			}
		} else if byteTransfer {
			value := b.regs[rd]
			if rd == cpu.RegisterPC {
				value = pc + 12
			}
			if writeErr := b.write8(address, byte(value), cpu.PermissionWrite); writeErr != nil {
				return nil, writeErr
			}
		} else {
			value := b.regs[rd]
			if rd == cpu.RegisterPC {
				value = pc + 12
			}
			if writeErr := b.write32(address, value, cpu.PermissionWrite); writeErr != nil {
				return nil, writeErr
			}
		}
		if (!preIndex || writeBack) && !(load && rd == rn) {
			b.regs[rn] = indexedAddress
		}
		return nil, nil

	case instruction&0x0e000000 == 0x08000000: // LDM/STM
		preIndex := instruction&(1<<24) != 0
		increment := instruction&(1<<23) != 0
		userOrPSR := instruction&(1<<22) != 0
		writeBack := instruction&(1<<21) != 0
		load := instruction&(1<<20) != 0
		rn := uint32(instruction>>16) & 0xf
		registers := uint16(instruction)
		if registers == 0 || rn == cpu.RegisterPC {
			return nil, b.unsupportedARM(pc, instruction)
		}
		exceptionReturn := userOrPSR && load && registers&(1<<cpu.RegisterPC) != 0
		transferUser := userOrPSR && !exceptionReturn
		currentMode := b.currentProcessorMode()
		if transferUser && (currentMode == processorModeUser || currentMode == processorModeSystem) {
			return nil, b.unsupportedARM(pc, instruction)
		}
		var restoredStatus uint32
		if exceptionReturn {
			status := b.savedStatus(currentMode)
			if status == nil {
				return nil, b.unsupportedARM(pc, instruction)
			}
			restoredStatus = *status
		}
		count := uint32(bits.OnesCount16(registers))
		base := b.readOperandRegister(rn, pc, cpu.ModeARM)
		var address uint32
		if increment {
			address = base
			if preIndex {
				address += 4
			}
		} else {
			address = base - count*4
			if !preIndex {
				address += 4
			}
		}
		var loadedPC uint32
		loadedProgramCounter := false
		for register := uint32(0); register < 16; register++ {
			if registers&(1<<register) == 0 {
				continue
			}
			if load {
				value, readErr := b.read32(address, cpu.PermissionRead)
				if readErr != nil {
					return nil, readErr
				}
				if register == cpu.RegisterPC {
					loadedPC = value
					loadedProgramCounter = true
				} else if transferUser {
					b.writeUserRegister(register, value)
				} else {
					b.regs[register] = value
				}
			} else {
				var value uint32
				if register == cpu.RegisterPC {
					value = pc + 12
				} else if transferUser {
					value = b.readUserRegister(register)
				} else {
					value = b.regs[register]
					if register == rn {
						value = base
					}
				}
				if writeErr := b.write32(address, value, cpu.PermissionWrite); writeErr != nil {
					return nil, writeErr
				}
			}
			address += 4
		}
		if writeBack && (!load || registers&(1<<rn) == 0) {
			if increment {
				b.regs[rn] = base + count*4
			} else {
				b.regs[rn] = base - count*4
			}
		}
		if loadedProgramCounter {
			if exceptionReturn {
				if statusErr := b.writeProgramStatus(false, 0xf, restoredStatus); statusErr != nil {
					return nil, fmt.Errorf("ARM LDM exception return at 0x%08x: %w", pc, statusErr)
				}
				if b.mode == cpu.ModeThumb {
					b.regs[cpu.RegisterPC] = loadedPC &^ 1
				} else {
					b.regs[cpu.RegisterPC] = loadedPC &^ 3
				}
			} else {
				b.branchExchange(loadedPC)
			}
		}
		return nil, nil

	case instruction&0x0f000000 == 0x0f000000: // SWI
		if instruction&0x00ffffff == semihostingARMImmediate && b.handleSemihosting() {
			return nil, nil
		}
		if b.systemBus != nil {
			b.enterException(processorModeSupervisor, vectorSoftware, pc+4)
			return nil, nil
		}
		reason := cpu.StopBreakpoint
		return &reason, nil

	default:
		return nil, b.unsupportedARM(pc, instruction)
	}
}

func (b *Backend) branchExchange(target uint32) {
	if target&1 != 0 {
		b.mode = cpu.ModeThumb
		b.regs[cpu.RegisterPC] = target &^ 1
	} else {
		b.mode = cpu.ModeARM
		b.regs[cpu.RegisterPC] = target &^ 3
	}
	b.setModeFlag()
}

func (b *Backend) readOperandRegister(id, instructionAddress uint32, mode cpu.Mode) uint32 {
	if id != cpu.RegisterPC {
		return b.regs[id]
	}
	if mode == cpu.ModeThumb {
		return instructionAddress + 4
	}
	return instructionAddress + 8
}

func (b *Backend) conditionPassed(condition uint8) bool {
	b.resolveFlags()
	cpsr := b.regs[cpu.RegisterCPSR]
	n := cpsr&flagN != 0
	z := cpsr&flagZ != 0
	c := cpsr&flagC != 0
	v := cpsr&flagV != 0
	switch condition {
	case 0x0:
		return z
	case 0x1:
		return !z
	case 0x2:
		return c
	case 0x3:
		return !c
	case 0x4:
		return n
	case 0x5:
		return !n
	case 0x6:
		return v
	case 0x7:
		return !v
	case 0x8:
		return c && !z
	case 0x9:
		return !c || z
	case 0xa:
		return n == v
	case 0xb:
		return n != v
	case 0xc:
		return !z && n == v
	case 0xd:
		return z || n != v
	case 0xe:
		return true
	default:
		return false
	}
}

func (b *Backend) setModeFlag() {
	if b.mode == cpu.ModeThumb {
		b.regs[cpu.RegisterCPSR] |= flagT
	} else {
		b.regs[cpu.RegisterCPSR] &^= flagT
	}
}

// pendingFlags is a deferred N/Z/C/V update: the result and the carry/overflow
// of the last flag-defining ALU op, kept until a reader materializes the CPSR
// bits. Feature-phone Thumb code sets flags on nearly every ALU instruction but
// reads them only at the occasional conditional branch, so most flag updates
// are dead; deferring skips the CPSR bit-twiddling for them. Materialization is
// bit-identical to eager evaluation.
type pendingFlags struct {
	dirty    bool
	value    uint32
	carry    bool
	overflow bool
}

// resolveFlags writes any deferred N/Z/C/V into CPSR. Idempotent; every path
// that reads condition flags (conditionPassed, carry, a CPSR register read, a
// context snapshot) calls it first.
func (b *Backend) resolveFlags() {
	if !b.flags.dirty {
		return
	}
	b.flags.dirty = false
	cpsr := b.regs[cpu.RegisterCPSR] &^ (flagN | flagZ | flagC | flagV)
	if b.flags.value == 0 {
		cpsr |= flagZ
	}
	if b.flags.value&(uint32(1)<<31) != 0 {
		cpsr |= flagN
	}
	if b.flags.carry {
		cpsr |= flagC
	}
	if b.flags.overflow {
		cpsr |= flagV
	}
	b.regs[cpu.RegisterCPSR] = cpsr
}

func (b *Backend) setNZ(value uint32) {
	// Only C and V survive from whatever set them last; materialize that first,
	// then overwrite N and Z.
	b.resolveFlags()
	b.regs[cpu.RegisterCPSR] &^= flagN | flagZ
	if value == 0 {
		b.regs[cpu.RegisterCPSR] |= flagZ
	}
	if value&(uint32(1)<<31) != 0 {
		b.regs[cpu.RegisterCPSR] |= flagN
	}
}

func (b *Backend) setNZCV(value uint32, carry, overflow bool) {
	// Defers the whole N/Z/C/V update; it overwrites every flag bit, so any
	// prior pending update is dead and need not be materialized.
	b.flags = pendingFlags{dirty: true, value: value, carry: carry, overflow: overflow}
}

func (b *Backend) setNZC(value uint32, carry bool) {
	// V survives from whatever set it last; materialize that, then overwrite
	// N, Z and C.
	b.resolveFlags()
	b.regs[cpu.RegisterCPSR] &^= flagN | flagZ | flagC
	if value == 0 {
		b.regs[cpu.RegisterCPSR] |= flagZ
	}
	if value&(uint32(1)<<31) != 0 {
		b.regs[cpu.RegisterCPSR] |= flagN
	}
	if carry {
		b.regs[cpu.RegisterCPSR] |= flagC
	}
}

func (b *Backend) carry() bool {
	b.resolveFlags()
	return b.regs[cpu.RegisterCPSR]&flagC != 0
}

// thumbFlagsDeadBefore reports that the N/Z/C/V a flag-setting Thumb instruction
// is about to write are provably dead because the instruction at pc (its
// sequential successor) unconditionally overwrites all four without reading any.
// Only the immediate add/sub/compare classes qualify: they always set the full
// NZCV from their own operands and never read a flag. Because a qualifying
// successor never reads flags, a chain of them can be skipped safely — the run
// always reaches a real flag write before any reader, so the earlier writes are
// genuinely dead. This is a sound one-instruction test needing no control-flow
// analysis. It peeks only inside the current execute-region slice; if the peek
// would leave it, it returns false so no executeData refresh (a side effect)
// happens during the look-ahead.
func (b *Backend) thumbFlagsDeadBefore(pc uint32) bool {
	if pc < b.executeAddress {
		return false
	}
	offset := uint64(pc - b.executeAddress)
	if offset+2 > uint64(len(b.executeData)) {
		return false
	}
	next := uint16(b.executeData[offset]) | uint16(b.executeData[offset+1])<<8
	switch thumbInstructionClasses[next] {
	case thumbCompareImmediate, thumbAddImmediate, thumbSubtractImmediate, thumbAddSubtract:
		return true
	}
	return false
}

func shiftLSL(value uint32, amount uint8, oldCarry bool) (uint32, bool) {
	switch {
	case amount == 0:
		return value, oldCarry
	case amount < 32:
		return value << amount, value&(uint32(1)<<(32-amount)) != 0
	case amount == 32:
		return 0, value&1 != 0
	default:
		return 0, false
	}
}

func shiftLSR(value uint32, amount uint8, oldCarry bool) (uint32, bool) {
	switch {
	case amount == 0:
		return value, oldCarry
	case amount < 32:
		return value >> amount, value&(uint32(1)<<(amount-1)) != 0
	case amount == 32:
		return 0, value&flagN != 0
	default:
		return 0, false
	}
}

func shiftASR(value uint32, amount uint8, oldCarry bool) (uint32, bool) {
	switch {
	case amount == 0:
		return value, oldCarry
	case amount < 32:
		return uint32(int32(value) >> amount),
			value&(uint32(1)<<(amount-1)) != 0
	default:
		if value&flagN != 0 {
			return ^uint32(0), true
		}
		return 0, false
	}
}

func shiftROR(value uint32, amount uint8, oldCarry bool) (uint32, bool) {
	if amount == 0 {
		return value, oldCarry
	}
	rotation := int(amount & 31)
	if rotation == 0 {
		return value, value&flagN != 0
	}
	result := bits.RotateLeft32(value, -rotation)
	return result, result&flagN != 0
}

func addWithCarry(left, right, carry uint32) (uint32, bool, bool) {
	unsigned := uint64(left) + uint64(right) + uint64(carry)
	result := uint32(unsigned)
	leftSign := left >> 31
	rightSign := right >> 31
	resultSign := result >> 31
	overflow := leftSign == rightSign && leftSign != resultSign
	return result, unsigned>>32 != 0, overflow
}

func (b *Backend) unsupportedThumb(pc uint32, instruction uint16) error {
	return fmt.Errorf("%w: Thumb 0x%04x at 0x%08x",
		cpu.ErrUnsupportedInstruction, instruction, pc)
}

func (b *Backend) unsupportedARM(pc, instruction uint32) error {
	return fmt.Errorf("%w: ARM 0x%08x at 0x%08x",
		cpu.ErrUnsupportedInstruction, instruction, pc)
}
