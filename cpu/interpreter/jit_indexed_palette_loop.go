package interpreter

import "github.com/mirusu400/aram-core/cpu"

const thumbIndexedPaletteLoopInstructions = uint32(11)

// This Thumb loop uses a fixed high-register source offset to expand bytes
// through a 16-bit palette. Its exact instruction sequence is required because
// the palette pointer, index and cursor remain visible to the guest.
type jitThumbIndexedPaletteLoop struct {
	start uint32
}

var thumbIndexedPaletteLoopWords = [thumbIndexedPaletteLoopInstructions]uint16{
	0x4660, 0x5c2b, 0x980e, 0x005b, 0x5a1b, 0x3901,
	0x8013, 0x3501, 0x3202, 0x2900, 0xd1f4,
}

func (b *Backend) classifyThumbIndexedPaletteLoop(start uint32) *jitThumbIndexedPaletteLoop {
	if uint64(start)+uint64(thumbIndexedPaletteLoopInstructions)*2 >= 1<<32 {
		return nil
	}
	for index, expected := range thumbIndexedPaletteLoopWords {
		word, err := b.fetch16(start + uint32(index)*2)
		if err != nil || word != expected {
			return nil
		}
	}
	return &jitThumbIndexedPaletteLoop{start: start}
}

func (b *Backend) accelerateThumbIndexedPaletteLoop(
	loop *jitThumbIndexedPaletteLoop,
	remaining uint64,
	wholeSystem, hasExecutionTraps, traced bool,
) uint64 {
	if loop == nil || wholeSystem || hasExecutionTraps || traced ||
		b.physicalAccess || b.tlb != nil ||
		remaining < uint64(thumbIndexedPaletteLoopInstructions) {
		return 0
	}
	var sourceRegion, stackRegion, paletteRegion, destinationRegion *region
	var retired, iterations uint64
	startGeneration := b.jitGen
	for remaining-retired >= uint64(thumbIndexedPaletteLoopInstructions) &&
		b.regs[cpu.RegisterPC] == loop.start {
		sourceAddress := b.regs[5] + b.regs[12]
		source, sourceOffset, _, ok := b.privateRegionHit(
			&sourceRegion, sourceAddress, 1, cpu.PermissionRead,
		)
		if !ok {
			break
		}
		paletteSlot, slotOffset, _, ok := b.privateRegionHit(
			&stackRegion, b.regs[cpu.RegisterSP]+0x38, 4, cpu.PermissionRead,
		)
		if !ok {
			break
		}
		paletteBase := loadLittle32(paletteSlot, slotOffset)
		colorAddress := paletteBase + uint32(source[sourceOffset])*2
		if colorAddress&1 != 0 {
			break
		}
		palette, colorOffset, _, ok := b.privateRegionHit(
			&paletteRegion, colorAddress, 2, cpu.PermissionRead,
		)
		if !ok {
			break
		}
		color := uint16(palette[colorOffset]) | uint16(palette[colorOffset+1])<<8
		destinationAddress := b.regs[2]
		if destinationAddress&1 != 0 ||
			colorLoopRangesOverlap(destinationAddress, 2, loop.start, thumbIndexedPaletteLoopInstructions*2) {
			break
		}
		destination, destOffset, perms, ok := b.privateRegionHit(
			&destinationRegion, destinationAddress, 2, cpu.PermissionWrite,
		)
		if !ok {
			break
		}
		destination[destOffset] = byte(color)
		destination[destOffset+1] = byte(color >> 8)
		b.smcInvalidate(destinationAddress, 2, perms)
		b.regs[0] = paletteBase
		b.regs[1]--
		b.regs[2] += 2
		b.regs[3] = uint32(color)
		b.regs[5]++
		b.setNZCV(b.regs[1], true, false)
		retired += uint64(thumbIndexedPaletteLoopInstructions)
		iterations++
		if b.regs[1] == 0 {
			b.regs[cpu.RegisterPC] = loop.start + thumbIndexedPaletteLoopInstructions*2
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
