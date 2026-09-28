package interpreter

import "github.com/mirusu400/aram-core/cpu"

const thumbTransparentPaletteLoopInstructions = uint32(13)

// This Thumb loop copies indexed pixels through a 16-bit palette. Index zero
// leaves the destination untouched and skips four instructions. The compiler
// emits the same loop for forward and reverse destination traversal.
type jitThumbTransparentPaletteLoop struct {
	start   uint32
	reverse bool
}

var thumbTransparentPaletteLoopWords = [thumbTransparentPaletteLoopInstructions]uint16{
	0x4663, 0x5d98, 0x3601, 0x2800, 0xd003, 0x0043, 0x9812,
	0x5a1b, 0x8013, 0x3c01, 0x3202, 0x2c00, 0xd1f2,
}

func (b *Backend) classifyThumbTransparentPaletteLoop(start uint32) *jitThumbTransparentPaletteLoop {
	if uint64(start)+uint64(thumbTransparentPaletteLoopInstructions)*2 >= 1<<32 {
		return nil
	}
	loop := &jitThumbTransparentPaletteLoop{start: start}
	for index, expected := range thumbTransparentPaletteLoopWords {
		word, err := b.fetch16(start + uint32(index)*2)
		if err != nil {
			return nil
		}
		if index == 10 && word == 0x3a02 {
			loop.reverse = true
			continue
		}
		if word != expected {
			return nil
		}
	}
	return loop
}

func (b *Backend) accelerateThumbTransparentPaletteLoop(
	loop *jitThumbTransparentPaletteLoop,
	remaining uint64,
	wholeSystem, hasExecutionTraps, traced bool,
) uint64 {
	if loop == nil || wholeSystem || hasExecutionTraps || traced ||
		b.physicalAccess || b.tlb != nil || remaining < 9 {
		return 0
	}
	var sourceRegion, stackRegion, paletteRegion, destinationRegion *region
	var retired, iterations uint64
	startGeneration := b.jitGen
	for remaining-retired >= 9 && b.regs[cpu.RegisterPC] == loop.start {
		sourceAddress := b.regs[12] + b.regs[6]
		source, sourceOffset, _, ok := b.privateRegionHit(
			&sourceRegion, sourceAddress, 1, cpu.PermissionRead,
		)
		if !ok {
			break
		}
		index := source[sourceOffset]
		pathInstructions := uint64(9)
		paletteBase := uint32(0)
		color := uint16(0)
		var destination []byte
		var destOffset int
		var destPerms cpu.Permissions
		if index != 0 {
			pathInstructions = uint64(thumbTransparentPaletteLoopInstructions)
			if remaining-retired < pathInstructions {
				break
			}
			paletteSlot, slotOffset, _, hit := b.privateRegionHit(
				&stackRegion, b.regs[cpu.RegisterSP]+0x48, 4, cpu.PermissionRead,
			)
			if !hit {
				break
			}
			paletteBase = loadLittle32(paletteSlot, slotOffset)
			colorAddress := paletteBase + uint32(index)*2
			if colorAddress&1 != 0 {
				break
			}
			palette, colorOffset, _, hit := b.privateRegionHit(
				&paletteRegion, colorAddress, 2, cpu.PermissionRead,
			)
			if !hit {
				break
			}
			color = uint16(palette[colorOffset]) | uint16(palette[colorOffset+1])<<8
			destinationAddress := b.regs[2]
			if destinationAddress&1 != 0 ||
				colorLoopRangesOverlap(destinationAddress, 2, loop.start, thumbTransparentPaletteLoopInstructions*2) {
				break
			}
			destination, destOffset, destPerms, hit = b.privateRegionHit(
				&destinationRegion, destinationAddress, 2, cpu.PermissionWrite,
			)
			if !hit {
				break
			}
			destination[destOffset] = byte(color)
			destination[destOffset+1] = byte(color >> 8)
			b.smcInvalidate(destinationAddress, 2, destPerms)
		}

		b.regs[0] = paletteBase
		b.regs[3] = b.regs[12]
		if index != 0 {
			b.regs[3] = uint32(color)
		}
		b.regs[6]++
		b.regs[4]--
		if loop.reverse {
			b.regs[2] -= 2
		} else {
			b.regs[2] += 2
		}
		b.setNZCV(b.regs[4], true, false)
		retired += pathInstructions
		iterations++
		if b.regs[4] == 0 {
			b.regs[cpu.RegisterPC] = loop.start + thumbTransparentPaletteLoopInstructions*2
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
