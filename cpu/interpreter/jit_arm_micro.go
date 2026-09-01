package interpreter

import (
	"encoding/binary"
	"fmt"
	"math/bits"

	"github.com/mirusu400/aram-core/cpu"
)

// classifyARMMemoryMicroOp removes the per-instruction closure call from the
// three ARM memory families. These are both frequent and comparatively large
// closure environments; keeping the raw word beside a prevalidated class makes
// the hot runner a direct switch while retaining the exact scalar/page-batched
// memory helpers used by the decoded closure tier.
func classifyARMMemoryMicroOp(instruction uint32) (armMicroOp, bool, bool) {
	switch {
	case instruction&0x0e000090 == 0x00000090 && instruction&0x00000060 != 0:
		immediate := instruction&(1<<22) != 0
		load := instruction&(1<<20) != 0
		preIndex := instruction&(1<<24) != 0
		writeBack := instruction&(1<<21) != 0
		rn := instruction >> 16 & 0xf
		rd := instruction >> 12 & 0xf
		operation := uint8(instruction>>5) & 3
		rm := instruction & 0xf
		doubleword := !load && operation >= 2
		writeBackAddress := !preIndex || writeBack
		if rd == cpu.RegisterPC ||
			doubleword && (rd&1 != 0 || rd+1 >= cpu.RegisterPC ||
				writeBackAddress && (rn == rd || rn == rd+1)) ||
			(!load && operation != 1 && !doubleword) ||
			(!immediate && (instruction&0x00000f00 != 0 || rm == cpu.RegisterPC)) {
			return armMicroClosure, false, false
		}
		return armMicroHalfwordTransfer, false, true

	case instruction&0x0c000000 == 0x04000000:
		if instruction&(1<<25) != 0 {
			if instruction&(1<<4) != 0 {
				return armMicroClosure, false, false
			}
			if _, ok := decodeARMJITShifter(instruction &^ (1 << 25)); !ok {
				return armMicroClosure, false, false
			}
		}
		load := instruction&(1<<20) != 0
		rd := instruction >> 12 & 0xf
		return armMicroSingleTransfer, load && rd == cpu.RegisterPC, true

	case instruction&0x0e000000 == 0x08000000:
		registers := uint16(instruction)
		rn := instruction >> 16 & 0xf
		if registers == 0 || rn == cpu.RegisterPC {
			return armMicroClosure, false, false
		}
		load := instruction&(1<<20) != 0
		return armMicroBlockTransfer,
			load && registers&(1<<cpu.RegisterPC) != 0, true
	}
	return armMicroClosure, false, false
}

func (b *Backend) executeARMJITInstruction(
	in *jitInstr,
) (bool, *cpu.StopReason, error) {
	switch in.op {
	case armMicroSingleTransfer:
		return b.executeARMMicroSingleTransfer(in.raw, in.pc)
	case armMicroHalfwordTransfer:
		return b.executeARMMicroHalfwordTransfer(in.raw, in.pc)
	case armMicroBlockTransfer:
		return b.executeARMMicroBlockTransfer(in.raw, in.pc)
	default:
		return in.exec(b)
	}
}

