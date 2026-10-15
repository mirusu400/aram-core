package interpreter

import "github.com/mirusu400/aram-core/cpu"

const thumbShiftBoundLoopInstructions = uint64(3)

// Match a complete LSLS value,value,#1 / CMP value,bound / BLS loop.
// No division preconditions are assumed: zero and wrapping values are ordinary
// uint32 states, and even a nonterminating guest loop remains budget bounded.
type jitThumbShiftBoundLoop struct {
	start, value, bound uint32
}

func classifyThumbShiftBoundLoop(block *jitBlock) *jitThumbShiftBoundLoop {
	if len(block.thumb) != 3 || uint64(block.start)+6 >= 1<<32 {
		return nil
	}
	w := block.thumb
	value, source := uint32(w[0].raw)&7, uint32(w[0].raw>>3)&7
	bound := uint32(w[1].raw>>3) & 7
	if value != source || value == bound || w[0].raw != 0x0040|uint16(value)<<3|uint16(value) ||
		w[1].raw != 0x4280|uint16(bound)<<3|uint16(value) ||
		w[2].raw&0xff00 != 0xd900 || thumbConditionalTarget(block.start+4, w[2].raw) != block.start {
		return nil
	}
	return &jitThumbShiftBoundLoop{start: block.start, value: value, bound: bound}
}

func (b *Backend) accelerateThumbShiftBoundLoop(loop *jitThumbShiftBoundLoop, remaining uint64, wholeSystem, hasExecutionTraps, traced bool) uint64 {
	if loop == nil || wholeSystem || hasExecutionTraps || traced || b.physicalAccess || b.tlb != nil || remaining < thumbShiftBoundLoopInstructions || b.regs[cpu.RegisterPC] != loop.start {
		return 0
	}
	value, bound := b.regs[loop.value], b.regs[loop.bound]
	nextPC := loop.start
	var retired uint64
	for remaining-retired >= thumbShiftBoundLoopInstructions {
		value <<= 1
		retired += thumbShiftBoundLoopInstructions
		if value > bound {
			nextPC += uint32(thumbShiftBoundLoopInstructions * 2)
			break
		}
	}
	if retired != 0 {
		b.regs[loop.value], b.regs[cpu.RegisterPC] = value, nextPC
		result, carry, overflow := addWithCarry(value, ^bound, 1)
		b.setNZCV(result, carry, overflow)
		// BLS materializes CMP flags in the precise executor. Preserve that raw
		// CPSR too, before a caller enables register capture without reading it.
		b.resolveFlags()
		b.executionStatistics.AcceleratedLoopIterations += retired / thumbShiftBoundLoopInstructions
		b.executionStatistics.AcceleratedLoopInstructions += retired
	}
	return retired
}
