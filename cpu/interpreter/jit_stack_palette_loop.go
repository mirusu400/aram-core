package interpreter

import "github.com/mirusu400/aram-core/cpu"

const thumbStackPaletteLoopInstructions = uint32(17)

// This Thumb loop expands source bytes through a 16-bit palette. Its palette
// object and destination cursor live in memory, so each iteration must observe
// the loads and stores in their original order, including a second cursor load
// after the pixel store. Match the complete generated instruction sequence.
type jitThumbStackPaletteLoop struct {
	start uint32
}

var thumbStackPaletteLoopWords = [thumbStackPaletteLoopInstructions]uint16{
	0x6f6b, 0x2b00, 0xd117, 0x990e, 0x7823, 0x688a,
	0x005b, 0x5a9b, 0x9a0a, 0x8013, 0x9a0a, 0x3e01,
	0x3202, 0x3401, 0x920a, 0x2e00, 0xd1ee,
}

func (b *Backend) classifyThumbStackPaletteLoop(start uint32) *jitThumbStackPaletteLoop {
	if uint64(start)+uint64(thumbStackPaletteLoopInstructions)*2 >= 1<<32 {
		return nil
	}
	for index, expected := range thumbStackPaletteLoopWords {
		word, err := b.fetch16(start + uint32(index)*2)
		if err != nil || word != expected {
			return nil
		}
	}
	return &jitThumbStackPaletteLoop{start: start}
}

func (b *Backend) accelerateThumbStackPaletteLoop(
	loop *jitThumbStackPaletteLoop,
	remaining uint64,
	wholeSystem, hasExecutionTraps, traced bool,
) uint64 {
	if loop == nil || wholeSystem || hasExecutionTraps || traced ||
		b.physicalAccess || b.tlb != nil ||
		remaining < uint64(thumbStackPaletteLoopInstructions) {
		return 0
	}

	var flagRegion, stackRegion, objectRegion, sourceRegion, tableRegion, destinationRegion *region
	var retired, iterations uint64
	startGeneration := b.jitGen
	for remaining-retired >= uint64(thumbStackPaletteLoopInstructions) &&
		b.regs[cpu.RegisterPC] == loop.start {
		flagAddress := b.regs[5] + 0x74
		flag, flagOffset, _, ok := b.privateRegionHit(&flagRegion, flagAddress, 4, cpu.PermissionRead)
		if !ok || loadLittle32(flag, flagOffset) != 0 {
			break
		}

		stack := b.regs[cpu.RegisterSP]
		objectSlot, objectSlotOffset, _, ok := b.privateRegionHit(
			&stackRegion, stack+0x38, 4, cpu.PermissionRead,
		)
		if !ok {
			break
		}
		objectAddress := loadLittle32(objectSlot, objectSlotOffset)
		source, sourceOffset, _, ok := b.privateRegionHit(
			&sourceRegion, b.regs[4], 1, cpu.PermissionRead,
		)
		if !ok {
			break
		}
		paletteSlot, paletteSlotOffset, _, ok := b.privateRegionHit(
			&objectRegion, objectAddress+8, 4, cpu.PermissionRead,
		)
		if !ok {
			break
		}
		paletteBase := loadLittle32(paletteSlot, paletteSlotOffset)
		index := source[sourceOffset]
		colorAddress := paletteBase + uint32(index)*2
		if colorAddress&1 != 0 {
			break
		}
		table, tableOffset, _, ok := b.privateRegionHit(
			&tableRegion, colorAddress, 2, cpu.PermissionRead,
		)
		if !ok {
			break
		}
		color := uint16(table[tableOffset]) | uint16(table[tableOffset+1])<<8

		cursorSlotAddress := stack + 0x28
		cursorSlot, cursorOffset, cursorPerms, ok := b.privateRegionHit(
			&stackRegion, cursorSlotAddress, 4, cpu.PermissionRead|cpu.PermissionWrite,
		)
		if !ok || colorLoopRangesOverlap(cursorSlotAddress, 4, loop.start, thumbStackPaletteLoopInstructions*2) {
			break
		}
		destinationAddress := loadLittle32(cursorSlot, cursorOffset)
		if destinationAddress&1 != 0 ||
			colorLoopRangesOverlap(destinationAddress, 2, loop.start, thumbStackPaletteLoopInstructions*2) {
			break
		}
		destination, destOffset, destPerms, ok := b.privateRegionHit(
			&destinationRegion, destinationAddress, 2, cpu.PermissionWrite,
		)
		if !ok {
			break
		}

		// STRH precedes the second LDR of the cursor. The pixel may alias
		// its stack slot; reading the slot again preserves that behavior.
		destination[destOffset] = byte(color)
		destination[destOffset+1] = byte(color >> 8)
		b.smcInvalidate(destinationAddress, 2, destPerms)
		nextCursor := loadLittle32(cursorSlot, cursorOffset) + 2
		cursorSlot[cursorOffset] = byte(nextCursor)
		cursorSlot[cursorOffset+1] = byte(nextCursor >> 8)
		cursorSlot[cursorOffset+2] = byte(nextCursor >> 16)
		cursorSlot[cursorOffset+3] = byte(nextCursor >> 24)
		b.smcInvalidate(cursorSlotAddress, 4, cursorPerms)

		b.regs[1] = objectAddress
		b.regs[2] = nextCursor
		b.regs[3] = uint32(color)
		b.regs[4]++
		b.regs[6]--
		b.setNZCV(b.regs[6], true, false)
		retired += uint64(thumbStackPaletteLoopInstructions)
		iterations++
		if b.regs[6] == 0 {
			b.regs[cpu.RegisterPC] = loop.start + thumbStackPaletteLoopInstructions*2
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
