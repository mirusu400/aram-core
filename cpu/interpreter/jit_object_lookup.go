package interpreter

import (
	"encoding/binary"

	"github.com/mirusu400/aram-core/cpu"
)

const thumbObjectLookupInstructions = uint32(9)

// This generated Thumb helper unwraps a direct object reference and returns.
// Tagged references take a separate branch, which stays on the precise path.
// The fast path preserves its PUSH/POP memory writes, return and final flags.
type jitThumbObjectLookup struct {
	start uint32
}

var thumbObjectLookupWords = [thumbObjectLookupInstructions]uint16{
	0xb510, 0x1c04, 0x6800, 0x2301, 0x4003,
	0x2b00, 0xd101, 0x68c0, 0xbd10,
}

func (b *Backend) classifyThumbObjectLookup(start uint32) *jitThumbObjectLookup {
	if uint64(start)+uint64(thumbObjectLookupInstructions)*2 >= 1<<32 {
		return nil
	}
	for index, expected := range thumbObjectLookupWords {
		word, err := b.fetch16(start + uint32(index)*2)
		if err != nil || word != expected {
			return nil
		}
	}
	return &jitThumbObjectLookup{start: start}
}

func (b *Backend) accelerateThumbObjectLookup(
	lookup *jitThumbObjectLookup,
	remaining uint64,
	wholeSystem, hasExecutionTraps, traced bool,
) uint64 {
	if lookup == nil || wholeSystem || hasExecutionTraps || traced ||
		b.physicalAccess || b.tlb != nil || remaining < uint64(thumbObjectLookupInstructions) {
		return 0
	}
	stackTop := b.regs[cpu.RegisterSP]
	objectAddress := b.regs[0]
	returnAddress := b.regs[cpu.RegisterLR]
	if stackTop < 8 || stackTop&3 != 0 || objectAddress&3 != 0 || returnAddress&1 == 0 {
		return 0
	}
	stackAddress := stackTop - 8
	if colorLoopRangesOverlap(stackAddress, 8, lookup.start, thumbObjectLookupInstructions*2) ||
		colorLoopRangesOverlap(stackAddress, 8, objectAddress, 4) {
		return 0
	}
	var stackRegion, objectRegion, valueRegion *region
	stack, stackOffset, stackPerms, ok := b.privateRegionHit(
		&stackRegion, stackAddress, 8, cpu.PermissionRead|cpu.PermissionWrite,
	)
	if !ok {
		return 0
	}
	object, objectOffset, _, ok := b.privateRegionHit(
		&objectRegion, objectAddress, 4, cpu.PermissionRead,
	)
	if !ok {
		return 0
	}
	pointer := loadLittle32(object, objectOffset)
	if pointer&3 != 0 ||
		colorLoopRangesOverlap(stackAddress, 8, pointer+12, 4) {
		return 0
	}
	value, valueOffset, _, ok := b.privateRegionHit(
		&valueRegion, pointer+12, 4, cpu.PermissionRead,
	)
	if !ok {
		return 0
	}
	result := loadLittle32(value, valueOffset)
	binary.LittleEndian.PutUint32(stack[stackOffset:], b.regs[4])
	binary.LittleEndian.PutUint32(stack[stackOffset+4:], returnAddress)
	b.smcInvalidate(stackAddress, 8, stackPerms)
	b.regs[0] = result
	b.regs[3] = 0
	b.setNZCV(0, true, false)
	b.branchExchange(returnAddress)
	return uint64(thumbObjectLookupInstructions)
}
