package brewrt

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/mirusu400/aram-core/cpu"
)

// AEEStdLib f_op and f_cmp use the ARM soft-float calling convention: the two
// doubles occupy r0-r3 and the operation selector is the next stack word.
func (r *Runtime) returnGuestFloatHelper(slot uint32) error {
	var words [4]uint32
	for index := range words {
		word, err := r.cpu.ReadRegister(cpu.RegisterR0 + uint32(index))
		if err != nil {
			return fmt.Errorf("read BREW floating argument %d: %w", index, err)
		}
		words[index] = word
	}
	first := math.Float64frombits(uint64(words[1])<<32 | uint64(words[0]))
	second := math.Float64frombits(uint64(words[3])<<32 | uint64(words[2]))
	sp, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return fmt.Errorf("read BREW floating argument stack: %w", err)
	}
	var encoded [4]byte
	if err := r.cpu.ReadMemory(sp, encoded[:]); err != nil {
		return fmt.Errorf("read BREW floating operation: %w", err)
	}
	operation := binary.LittleEndian.Uint32(encoded[:])
	if slot == helperFloatOpSlot {
		var result float64
		switch operation {
		case 0: // FO_ADD
			result = first + second
		case 1: // FO_SUB
			result = first - second
		case 2: // FO_MUL
			result = first * second
		case 3: // FO_DIV
			result = first / second
		case 9: // FO_POW
			result = math.Pow(first, second)
		default:
			return fmt.Errorf("BREW execution boundary: unsupported f_op selector %d", operation)
		}
		bits := math.Float64bits(result)
		if err := r.cpu.WriteRegister(cpu.RegisterR0, uint32(bits)); err != nil {
			return fmt.Errorf("return BREW floating result low word: %w", err)
		}
		if err := r.cpu.WriteRegister(cpu.RegisterR1, uint32(bits>>32)); err != nil {
			return fmt.Errorf("return BREW floating result high word: %w", err)
		}
		return nil
	}
	var result bool
	switch operation {
	case 4: // FO_CMP_L
		result = first < second
	case 5: // FO_CMP_LE
		result = first <= second
	case 6: // FO_CMP_E
		result = first == second
	case 7: // FO_CMP_G
		result = first > second
	case 8: // FO_CMP_GE
		result = first >= second
	default:
		return fmt.Errorf("BREW execution boundary: unsupported f_cmp selector %d", operation)
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, boolWord(result)); err != nil {
		return fmt.Errorf("return BREW floating comparison: %w", err)
	}
	return nil
}