func (b *Backend) executeARMMicroSingleTransfer(
	instruction, pc uint32,
) (bool, *cpu.StopReason, error) {
	registerOffset := instruction&(1<<25) != 0
	preIndex := instruction&(1<<24) != 0
	up := instruction&(1<<23) != 0
	byteTransfer := instruction&(1<<22) != 0
	writeBack := instruction&(1<<21) != 0
	load := instruction&(1<<20) != 0
	rn := instruction >> 16 & 0xf
	rd := instruction >> 12 & 0xf
	offset := instruction & 0xfff
	if registerOffset {
		shifter, _ := decodeARMJITShifter(instruction &^ (1 << 25))
		offset, _ = shifter.value(b, pc)
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
	branched := false
	if load {
		if byteTransfer {
			value, err := b.read8(address, cpu.PermissionRead)
			if err != nil {
				return false, nil, err
			}
			if rd == cpu.RegisterPC {
				b.branchExchange(uint32(value))
				branched = true
			} else {
				b.regs[rd] = uint32(value)
			}
		} else {
			value, err := b.read32(address, cpu.PermissionRead)
			if err != nil {
				return false, nil, err
			}
			if rd == cpu.RegisterPC {
				b.branchExchange(value)
				branched = true
			} else {
				b.regs[rd] = value
			}
		}
	} else {
		value := b.regs[rd]
		if rd == cpu.RegisterPC {
			value = pc + 12
		}
		if byteTransfer {
			if err := b.write8(address, byte(value), cpu.PermissionWrite); err != nil {
				return false, nil, err
			}
		} else if err := b.write32(address, value, cpu.PermissionWrite); err != nil {
			return false, nil, err
		}
	}
	if (!preIndex || writeBack) && !(load && rd == rn) {
		b.regs[rn] = indexedAddress
	}
	return branched, nil, nil
}

func (b *Backend) executeARMMicroHalfwordTransfer(
	instruction, pc uint32,
) (bool, *cpu.StopReason, error) {
	preIndex := instruction&(1<<24) != 0
	up := instruction&(1<<23) != 0
	immediate := instruction&(1<<22) != 0
	writeBack := instruction&(1<<21) != 0
	load := instruction&(1<<20) != 0
	rn := instruction >> 16 & 0xf
	rd := instruction >> 12 & 0xf
	operation := uint8(instruction>>5) & 3
	semanticLoad := load || operation == 2
	offset := instruction>>4&0xf0 | instruction&0xf
	if !immediate {
		offset = b.regs[instruction&0xf]
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
	case !load && operation == 1:
		if err := b.write16(address, uint16(b.regs[rd]), cpu.PermissionWrite); err != nil {
			return false, nil, err
		}
	case operation == 1:
		value, err := b.read16(address, cpu.PermissionRead)
		if err != nil {
			return false, nil, err
		}
		b.regs[rd] = uint32(value)
	case !load && operation == 2:
		low, err := b.read32(address, cpu.PermissionRead)
		if err != nil {
			return false, nil, err
		}
		high, err := b.read32(address+4, cpu.PermissionRead)
		if err != nil {
			return false, nil, err
		}
		b.regs[rd], b.regs[rd+1] = low, high
	case !load && operation == 3:
		if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil {
			return false, nil, err
		}
		if err := b.write32(address+4, b.regs[rd+1], cpu.PermissionWrite); err != nil {
			return false, nil, err
		}
	case operation == 2:
		value, err := b.read8(address, cpu.PermissionRead)
		if err != nil {
			return false, nil, err
		}
		b.regs[rd] = uint32(int32(int8(value)))
	default:
		value, err := b.read16(address, cpu.PermissionRead)
		if err != nil {
			return false, nil, err
		}
		b.regs[rd] = uint32(int32(int16(value)))
	}
	if (!preIndex || writeBack) && !(semanticLoad && rd == rn) {
		b.regs[rn] = indexedAddress
	}
	return false, nil, nil
}

func (b *Backend) executeARMMicroBlockTransfer(
	instruction, pc uint32,
) (bool, *cpu.StopReason, error) {
	preIndex := instruction&(1<<24) != 0
	increment := instruction&(1<<23) != 0
	userOrPSR := instruction&(1<<22) != 0
	writeBack := instruction&(1<<21) != 0
	load := instruction&(1<<20) != 0
	rn := instruction >> 16 & 0xf
	registers := uint16(instruction)
	exceptionReturn := userOrPSR && load && registers&(1<<cpu.RegisterPC) != 0
	transferUser := userOrPSR && !exceptionReturn
	currentMode := b.currentProcessorMode()
	if transferUser && (currentMode == processorModeUser || currentMode == processorModeSystem) {
		return false, nil, b.unsupportedARM(pc, instruction)
	}
	var restoredStatus uint32
	if exceptionReturn {
		status := b.savedStatus(currentMode)
		if status == nil {
			return false, nil, b.unsupportedARM(pc, instruction)
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
	direct, directOffset, directOK := b.armBlockTransferPage(
		address, int(count*4), load,
	)
	for register := uint32(0); register < 16; register++ {
		if registers&(1<<register) == 0 {
			continue
		}
		if load {
			var value uint32
			if directOK {
				value = binary.LittleEndian.Uint32(direct[directOffset : directOffset+4])
				directOffset += 4
			} else {
				var err error
				value, err = b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
			}
			if register == cpu.RegisterPC {
				loadedPC, loadedProgramCounter = value, true
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
			if directOK {
				binary.LittleEndian.PutUint32(direct[directOffset:directOffset+4], value)
				directOffset += 4
			} else if err := b.write32(address, value, cpu.PermissionWrite); err != nil {
				return false, nil, err
			}
		}
		address += 4
	}
	if directOK && !load && !b.instructionCacheEnabled() {
		b.smcInvalidate(address-count*4, count*4, cpu.PermissionExecute)
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
			if err := b.writeProgramStatus(false, 0xf, restoredStatus); err != nil {
				return false, nil, fmt.Errorf("ARM LDM exception return at 0x%08x: %w", pc, err)
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
	return loadedProgramCounter, nil, nil
}
