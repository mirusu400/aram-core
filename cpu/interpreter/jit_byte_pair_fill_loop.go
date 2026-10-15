package interpreter

import "github.com/mirusu400/aram-core/cpu"

const thumbBytePairFillLoopInstructions = uint64(6)

// Two byte stores deliberately remain byte stores. The destination may be odd,
// and a fault at the second byte must retain the first store and first ADDS.
type jitThumbBytePairFillLoop struct {
	start, destination, low, high, bound uint32
}

func classifyThumbBytePairFillLoop(block *jitBlock) *jitThumbBytePairFillLoop {
	if len(block.thumb) != 6 || uint64(block.start)+12 >= 1<<32 {
		return nil
	}
	w := block.thumb
	destination, low := uint32(w[0].raw>>3)&7, uint32(w[0].raw)&7
	high, bound := uint32(w[2].raw)&7, uint32(w[4].raw>>3)&7
	d := uint16(destination)
	if destination == low || destination == high || destination == bound ||
		w[0].raw != 0x7000|d<<3|uint16(low) || w[1].raw != 0x3001|d<<8 ||
		w[2].raw != 0x7000|d<<3|uint16(high) || w[3].raw != 0x3001|d<<8 ||
		w[4].raw != 0x4280|uint16(bound)<<3|d || w[5].raw&0xff00 != 0xd900 ||
		thumbConditionalTarget(block.start+10, w[5].raw) != block.start {
		return nil
	}
	return &jitThumbBytePairFillLoop{start: block.start, destination: destination, low: low, high: high, bound: bound}
}

func (b *Backend) bytePairFillCodeAlias(loop *jitThumbBytePairFillLoop) bool {
	address := b.regs[loop.destination]
	span := uint32(thumbBytePairFillLoopInstructions * 2)
	if address == ^uint32(0) {
		// The second STRB wraps to zero. The ordinary range-overlap helper does
		// not split wrapped intervals, so test both architectural byte addresses.
		return colorLoopRangesOverlap(address, 1, loop.start, span) || colorLoopRangesOverlap(0, 1, loop.start, span)
	}
	return colorLoopRangesOverlap(address, 2, loop.start, span)
}

func (b *Backend) accelerateThumbBytePairFillLoop(loop *jitThumbBytePairFillLoop, remaining uint64, wholeSystem, hasExecutionTraps, traced bool) uint64 {
	if loop == nil || wholeSystem || hasExecutionTraps || traced || b.physicalAccess || b.tlb != nil || remaining < thumbBytePairFillLoopInstructions || b.regs[cpu.RegisterPC] != loop.start {
		return 0
	}
	var destinationRegion *region
	var retired uint64
	address, bound := b.regs[loop.destination], b.regs[loop.bound]
	low, high := byte(b.regs[loop.low]), byte(b.regs[loop.high])
	nextPC := loop.start
	for remaining-retired >= thumbBytePairFillLoopInstructions {
		// Prove the whole iteration before its first side effect. A wrapping
		// pointer update, split region or second-byte fault uses the precise path.
		if uint64(address)+2 > uint64(^uint32(0)) {
			break
		}
		data, offset, perms, ok := b.privateRegionHit(&destinationRegion, address, 2, cpu.PermissionWrite)
		if !ok {
			break
		}
		// Executable data is common in WIPI images. Permit it only when neither
		// store can invalidate translated code; code-page hits decline without
		// stores, flag updates or retirement. No speculative bulk write is used.
		if perms&cpu.PermissionExecute != 0 &&
			(b.hasJITCodePages(address, 2) ||
				(b.nativeBlocks != nil || b.nativeARMBlocks != nil) && b.hasCodePages(address, 2)) {
			break
		}
		data[offset] = low
		data[offset+1] = high
		address += 2
		retired += thumbBytePairFillLoopInstructions
		if address > bound {
			nextPC += uint32(thumbBytePairFillLoopInstructions * 2)
			break
		}
	}
	if retired != 0 {
		b.regs[loop.destination], b.regs[cpu.RegisterPC] = address, nextPC
		result, carry, overflow := addWithCarry(address, ^bound, 1)
		b.setNZCV(result, carry, overflow)
		b.resolveFlags()
		b.executionStatistics.AcceleratedLoopIterations += retired / thumbBytePairFillLoopInstructions
		b.executionStatistics.AcceleratedLoopInstructions += retired
	}
	return retired
}
