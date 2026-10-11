package interpreter

import (
	"encoding/binary"

	"github.com/mirusu400/aram-core/cpu"
)

// Packed indices use a cursor whose low bit chooses the nibble and whose
// remaining bits give the byte address. The keyed form keeps temporaries in
// registers; the opaque form spills the next cursor/count/destination to stack.
type jitThumbNibblePaletteLoop struct {
	start, words                                                       uint32
	cursor, destination, counter, index, scratch, temporary, nextCount uint32
	paletteSP, keySP, cursorSP, destinationSP, countSP                 uint32
	keyed, reverse                                                     bool
}

func thumbShortBranchTo(start uint32, position uint32, word uint16, target uint32) bool {
	return word&0xf800 == 0xe000 && uint32(int64(start+position*2)+4+(int64(int16(word<<5))>>4)) == target
}

func (b *Backend) classifyThumbNibblePaletteLoop(start uint32) *jitThumbNibblePaletteLoop {
	if uint64(start)+54 >= 1<<32 {
		return nil
	}
	first, err := b.fetch16(start)
	if err != nil {
		return nil
	}
	l := &jitThumbNibblePaletteLoop{start: start}
	switch first & 0xffc0 {
	case 0x0840:
		l.keyed, l.words = true, 24
	case 0x1c80:
		l.words = 27
	default:
		return nil
	}
	var w [27]uint16
	for i := uint32(0); i < l.words; i++ {
		word, err := b.fetch16(start + i*2)
		if err != nil {
			return nil
		}
		w[i] = word
	}
	if l.keyed {
		l.cursor, l.scratch = uint32(w[0]>>3)&7, uint32(w[0])&7
		l.temporary, l.index = uint32(w[1])&7, uint32(w[2])&7
		l.counter, l.nextCount = uint32(w[12]>>3)&7, uint32(w[12])&7
		l.destination = uint32(w[13]>>3) & 7
		l.keySP, l.paletteSP = uint32(w[10]&255)*4, uint32(w[16]&255)*4
		c, d, n, x, s, tmp, next := uint16(l.cursor), uint16(l.destination), uint16(l.counter), uint16(l.index), uint16(l.scratch), uint16(l.temporary), uint16(l.nextCount)
		step := uint16(0x1c40 | c<<3 | tmp)
		if w[1] == 0x1e40|c<<3|tmp {
			l.reverse = true
			step = w[1]
		}
		if w[0] != 0x0840|c<<3|s || w[1] != step || w[2] != 0x07c0|c<<3|x ||
			w[3]&0xff00 != 0xd500 || thumbConditionalTarget(start+6, w[3]) != start+14 ||
			w[4] != 0x7800|s<<3|s || w[5] != 0x0900|s<<3|x || !thumbShortBranchTo(start, 6, w[6], start+20) ||
			w[7] != 0x7800|s<<3|x || w[8] != 0x200f|s<<8 || w[9] != 0x4000|s<<3|x ||
			w[10]&0xff00 != 0x9800|s<<8 || w[11] != 0x1c00|tmp<<3|c ||
			w[12] != 0x1e40|n<<3|next || w[13] != 0x1c80|d<<3|tmp || w[14] != 0x4280|s<<3|x ||
			w[15]&0xff00 != 0xd000 || thumbConditionalTarget(start+30, w[15]) != start+40 ||
			w[16]&0xff00 != 0x9800|n<<8 || w[17] != 0x0040|x<<3|s ||
			w[18] != 0x5a00|n<<6|s<<3|s || w[19] != 0x8000|d<<3|s ||
			w[20] != 0x1c00|tmp<<3|d || w[21] != 0x1c00|next<<3|n || w[22] != 0x2800|next<<8 {
			return nil
		}
	} else {
		l.destination, l.scratch = uint32(w[0]>>3)&7, uint32(w[0])&7
		l.counter, l.cursor, l.index = uint32(w[2]>>8)&7, uint32(w[3]>>3)&7, uint32(w[4])&7
		l.destinationSP, l.cursorSP, l.countSP, l.paletteSP = uint32(w[1]&255)*4, uint32(w[5]&255)*4, uint32(w[6]&255)*4, uint32(w[10]&255)*4
		c, d, n, x, s := uint16(l.cursor), uint16(l.destination), uint16(l.counter), uint16(l.index), uint16(l.scratch)
		step := uint16(0x1c40 | c<<3 | s)
		if w[3] == 0x1e40|c<<3|s {
			l.reverse = true
			step = w[3]
		}
		if w[0] != 0x1c80|d<<3|s || w[1]&0xff00 != 0x9000|s<<8 || w[2] != 0x3801|n<<8 ||
			w[3] != step || w[4] != 0x0840|c<<3|x || w[5]&0xff00 != 0x9000|s<<8 ||
			w[6]&0xff00 != 0x9000|n<<8 || w[7] != 0x07c0|c<<3|n ||
			w[8]&0xff00 != 0xd500 || thumbConditionalTarget(start+16, w[8]) != start+30 ||
			w[9] != 0x7800|x<<3|x || w[10]&0xff00 != 0x9800|s<<8 || w[11] != 0x0900|x<<3|x ||
			w[12] != 0x0040|x<<3|x || w[13] != 0x5a00|s<<6|x<<3|x || !thumbShortBranchTo(start, 14, w[14], start+42) ||
			w[15] != 0x7800|x<<3|x || w[16] != 0x200f|s<<8 || w[17] != 0x4000|s<<3|x ||
			w[18] != 0x9800|n<<8|uint16(l.paletteSP/4) || w[19] != 0x0040|x<<3|x || w[20] != 0x5a00|n<<6|x<<3|x ||
			w[21] != 0x9800|n<<8|uint16(l.countSP/4) || w[22] != 0x8000|d<<3|x ||
			w[23] != 0x9800|c<<8|uint16(l.cursorSP/4) || w[24] != 0x9800|d<<8|uint16(l.destinationSP/4) ||
			w[25] != 0x2800|n<<8 {
			return nil
		}
		slots := []uint32{l.destinationSP, l.cursorSP, l.countSP, l.paletteSP}
		for i, s := range slots {
			for _, other := range slots[i+1:] {
				if s == other {
					return nil
				}
			}
		}
	}
	last := l.words - 1
	if w[last]&0xff00 != 0xdc00 || thumbConditionalTarget(start+last*2, w[last]) != start {
		return nil
	}
	registers := []uint32{l.cursor, l.destination, l.counter, l.index, l.scratch}
	if l.keyed {
		registers = append(registers, l.temporary, l.nextCount)
	}
	for i, r := range registers {
		for _, other := range registers[i+1:] {
			if r == other {
				return nil
			}
		}
	}
	return l
}

