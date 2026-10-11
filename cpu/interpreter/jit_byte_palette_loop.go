package interpreter

import "github.com/mirusu400/aram-core/cpu"

// jitThumbBytePaletteLoop expands contiguous byte indices through a stack-held
// 16-bit palette. Match register relationships, not a title or fixed register
// assignment. The keyed form keeps its precomputed cursor and count temporaries
// visible, including on transparent pixels.
type jitThumbBytePaletteLoop struct {
	start, words                    uint32
	source, destination, index      uint32
	counter, temporary, nextAddress uint32
	paletteSP, keySP                uint32
	keyed                           bool
}

func (b *Backend) classifyThumbBytePaletteLoop(start uint32) *jitThumbBytePaletteLoop {
	if uint64(start)+4 > 1<<32 {
		return nil
	}
	var w [15]uint16
	for i := 0; i < 2; i++ {
		word, err := b.fetch16(start + uint32(i)*2)
		if err != nil {
			return nil
		}
		w[i] = word
	}
	if w[0]&0xffc0 != 0x7800 {
		return nil
	} // LDRB index, [source]
	l := &jitThumbBytePaletteLoop{start: start, source: uint32(w[0]>>3) & 7, index: uint32(w[0]) & 7}
	if w[1]&0xf800 == 0x9800 {
		l.words = 10
	} else if w[1]&0xfe00 == 0x1e00 {
		l.words, l.keyed = 15, true
	} else {
		return nil
	}
	if uint64(start)+uint64(l.words)*2 >= 1<<32 {
		return nil
	}
	for i := uint32(2); i < l.words; i++ {
		word, err := b.fetch16(start + i*2)
		if err != nil {
			return nil
		}
		w[i] = word
	}
	s, x := uint16(l.source), uint16(l.index)
	if !l.keyed {
		// LDR table,[SP]; LSL index,#1; LDRH index,[index,table];
		// SUB count,#1; STRH index,[dest]; ADD source,#1; ADD dest,#2;
		// CMP count,#0; BGT loop.
		l.temporary, l.paletteSP = uint32(w[1]>>8)&7, uint32(w[1]&255)*4
		l.counter, l.destination = uint32(w[4]>>8)&7, uint32(w[5]>>3)&7
		c, d, table := uint16(l.counter), uint16(l.destination), uint16(l.temporary)
		if w[2] != 0x0040|x<<3|x || w[3] != 0x5a00|table<<6|x<<3|x ||
			w[4] != 0x3801|c<<8 || w[5] != 0x8000|d<<3|x ||
			w[6] != 0x3001|s<<8 || w[7] != 0x3002|d<<8 || w[8] != 0x2800|c<<8 {
			return nil
		}
	} else {
		// Compute the next count/cursor before testing the stack-held key.
		// Only non-key pixels execute the four palette load/store instructions.
		l.counter, l.temporary = uint32(w[1]>>3)&7, uint32(w[1])&7
		l.destination, l.nextAddress = uint32(w[4]>>3)&7, uint32(w[4])&7
		l.paletteSP, l.keySP = uint32(w[7]&255)*4, uint32(w[2]&255)*4
		c, d, tmp, next := uint16(l.counter), uint16(l.destination), uint16(l.temporary), uint16(l.nextAddress)
		if w[1] != 0x1e40|c<<3|tmp || w[2]&0xff00 != 0x9800|c<<8 ||
			w[3] != 0x3001|s<<8 || w[4] != 0x1c80|d<<3|next ||
			w[5] != 0x4280|c<<3|x || w[6]&0xff00 != 0xd000 ||
			thumbConditionalTarget(start+12, w[6]) != start+22 ||
			w[7]&0xff00 != 0x9800|c<<8 || w[8] != 0x0040|x<<3|x ||
			w[9] != 0x5a00|c<<6|x<<3|x || w[10] != 0x8000|d<<3|x ||
			w[11] != 0x1c00|next<<3|d || w[12] != 0x1c00|tmp<<3|c ||
			w[13] != 0x2800|tmp<<8 {
			return nil
		}
	}
	if w[l.words-1]&0xff00 != 0xdc00 || thumbConditionalTarget(start+(l.words-1)*2, w[l.words-1]) != start {
		return nil
	}
	registers := []uint32{l.source, l.destination, l.index, l.counter, l.temporary}
	if l.keyed {
		registers = append(registers, l.nextAddress)
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

func (b *Backend) accelerateThumbBytePaletteLoop(l *jitThumbBytePaletteLoop, remaining uint64, wholeSystem, hasExecutionTraps, traced bool) uint64 {
	minimum := uint64(10)
	if l != nil && l.keyed {
		minimum = 11
	}
	if l == nil || wholeSystem || hasExecutionTraps || traced || b.physicalAccess || b.tlb != nil || remaining < minimum {
		return 0
	}
	var sourceRegion, stackRegion, paletteRegion, destinationRegion *region
	var retired, iterations uint64
	generation := b.jitGen
	for remaining-retired >= minimum && b.regs[cpu.RegisterPC] == l.start {
		src, off, _, ok := b.privateRegionHit(&sourceRegion, b.regs[l.source], 1, cpu.PermissionRead)
		if !ok {
			break
		}
		index := uint32(src[off])
		transparent := false
		if l.keyed {
			address := b.regs[cpu.RegisterSP] + l.keySP
			if address&3 != 0 {
				break
			}
			data, offset, _, hit := b.privateRegionHit(&stackRegion, address, 4, cpu.PermissionRead)
			if !hit {
				break
			}
			transparent = index == loadLittle32(data, offset)
		}
		path := uint64(l.words)
		if transparent {
			path = 11
		}
		if remaining-retired < path {
			break
		}
		var paletteBase, color uint32
		if !transparent {
			address := b.regs[cpu.RegisterSP] + l.paletteSP
			if address&3 != 0 {
				break
			}
			data, offset, _, hit := b.privateRegionHit(&stackRegion, address, 4, cpu.PermissionRead)
			if !hit {
				break
			}
			paletteBase = loadLittle32(data, offset)
			address = paletteBase + index*2
			if address&1 != 0 {
				break
			}
			palette, offset, _, hit := b.privateRegionHit(&paletteRegion, address, 2, cpu.PermissionRead)
			if !hit {
				break
			}
			color = uint32(palette[offset]) | uint32(palette[offset+1])<<8
			address = b.regs[l.destination]
			if address&1 != 0 || colorLoopRangesOverlap(address, 2, l.start, l.words*2) {
				break
			}
			dst, offset, perms, hit := b.privateRegionHit(&destinationRegion, address, 2, cpu.PermissionWrite)
			if !hit {
				break
			}
			dst[offset], dst[offset+1] = byte(color), byte(color>>8)
			b.smcInvalidate(address, 2, perms)
			index = color
		}
		next := b.regs[l.counter] - 1
		b.regs[l.source]++
		b.regs[l.destination] += 2
		b.regs[l.index] = index
		b.regs[l.counter] = next
		if l.keyed {
			b.regs[l.temporary] = next
			b.regs[l.nextAddress] = b.regs[l.destination]
		} else {
			b.regs[l.temporary] = paletteBase
		}
		// The final CMP count,#0 replaces all arithmetic flags from the body.
		b.setNZCV(next, true, false)
		retired += path
		iterations++
		if int32(next) <= 0 {
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
