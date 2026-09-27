package interpreter

import (
	"encoding/binary"

	"github.com/mirusu400/aram-core/cpu"
)

const thumbColorLoopInstructions = uint32(48)

// This compiler-generated RGB565 blend loop expands each pixel into three
// channels, multiplies each by r0, adds channel biases, and packs it back.
// Match the whole instruction sequence: spills and scratch registers are
// guest-visible too, and cannot be replaced by merely similar color math.
type jitThumbColorLoop struct {
	start, maskAddress uint32
}

var thumbColorLoopWords = [thumbColorLoopInstructions]uint16{
	0x8832, 0, 0x4013, 0x0a1b, 0x9303, 0x9b0c, 0x4013, 0x08db,
	0x9302, 0x231f, 0x4013, 0x9903, 0x00db, 0x469c, 0x1c03, 0x434b,
	0x9a09, 0x9c02, 0x18d3, 0x121f, 0x1c03, 0x4363, 0x4662, 0x4342,
	0x9908, 0x18cb, 0x121b, 0, 0x9300, 0x0239, 0x4442, 0x4021,
	0x9c00, 0x1213, 0x9301, 0x00e3, 0x9c0c, 0x4023, 0x4319, 0x12d2,
	0x231f, 0x401a, 0x4311, 0x3d01, 0x8031, 0x3602, 0x2d00, 0xd1cf,
}

func (b *Backend) classifyThumbColorLoop(start uint32) *jitThumbColorLoop {
	var maskAddress uint32
	for index, expected := range thumbColorLoopWords {
		pc := start + uint32(index)*2
		word, err := b.fetch16(pc)
		if err != nil {
			return nil
		}
		if index == 1 || index == 27 {
			register := uint16(3)
			if index == 27 {
				register = 4
			}
			if word&0xff00 != 0x4800|register<<8 {
				return nil
			}
			address := (pc+4)&^uint32(3) + uint32(word&0xff)*4
			if index == 1 {
				maskAddress = address
			} else if maskAddress != address {
				return nil
			}
		} else if word != expected {
			return nil
		}
	}
	return &jitThumbColorLoop{start: start, maskAddress: maskAddress}
}

func (b *Backend) accelerateThumbColorLoop(
	loop *jitThumbColorLoop,
	remaining uint64,
	wholeSystem, hasExecutionTraps, traced bool,
) uint64 {
	if loop == nil || wholeSystem || hasExecutionTraps || traced ||
		b.physicalAccess || b.tlb != nil || remaining < uint64(thumbColorLoopInstructions) {
		return 0
	}
	// Only direct, non-executable RAM may be written. This excludes MMIO and
	// self-modifying code; aliases between the pixel and spill/parameter area
	// fall back to individual instructions, preserving their access ordering.
	var stackRegion, pixelRegion, literalRegion *region
	stackAddress := b.regs[cpu.RegisterSP]
	stack, offset, stackPerms, ok := b.privateRegionHit(
		&stackRegion, stackAddress, 52, cpu.PermissionRead|cpu.PermissionWrite,
	)
	if !ok || stackPerms&cpu.PermissionExecute != 0 ||
		uint64(stackAddress)+52 > 1<<32 {
		return 0
	}
	literal, literalOffset, _, ok := b.privateRegionHit(
		&literalRegion, loop.maskAddress, 4, cpu.PermissionRead,
	)
	if !ok || colorLoopRangesOverlap(stackAddress, 52, loop.maskAddress, 4) {
		return 0
	}
	redMask := loadLittle32(literal, literalOffset)
	greenMask := loadLittle32(stack, offset+48)
	redBias := loadLittle32(stack, offset+36)
	greenBias := loadLittle32(stack, offset+32)
	factor, blueBias := b.regs[0], b.regs[8]
	var retired, iterations uint64
	for remaining-retired >= uint64(thumbColorLoopInstructions) {
		address := b.regs[6]
		if colorLoopRangesOverlap(address, 2, stackAddress, 52) ||
			colorLoopRangesOverlap(address, 2, loop.maskAddress, 4) {
			break
		}
		pixel, pixelOffset, perms, hit := b.privateRegionHit(
			&pixelRegion, address, 2, cpu.PermissionRead|cpu.PermissionWrite,
		)
		if !hit || perms&cpu.PermissionExecute != 0 {
			break
		}
		color := uint32(binary.LittleEndian.Uint16(pixel[pixelOffset:]))
		red := (color & redMask) >> 8
		green := (color & greenMask) >> 3
		blue := (color & 31) << 3
		blendedRed := uint32(int32(factor*red+redBias) >> 8)
		blendedGreen := uint32(int32(factor*green+greenBias) >> 8)
		blueSum := factor*blue + blueBias
		blendedBlue := uint32(int32(blueSum) >> 8)
		blueBits := uint32(int32(blueSum)>>11) & 31
		packed := (blendedRed<<8)&redMask | (blendedGreen<<3)&greenMask | blueBits
		binary.LittleEndian.PutUint32(stack[offset:], blendedGreen)
		binary.LittleEndian.PutUint32(stack[offset+4:], blendedBlue)
		binary.LittleEndian.PutUint32(stack[offset+8:], green)
		binary.LittleEndian.PutUint32(stack[offset+12:], red)
		binary.LittleEndian.PutUint16(pixel[pixelOffset:], uint16(packed))
		b.regs[1], b.regs[2], b.regs[3], b.regs[4] = packed, blueBits, 31, greenMask
		b.regs[5]--
		b.regs[6] += 2
		b.regs[7], b.regs[12] = blendedRed, blue
		b.setNZCV(b.regs[5], true, false) // CMP r5, #0
		retired += uint64(thumbColorLoopInstructions)
		iterations++
		if b.regs[5] == 0 {
			b.regs[cpu.RegisterPC] = loop.start + thumbColorLoopInstructions*2
			break
		}
		b.regs[cpu.RegisterPC] = loop.start
	}
	b.executionStatistics.AcceleratedLoopIterations += iterations
	b.executionStatistics.AcceleratedLoopInstructions += retired
	return retired
}

func colorLoopRangesOverlap(left, leftSize, right, rightSize uint32) bool {
	return uint64(left) < uint64(right)+uint64(rightSize) &&
		uint64(right) < uint64(left)+uint64(leftSize)
}
