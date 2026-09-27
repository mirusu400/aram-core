package interpreter

import (
	"encoding/binary"

	"github.com/mirusu400/aram-core/cpu"
)

const thumbFillLoopInstructions = uint32(16)

// This generated RGB565 fill loop clips a signed coordinate, conditionally
// writes a stack-owned color, advances its index/pointer, then compares the
// next index. Match every instruction and branch, allowing only stack offsets
// to vary. Both clipped paths have different guest instruction counts.
type jitThumbFillLoop struct {
	start                               uint32
	lowerSP, upperSP, strideSP, colorSP uint32
	stackSize                           uint32
	tailOnly                            bool
}

var thumbFillLoopWords = [thumbFillLoopInstructions]uint16{
	0x4640, 0x1843, 0x9808, 0x4283, 0xdb05, 0x9807, 0x4283, 0xda02,
	0x466b, 0x891b, 0x8013, 0x9801, 0x19c9, 0x1812, 0x42b1, 0xdbef,
}

func (b *Backend) classifyThumbFillLoop(start uint32) *jitThumbFillLoop {
	return b.classifyThumbFillLoopAt(start, 0)
}

// A row's first pixel often enters after the MOV r0,r8. Recognize that suffix
// separately, use the actual r0 value, and retire exactly one iteration before
// redispatching its back branch. No assumption is made about the skipped MOV.
func (b *Backend) classifyThumbFillLoopTail(start uint32) *jitThumbFillLoop {
	if start < 2 {
		return nil
	}
	return b.classifyThumbFillLoopAt(start-2, 1)
}

func (b *Backend) classifyThumbFillLoopAt(start uint32, first int) *jitThumbFillLoop {
	// Keep the extended decoded span representable for code invalidation.
	if uint64(start)+uint64(thumbFillLoopInstructions)*2 >= 1<<32 {
		return nil
	}
	loop := &jitThumbFillLoop{start: start, tailOnly: first != 0}
	for index := first; index < len(thumbFillLoopWords); index++ {
		expected := thumbFillLoopWords[index]
		word, err := b.fetch16(start + uint32(index)*2)
		if err != nil {
			return nil
		}
		switch index {
		case 2, 5, 11:
			if word&0xff00 != 0x9800 {
				return nil
			}
			offset := uint32(word&0xff) * 4
			if index == 2 {
				loop.lowerSP = offset
			} else if index == 5 {
				loop.upperSP = offset
			} else {
				loop.strideSP = offset
			}
			if offset+4 > loop.stackSize {
				loop.stackSize = offset + 4
			}
		case 9:
			if word&0xf83f != 0x881b {
				return nil
			}
			loop.colorSP = uint32(word>>6&31) * 2
			if loop.colorSP+2 > loop.stackSize {
				loop.stackSize = loop.colorSP + 2
			}
		default:
			if word != expected {
				return nil
			}
		}
	}
	return loop
}

func (b *Backend) accelerateThumbFillLoop(
	loop *jitThumbFillLoop,
	remaining uint64,
	wholeSystem, hasExecutionTraps, traced bool,
) uint64 {
	if loop == nil || wholeSystem || hasExecutionTraps || traced || b.physicalAccess || b.tlb != nil {
		return 0
	}
	maxInstructions := uint64(thumbFillLoopInstructions)
	if loop.tailOnly {
		maxInstructions--
	}
	if remaining < maxInstructions {
		return 0
	}
	stackAddress := b.regs[cpu.RegisterSP]
	if uint64(stackAddress)+uint64(loop.stackSize) > 1<<32 {
		return 0
	}
	var stackRegion, pixelRegion *region
	stack, offset, _, ok := b.privateRegionHit(&stackRegion, stackAddress, int(loop.stackSize), cpu.PermissionRead)
	if !ok {
		return 0
	}
	lower := loadLittle32(stack, offset+int(loop.lowerSP))
	upper := loadLittle32(stack, offset+int(loop.upperSP))
	stride := loadLittle32(stack, offset+int(loop.strideSP))
	color := binary.LittleEndian.Uint16(stack[offset+int(loop.colorSP):])
	var retired, iterations uint64
	for remaining-retired >= maxInstructions {
		origin := b.regs[8]
		if loop.tailOnly {
			origin = b.regs[0]
		}
		coordinate := origin + b.regs[1]
		pathInstructions := uint64(16)
		inside := true
		if int32(coordinate) < int32(lower) {
			inside = false
			pathInstructions = 10
		} else if int32(coordinate) >= int32(upper) {
			inside = false
			pathInstructions = 13
		}
		if loop.tailOnly {
			pathInstructions--
		}
		scratch := coordinate
		if inside {
			address := b.regs[2]
			// Hoisted stack loads must not be changed by a pixel store. Execute
			// permissions exclude self-modifying guest code; bus/TLB/traced
			// accesses stay on the precise instruction path above.
			if colorLoopRangesOverlap(address, 2, stackAddress, loop.stackSize) {
				break
			}
			pixel, pixelOffset, perms, hit := b.privateRegionHit(&pixelRegion, address, 2, cpu.PermissionWrite)
			if !hit || perms&cpu.PermissionExecute != 0 {
				break
			}
			binary.LittleEndian.PutUint16(pixel[pixelOffset:], color)
			scratch = uint32(color)
		}
		b.regs[0] = stride
		b.regs[1] += b.regs[7]
		b.regs[2] += stride
		b.regs[3] = scratch
		result, carry, overflow := addWithCarry(b.regs[1], ^b.regs[6], 1)
		b.setNZCV(result, carry, overflow)
		retired += pathInstructions
		iterations++
		if int32(b.regs[1]) >= int32(b.regs[6]) {
			b.regs[cpu.RegisterPC] = loop.start + thumbFillLoopInstructions*2
			break
		}
		b.regs[cpu.RegisterPC] = loop.start
		if loop.tailOnly {
			break
		}
	}
	b.executionStatistics.AcceleratedLoopIterations += iterations
	b.executionStatistics.AcceleratedLoopInstructions += retired
	return retired
}
