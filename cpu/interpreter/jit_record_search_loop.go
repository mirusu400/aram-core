package interpreter

import "github.com/mirusu400/aram-core/cpu"

// This generated Thumb loop searches twelve-byte records. The two loads and
// both signed comparisons must keep their original order. Only iterations
// taking the back edge are accelerated; exits and faults run in the interpreter.
const thumbRecordSearchInstructions = uint32(9)

type jitThumbRecordSearch struct{ start uint32 }

var thumbRecordSearchWords = [thumbRecordSearchInstructions]uint16{
	0x6813, 0x1a5b, 0x42a3, 0xda06, 0x3001,
	0x6851, 0x320c, 0x4560, 0xdbf6,
}

func (b *Backend) classifyThumbRecordSearch(start uint32) *jitThumbRecordSearch {
	if uint64(start)+uint64(thumbRecordSearchInstructions)*2 >= 1<<32 {
		return nil
	}
	for index, expected := range thumbRecordSearchWords {
		word, err := b.fetch16(start + uint32(index)*2)
		if err != nil || word != expected {
			return nil
		}
	}
	return &jitThumbRecordSearch{start: start}
}

func (b *Backend) accelerateThumbRecordSearch(
	loop *jitThumbRecordSearch, remaining uint64,
	wholeSystem, hasExecutionTraps, traced bool,
) uint64 {
	if loop == nil || wholeSystem || hasExecutionTraps || traced ||
		b.physicalAccess || b.tlb != nil || remaining < uint64(thumbRecordSearchInstructions) {
		return 0
	}
	var recordRegion *region
	var retired, iterations uint64
	startGeneration := b.jitGen
	for remaining-retired >= uint64(thumbRecordSearchInstructions) &&
		b.regs[cpu.RegisterPC] == loop.start {
		address := b.regs[2]
		if address&3 != 0 {
			break
		}
		first, firstOffset, _, ok := b.privateRegionHit(&recordRegion, address, 4, cpu.PermissionRead)
		if !ok {
			break
		}
		value := loadLittle32(first, firstOffset) - b.regs[1]
		if int32(value) >= int32(b.regs[4]) {
			break
		}
		newCount := b.regs[0] + 1
		second, secondOffset, _, ok := b.privateRegionHit(&recordRegion, address+4, 4, cpu.PermissionRead)
		if !ok || int32(newCount) >= int32(b.regs[12]) {
			break
		}
		b.regs[0] = newCount
		b.regs[1] = loadLittle32(second, secondOffset)
		b.regs[2] = address + 12
		b.regs[3] = value
		result, carry, overflow := addWithCarry(newCount, ^b.regs[12], 1)
		b.setNZCV(result, carry, overflow)
		retired += uint64(thumbRecordSearchInstructions)
		iterations++
		if b.jitGen != startGeneration {
			break
		}
	}
	b.executionStatistics.AcceleratedLoopIterations += iterations
	b.executionStatistics.AcceleratedLoopInstructions += retired
	return retired
}