func (b *Backend) nibblePaletteCodeAlias(l *jitThumbNibblePaletteLoop) bool {
	if colorLoopRangesOverlap(b.regs[l.destination], 2, l.start, l.words*2) {
		return true
	}
	if !l.keyed {
		for _, slot := range [...]uint32{l.destinationSP, l.cursorSP, l.countSP} {
			if colorLoopRangesOverlap(b.regs[cpu.RegisterSP]+slot, 4, l.start, l.words*2) {
				return true
			}
		}
	}
	return false
}

func (b *Backend) accelerateThumbNibblePaletteLoop(l *jitThumbNibblePaletteLoop, remaining uint64, wholeSystem, hasExecutionTraps, traced bool) uint64 {
	minimum := uint64(21)
	if l != nil && l.keyed {
		minimum = 17
	}
	if l == nil || wholeSystem || hasExecutionTraps || traced || b.physicalAccess || b.tlb != nil || remaining < minimum {
		return 0
	}
	var sourceRegion, stackRegion, paletteRegion, destinationRegion *region
	var retired, iterations uint64
	generation := b.jitGen
	for remaining-retired >= minimum && b.regs[cpu.RegisterPC] == l.start {
		cursor, destination, count, sp := b.regs[l.cursor], b.regs[l.destination], b.regs[l.counter], b.regs[cpu.RegisterSP]
		sourceAddress := cursor >> 1
		source, offset, _, ok := b.privateRegionHit(&sourceRegion, sourceAddress, 1, cpu.PermissionRead)
		if !ok {
			break
		}
		index := uint32(source[offset])
		odd := cursor&1 != 0
		if odd {
			index >>= 4
		} else {
			index &= 15
		}
		var key, paletteBase uint32
		var stack []byte
		var stackOffset int
		var stackAddress, stackSize, firstSlot uint32
		transparent := false
		if l.keyed {
			address := sp + l.keySP
			if address&3 != 0 {
				break
			}
			data, offset, _, hit := b.privateRegionHit(&stackRegion, address, 4, cpu.PermissionRead)
			if !hit {
				break
			}
			key = loadLittle32(data, offset)
			transparent = index == key
		} else {
			firstSlot = min(l.cursorSP, l.destinationSP, l.countSP, l.paletteSP)
			last := max(l.cursorSP, l.destinationSP, l.countSP, l.paletteSP) + 4
			if uint64(sp)+uint64(last) > 1<<32 || sp&3 != 0 {
				break
			}
			stackAddress, stackSize = sp+firstSlot, last-firstSlot
			var perms cpu.Permissions
			stack, stackOffset, perms, ok = b.privateRegionHit(&stackRegion, stackAddress, int(stackSize), cpu.PermissionRead|cpu.PermissionWrite)
			if !ok || perms&cpu.PermissionExecute != 0 || colorLoopRangesOverlap(sourceAddress, 1, stackAddress, stackSize) || colorLoopRangesOverlap(destination, 2, stackAddress, stackSize) {
				break
			}
			paletteBase = loadLittle32(stack, stackOffset+int(l.paletteSP-firstSlot))
		}
		path := uint64(21)
		if transparent {
			path = 17
		}
		if remaining-retired < path {
			break
		}
		var color uint32
		if !transparent {
			if l.keyed {
				address := sp + l.paletteSP
				if address&3 != 0 {
					break
				}
				data, offset, _, hit := b.privateRegionHit(&stackRegion, address, 4, cpu.PermissionRead)
				if !hit {
					break
				}
				paletteBase = loadLittle32(data, offset)
			}
			address := paletteBase + index*2
			if address&1 != 0 || !l.keyed && colorLoopRangesOverlap(address, 2, stackAddress, stackSize) {
				break
			}
			palette, offset, _, hit := b.privateRegionHit(&paletteRegion, address, 2, cpu.PermissionRead)
			if !hit {
				break
			}
			color = uint32(palette[offset]) | uint32(palette[offset+1])<<8
			if destination&1 != 0 || colorLoopRangesOverlap(destination, 2, l.start, l.words*2) {
				break
			}
			dst, offset, perms, hit := b.privateRegionHit(&destinationRegion, destination, 2, cpu.PermissionWrite)
			if !hit {
				break
			}
			if !l.keyed {
				nextCursor := cursor + 1
				if l.reverse {
					nextCursor = cursor - 1
				}
				binary.LittleEndian.PutUint32(stack[stackOffset+int(l.destinationSP-firstSlot):], destination+2)
				binary.LittleEndian.PutUint32(stack[stackOffset+int(l.cursorSP-firstSlot):], nextCursor)
				binary.LittleEndian.PutUint32(stack[stackOffset+int(l.countSP-firstSlot):], count-1)
			}
			dst[offset], dst[offset+1] = byte(color), byte(color>>8)
			b.smcInvalidate(destination, 2, perms)
		}
		if l.reverse {
			b.regs[l.cursor]--
		} else {
			b.regs[l.cursor]++
		}
		b.regs[l.destination] += 2
		b.regs[l.counter]--
		if l.keyed {
			b.regs[l.index] = index
			b.regs[l.scratch] = key
			if !transparent {
				b.regs[l.scratch] = color
			}
			b.regs[l.temporary] = destination + 2
			b.regs[l.nextCount] = count - 1
		} else {
			b.regs[l.index] = color
			b.regs[l.scratch] = 15
			if odd {
				b.regs[l.scratch] = paletteBase
			}
		}
		b.setNZCV(count-1, true, false)
		retired += path
		iterations++
		if int32(count-1) <= 0 {
			b.regs[cpu.RegisterPC] = l.start + l.words*2
			break
		}
		b.regs[cpu.RegisterPC] = l.start
		if b.jitGen != generation {
			break
		}
	}
	b.executionStatistics.AcceleratedLoopIterations += iterations
	b.executionStatistics.AcceleratedLoopInstructions += retired
	return retired
}
