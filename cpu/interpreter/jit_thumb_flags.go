package interpreter

// Keep specialized opcodes near the ordinary ones. A high marker bit makes
// the dispatch sparse enough to disable Go's native switch jump table.
const thumbDeadFlagsOffset thumbInstructionClass = thumbUnconditionalBranch + 1

// markDeadThumbFlags finds flag writes overwritten by later pure ALU
// instructions. Faulting or control-flow instructions are observation barriers:
// flags at every possible exit still describe the last retired instruction.
// The application executor uses these marks only for a complete block; a Run
// budget cutting through the block executes every flag write as usual.
func markDeadThumbFlags(instructions []thumbMicroInstr) {
	const all = flagN | flagZ | flagC | flagV
	live := uint32(all)
	for index := len(instructions) - 1; index >= 0; index-- {
		in := &instructions[index]
		in.appOp = in.op
		var read, write uint32
		switch in.op {
		case thumbMoveImmediate:
			write = flagN | flagZ
		case thumbCompareImmediate, thumbAddImmediate, thumbSubtractImmediate, thumbAddSubtract:
			write = all
		case thumbShiftImmediate:
			write = flagN | flagZ | flagC
			if in.raw&0x1fc0 == 0 { // LSL #0 preserves carry.
				read = flagC
			}
		case thumbALU:
			switch in.raw >> 6 & 15 {
			case 2, 3, 4, 7: // Register shifts can preserve carry at amount zero.
				read, write = flagC, flagN|flagZ|flagC
			case 5, 6: // ADC/SBC consume the old carry even if their flags die.
				read, write = flagC, all
			case 9, 10, 11: // NEG, CMP, CMN
				write = all
			default:
				write = flagN | flagZ
			}
		case thumbAdjustStack, thumbAddPCSP:
			// These are non-faulting and neither read nor write condition flags.
		case thumbHighRegister:
			opcode := in.raw >> 8 & 3
			destination := uint32(in.raw)&7 | uint32(in.raw>>7)&1<<3
			if opcode == 3 || opcode != 1 && destination == 15 {
				live = all
				continue
			}
			if opcode == 1 {
				write = all
			}
		default:
			live = all
			continue
		}
		if write != 0 && live&write == 0 {
			in.appOp += thumbDeadFlagsOffset
		}
		live = live&^write | read
	}
}

// addWithLiveFlags retains the exact arithmetic result while avoiding carry
// and overflow computation when the complete translated block overwrites them.
func addWithLiveFlags(left, right, carry uint32, flagsLive bool) (uint32, bool, bool) {
	if !flagsLive {
		return left + right + carry, false, false
	}
	return addWithCarry(left, right, carry)
}
