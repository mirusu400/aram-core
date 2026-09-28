package interpreter

import "github.com/mirusu400/aram-core/cpu"

const thumbHalfwordFillLoopInstructions = uint32(10)

// This generated Thumb loop fills halfwords until its signed counter reaches
// -1. A runtime flag can divert control before the store, so the fast path
// checks that flag on every iteration and leaves the other path to the JIT.
type jitThumbHalfwordFillLoop struct {
	start uint32
}

var thumbHalfwordFillLoopWords = [thumbHalfwordFillLoopInstructions]uint16{
	0x6f7b, 0x2b00, 0xd10a, 0x802e, 0x2301,
	0x3c01, 0x425b, 0x3502, 0x429c, 0xd1f5,
}

func (b *Backend) classifyThumbHalfwordFillLoop(start uint32) *jitThumbHalfwordFillLoop {
	if uint64(start)+uint64(thumbHalfwordFillLoopInstructions)*2 >= 1<<32 {
		return nil
	}
	for index, expected := range thumbHalfwordFillLoopWords {
		word, err := b.fetch16(start + uint32(index)*2)
		if err != nil || word != expected {
			return nil
		}
	}
	return &jitThumbHalfwordFillLoop{start: start}
}

func (b *Backend) accelerateThumbHalfwordFillLoop(
	loop *jitThumbHalfwordFillLoop,
	remaining uint64,
	wholeSystem, hasExecutionTraps, traced bool,
) uint64 {
	if loop == nil || wholeSystem || hasExecutionTraps || traced ||
		b.physicalAccess || b.tlb != nil ||
		remaining < uint64(thumbHalfwordFillLoopInstructions) {
		return 0
	}
	var flagRegion, destinationRegion *region
	var retired, iterations uint64
	startGeneration := b.jitGen
	for remaining-retired >= uint64(thumbHalfwordFillLoopInstructions) &&
		b.regs[cpu.RegisterPC] == loop.start {
		flag, flagOffset, _, ok := b.privateRegionHit(
			&flagRegion, b.regs[7]+0x74, 4, cpu.PermissionRead,
		)
		if !ok || loadLittle32(flag, flagOffset) != 0 {
			break
		}
		address := b.regs[5]
		if address&1 != 0 ||
			colorLoopRangesOverlap(address, 2, loop.start, thumbHalfwordFillLoopInstructions*2) {
			break
		}
		destination, offset, perms, ok := b.privateRegionHit(
			&destinationRegion, address, 2, cpu.PermissionWrite,
		)
		if !ok {
			break
		}
		color := uint16(b.regs[6])
		destination[offset] = byte(color)
		destination[offset+1] = byte(color >> 8)
		b.smcInvalidate(address, 2, perms)
		b.regs[3] = ^uint32(0)
		b.regs[4]--
		b.regs[5] += 2
		result, carry, overflow := addWithCarry(b.regs[4], ^b.regs[3], 1)
		b.setNZCV(result, carry, overflow)
		retired += uint64(thumbHalfwordFillLoopInstructions)
		iterations++
		if b.regs[4] == b.regs[3] {
			b.regs[cpu.RegisterPC] = loop.start + thumbHalfwordFillLoopInstructions*2
			break
		}
		b.regs[cpu.RegisterPC] = loop.start
		if b.jitGen != startGeneration {
			break
		}
	}
	b.executionStatistics.AcceleratedLoopIterations += iterations
	b.executionStatistics.AcceleratedLoopInstructions += retired
	return retired
}
