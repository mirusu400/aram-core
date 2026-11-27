package interpreter

import "github.com/mirusu400/aram-core/cpu"

// flagQ is the sticky ARMv5TE saturation flag. Only the enhanced DSP
// instructions set it; it is cleared by an explicit MSR.
const flagQ = uint32(1) << 27

// isARMDSP reports the ARMv5TE enhanced DSP instructions: the saturating
// add/subtract family and the signed 16x16 and 32x16 multiplies. They occupy
// the data-processing opcode space 10xx with S clear, so a decoder that does
// not recognise them would execute a flag-setting compare instead.
func isARMDSP(instruction uint32) bool {
	if instruction&0x0f900000 != 0x01000000 {
		return false
	}
	return instruction&0x00000090 == 0x00000080 || // SMLAxy/SMLAWy/SMULWy/SMLALxy/SMULxy
		instruction&0x00000ff0 == 0x00000050 // QADD/QSUB/QDADD/QDSUB
}

// armDSPOperandsValid rejects the UNPREDICTABLE forms that name PC, and a long
// accumulate whose halves share one register.
func armDSPOperandsValid(instruction uint32) bool {
	high := instruction >> 16 & 0xf
	low := instruction >> 12 & 0xf
	rm := instruction & 0xf
	if rm == cpu.RegisterPC || high == cpu.RegisterPC {
		return false
	}
	if instruction&0x00000090 != 0x00000080 {
		return low != cpu.RegisterPC // saturating: Rn=high, Rd=low
	}
	if instruction>>8&0xf == cpu.RegisterPC {
		return false
	}
	switch instruction >> 21 & 3 {
	case 0: // SMLAxy accumulates Rn
		return low != cpu.RegisterPC
	case 1: // SMLAWy accumulates Rn; SMULWy ignores the field
		return instruction&0x20 != 0 || low != cpu.RegisterPC
	case 2: // SMLALxy
		return low != cpu.RegisterPC && low != high
	}
	return true
}

func saturateSigned32(value int64) (uint32, bool) {
	switch {
	case value > 0x7fffffff:
		return 0x7fffffff, true
	case value < -0x80000000:
		return 0x80000000, true
	}
	return uint32(value), false
}

func signedHalf(value uint32, high bool) int64 {
	if high {
		return int64(int16(value >> 16))
	}
	return int64(int16(value))
}

// executeARMDSP runs one instruction accepted by isARMDSP and
// armDSPOperandsValid. None of them changes N, Z, C or V.
func (b *Backend) executeARMDSP(instruction uint32) {
	high := instruction >> 16 & 0xf
	low := instruction >> 12 & 0xf
	rm := instruction & 0xf
	operation := instruction >> 21 & 3
	saturated := false

	if instruction&0x00000090 != 0x00000080 {
		left := int64(int32(b.regs[rm]))
		right := int64(int32(b.regs[high]))
		if operation&2 != 0 { // QDADD / QDSUB double Rn first
			doubled, clipped := saturateSigned32(right * 2)
			right, saturated = int64(int32(doubled)), clipped
		}
		if operation&1 != 0 {
			right = -right
		}
		result, clipped := saturateSigned32(left + right)
		b.regs[low] = result
		if saturated || clipped {
			b.regs[cpu.RegisterCPSR] |= flagQ
		}
		return
	}

	rs := instruction >> 8 & 0xf
	second := signedHalf(b.regs[rs], instruction&0x40 != 0)
	switch operation {
	case 0: // SMLAxy
		product := signedHalf(b.regs[rm], instruction&0x20 != 0) * second
		sum := product + int64(int32(b.regs[low]))
		b.regs[high] = uint32(sum)
		saturated = sum != int64(int32(sum))
	case 1: // SMLAWy / SMULWy
		product := int64(int32(b.regs[rm])) * second >> 16
		if instruction&0x20 != 0 {
			b.regs[high] = uint32(product)
			break
		}
		sum := int64(int32(product)) + int64(int32(b.regs[low]))
		b.regs[high] = uint32(sum)
		saturated = sum != int64(int32(sum))
	case 2: // SMLALxy
		product := signedHalf(b.regs[rm], instruction&0x20 != 0) * second
		sum := uint64(b.regs[high])<<32 | uint64(b.regs[low])
		sum += uint64(product)
		b.regs[high], b.regs[low] = uint32(sum>>32), uint32(sum)
	default: // SMULxy
		b.regs[high] = uint32(signedHalf(b.regs[rm], instruction&0x20 != 0) * second)
	}
	if saturated {
		b.regs[cpu.RegisterCPSR] |= flagQ
	}
}
