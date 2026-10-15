package interpreter

import "github.com/mirusu400/aram-core/cpu"

const thumbQuotientRefineWords = uint32(10)

// This is an arithmetic CFG, not a replacement for an entire division routine.
// Arbitrary uint32 inputs, including zero and unreachable original divisors,
// retain their literal instruction semantics and may exhaust the guest budget.
type jitThumbQuotientRefineLoop struct {
	start, quotient, divisor, remainder, original uint32
}

func (b *Backend) classifyThumbQuotientRefineLoop(block *jitBlock) *jitThumbQuotientRefineLoop {
	// The extra forward fetches must not observe a bus or functional I-cache.
	if b.systemBus != nil || b.physicalAccess || b.tlb != nil || len(block.thumb) != 3 ||
		uint64(block.start)+uint64(thumbQuotientRefineWords)*2 >= 1<<32 {
		return nil
	}
	w := block.thumb
	quotient, divisor, original := uint32(w[0].raw)&7, uint32(w[1].raw)&7, uint32(w[1].raw>>3)&7
	q, d, o := uint16(quotient), uint16(divisor), uint16(original)
	if quotient == divisor || quotient == original || divisor == original ||
		w[0].raw != 0x1800|q<<6|q<<3|q || w[1].raw != 0x4280|o<<3|d ||
		w[2].raw&0xff00 != 0xd000 || thumbConditionalTarget(block.start+4, w[2].raw) != block.start+20 {
		return nil
	}
	var tail [7]uint16
	for index := range tail {
		word, err := b.fetch16(block.start + 6 + uint32(index)*2)
		if err != nil {
			return nil
		}
		tail[index] = word
	}
	remainder := uint32(tail[1]) & 7
	r := uint16(remainder)
	if remainder == quotient || remainder == divisor || remainder == original ||
		tail[0] != 0x0840|d<<3|d || tail[1] != 0x4280|d<<3|r ||
		tail[2]&0xff00 != 0xd300 || thumbConditionalTarget(block.start+10, tail[2]) != block.start ||
		tail[3] != 0x4140|q<<3|q || tail[4] != 0x1a00|d<<6|r<<3|r ||
		tail[5] != w[1].raw || tail[6]&0xff00 != 0xd100 || thumbConditionalTarget(block.start+18, tail[6]) != block.start+6 {
		return nil
	}
	return &jitThumbQuotientRefineLoop{start: block.start, quotient: quotient, divisor: divisor, remainder: remainder, original: original}
}

func (b *Backend) accelerateThumbQuotientRefineLoop(loop *jitThumbQuotientRefineLoop, remaining uint64, wholeSystem, hasExecutionTraps, traced bool) uint64 {
	if loop == nil || wholeSystem || hasExecutionTraps || traced || b.physicalAccess || b.tlb != nil || remaining < 3 || b.regs[cpu.RegisterPC] != loop.start {
		return 0
	}
	quotient, divisor := b.regs[loop.quotient], b.regs[loop.divisor]
	remainder, original := b.regs[loop.remainder], b.regs[loop.original]
	quotient <<= 1
	retired, units := uint64(3), uint64(1)
	nextPC := loop.start + 6
	if divisor == original {
		nextPC = loop.start + thumbQuotientRefineWords*2
	} else {
		for {
			shifted := divisor >> 1
			cost := uint64(6)
			if remainder >= shifted {
				cost = 7
			}
			// A short budget leaves even the speculative LSRS uncommitted.
			// The ordinary runner executes the incomplete path from its head.
			if remaining-retired < cost {
				break
			}
			divisor = shifted
			quotient <<= 1
			if cost == 7 {
				// CMP remainder,divisor, followed by BCC not taken, guarantees
				// ADC's carry-in is one, independent of the caller's old CPSR.
				quotient++
				remainder -= divisor
			}
			retired += cost
			units++
			if divisor == original {
				nextPC = loop.start + thumbQuotientRefineWords*2
				break
			}
		}
	}
	b.regs[loop.quotient], b.regs[loop.divisor], b.regs[loop.remainder] = quotient, divisor, remainder
	b.regs[cpu.RegisterPC] = nextPC
	result, carry, overflow := addWithCarry(divisor, ^original, 1)
	b.setNZCV(result, carry, overflow)
	// Every committed unit ends with CMP/BEQ or CMP/BNE, including the initial
	// three-instruction prefix. Preserve the precise branch's stored CPSR too.
	b.resolveFlags()
	// Count the prefix and each complete 6/7-instruction path as accelerated
	// units; virtual instruction retirement includes the prefix exactly once.
	b.executionStatistics.AcceleratedLoopIterations += units
	b.executionStatistics.AcceleratedLoopInstructions += retired
	return retired
}
