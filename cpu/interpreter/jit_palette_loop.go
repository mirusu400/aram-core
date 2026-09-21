package interpreter

import "github.com/mirusu400/aram-core/cpu"

const thumbPaletteLoopInstructions = uint32(17)

// jitThumbPaletteLoop describes the common Thumb loop that expands one byte
// through a 16-bit lookup table into a destination surface. The generated code
// used by handset Java runtimes also folds a signed row counter and source
// stride into this loop, which is why the exact structural match is kept here
// instead of treating arbitrary memory loops as equivalent.
type jitThumbPaletteLoop struct {
	start       uint32
	source      uint32
	destination uint32
	counter     uint32
	index       uint32
	lookup      uint32
	stride      uint32
	limit       uint32
	lookupSP    uint32
	strideSP    uint32
	tableOffset uint32
}

func hasThumbPaletteLoopPrefix(instructions []thumbMicroInstr, start uint32) bool {
	return len(instructions) >= 3 && instructions[0].pc == start &&
		instructions[0].raw&0xffc0 == 0x7800 &&
		instructions[1].raw&0xf8ff == 0x2800 &&
		instructions[2].raw&0xff00 == 0xd000
}

func (b *Backend) classifyThumbPaletteLoop(start uint32) *jitThumbPaletteLoop {
	var words [thumbPaletteLoopInstructions]uint16
	for index := range words {
		word, err := b.fetch16(start + uint32(index)*2)
		if err != nil {
			return nil
		}
		words[index] = word
	}

	// LDRB index, [source, #0]
	if words[0]&0xffc0 != 0x7800 {
		return nil
	}
	indexReg := uint32(words[0] & 7)
	sourceReg := uint32(words[0]>>3) & 7
	// CMP index, #0; BEQ to the counter update at instruction 8.
	if words[1] != 0x2800|uint16(indexReg<<8) ||
		words[2]&0xff00 != 0xd000 ||
		thumbConditionalTarget(start+4, words[2]) != start+16 {
		return nil
	}
	// LDR lookup, [SP, #imm]; LSLS index, index, #1.
	if words[3]&0xf800 != 0x9800 ||
		words[4] != uint16(1<<6|indexReg<<3|indexReg) {
		return nil
	}
	lookupReg := uint32(words[3]>>8) & 7
	// ADDS index, index, lookup.
	if words[5]&0xfe00 != 0x1800 ||
		uint32(words[5]>>6)&7 != lookupReg ||
		uint32(words[5]>>3)&7 != indexReg || uint32(words[5])&7 != indexReg {
		return nil
	}
	// LDRH index, [index, #imm]; STRH index, [destination, #0].
	if words[6]&0xf800 != 0x8800 ||
		uint32(words[6]>>3)&7 != indexReg || uint32(words[6])&7 != indexReg ||
		words[7]&0xffc0 != 0x8000 || uint32(words[7])&7 != indexReg {
		return nil
	}
	destinationReg := uint32(words[7]>>3) & 7
	// ADDS index, counter, #1; LSLS index, index, #16.
	if words[8]&0xfe00 != 0x1c00 || uint32(words[8]>>6)&7 != 1 ||
		uint32(words[8])&7 != indexReg ||
		words[9] != uint16(16<<6|indexReg<<3|indexReg) {
		return nil
	}
	counterReg := uint32(words[8]>>3) & 7
	// LDR stride, [SP, #imm]; ASRS counter, index, #16;
	// LSRS index, index, #16.
	if words[10]&0xf800 != 0x9800 ||
		words[11] != uint16(0x1000|16<<6|indexReg<<3|counterReg) ||
		words[12] != uint16(0x0800|16<<6|indexReg<<3|indexReg) {
		return nil
	}
	strideReg := uint32(words[10]>>8) & 7
	// ADDS destination, #2; ADDS source, source, stride.
	if words[13] != 0x3002|uint16(destinationReg<<8) ||
		words[14]&0xfe00 != 0x1800 ||
		uint32(words[14]>>6)&7 != strideReg ||
		uint32(words[14]>>3)&7 != sourceReg || uint32(words[14])&7 != sourceReg {
		return nil
	}
	// CMP index, limit (high-register form); BLT back to the first load.
	if words[15]&0xff00 != 0x4500 || thumbHighRD(words[15]) != indexReg ||
		words[16]&0xff00 != 0xdb00 ||
		thumbConditionalTarget(start+32, words[16]) != start {
		return nil
	}
	limitReg := thumbHighRS(words[15])

	registers := []uint32{
		sourceReg, destinationReg, counterReg, indexReg,
		lookupReg, strideReg, limitReg,
	}
	for left := range registers {
		for right := left + 1; right < len(registers); right++ {
			if registers[left] == registers[right] {
				return nil
			}
		}
	}

	return &jitThumbPaletteLoop{
		start:       start,
		source:      sourceReg,
		destination: destinationReg,
		counter:     counterReg,
		index:       indexReg,
		lookup:      lookupReg,
		stride:      strideReg,
		limit:       limitReg,
		lookupSP:    uint32(words[3]&0xff) * 4,
		strideSP:    uint32(words[10]&0xff) * 4,
		tableOffset: uint32(words[6]>>6&0x1f) * 2,
	}
}

func thumbConditionalTarget(pc uint32, instruction uint16) uint32 {
	return uint32(int64(pc) + 4 + int64(int8(instruction))*2)
}

func thumbHighRS(instruction uint16) uint32 {
	return uint32(instruction>>3)&7 | uint32(instruction>>6)&1<<3
}

func thumbHighRD(instruction uint16) uint32 {
	return uint32(instruction)&7 | uint32(instruction>>7)&1<<3
}

func (b *Backend) accelerateThumbPaletteLoop(
	loop *jitThumbPaletteLoop,
	remaining uint64,
	wholeSystem, hasExecutionTraps, traced bool,
) (uint64, error) {
	if loop == nil || wholeSystem || hasExecutionTraps || traced ||
		b.physicalAccess || b.tlb != nil ||
		remaining < uint64(thumbPaletteLoopInstructions) {
		return 0, nil
	}

	var retired, iterations uint64
	startGeneration := b.jitGen
	var stackRegion, sourceRegion, tableRegion, destinationRegion *region
	for remaining-retired >= uint64(thumbPaletteLoopInstructions) &&
		b.regs[cpu.RegisterPC] == loop.start {
		stack := b.regs[cpu.RegisterSP]
		lookupData, lookupOffset, _, ok := b.privateRegionHit(
			&stackRegion,
			stack+loop.lookupSP,
			4,
			cpu.PermissionRead,
		)
		if !ok {
			break
		}
		strideData, strideOffset, _, ok := b.privateRegionHit(
			&stackRegion,
			stack+loop.strideSP,
			4,
			cpu.PermissionRead,
		)
		if !ok {
			break
		}
		sourceAddress := b.regs[loop.source]
		sourceData, sourceOffset, _, ok := b.privateRegionHit(
			&sourceRegion,
			sourceAddress,
			1,
			cpu.PermissionRead,
		)
		if !ok {
			break
		}
		value := sourceData[sourceOffset]
		lookupBase := loadLittle32(lookupData, lookupOffset)
		stride := loadLittle32(strideData, strideOffset)

		var (
			color       uint16
			destination []byte
			destOffset  int
			destPerms   cpu.Permissions
		)
		if value != 0 {
			tableAddress := lookupBase + uint32(value)*2 + loop.tableOffset
			table, tableOffset, _, hit := b.privateRegionHit(
				&tableRegion,
				tableAddress,
				2,
				cpu.PermissionRead,
			)
			if !hit {
				break
			}
			color = uint16(table[tableOffset]) | uint16(table[tableOffset+1])<<8
			destination, destOffset, destPerms, hit = b.privateRegionHit(
				&destinationRegion,
				b.regs[loop.destination],
				2,
				cpu.PermissionWrite,
			)
			if !hit {
				break
			}
		}

		b.regs[loop.index] = uint32(value)
		pathInstructions := uint64(12)
		if value != 0 {
			b.regs[loop.lookup] = lookupBase
			b.regs[loop.index] = uint32(color)
			destination[destOffset] = byte(color)
			destination[destOffset+1] = byte(color >> 8)
			b.smcInvalidate(b.regs[loop.destination], 2, destPerms)
			pathInstructions = uint64(thumbPaletteLoopInstructions)
		}

		next := b.regs[loop.counter] + 1
		shifted := next << 16
		b.regs[loop.index] = shifted
		b.regs[loop.stride] = stride
		b.regs[loop.counter] = uint32(int32(shifted) >> 16)
		b.regs[loop.index] = shifted >> 16
		b.regs[loop.destination] += 2
		b.regs[loop.source] += stride

		index := b.regs[loop.index]
		limit := b.regs[loop.limit]
		result, carry, overflow := addWithCarry(index, ^limit, 1)
		b.setNZCV(result, carry, overflow)
		retired += pathInstructions
		iterations++
		if int32(index) < int32(limit) {
			b.regs[cpu.RegisterPC] = loop.start
		} else {
			b.regs[cpu.RegisterPC] = loop.start + thumbPaletteLoopInstructions*2
			break
		}
		if b.jitGen != startGeneration {
			break
		}
	}
	if retired != 0 {
		b.executionStatistics.AcceleratedLoopIterations += iterations
		b.executionStatistics.AcceleratedLoopInstructions += retired
	}
	return retired, nil
}

func (b *Backend) privateRegionHit(
	cached **region,
	address uint32,
	size int,
	permission cpu.Permissions,
) ([]byte, int, cpu.Permissions, bool) {
	mapped := *cached
	if mapped != nil && mapped.permissions&permission == permission &&
		address >= mapped.address {
		offset := uint64(address - mapped.address)
		if offset+uint64(size) <= uint64(len(mapped.data)) {
			return mapped.data, int(offset), mapped.permissions, true
		}
	}
	var err error
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil || len(mapped.data)-offset < size {
		return nil, 0, 0, false
	}
	*cached = mapped
	return mapped.data, offset, mapped.permissions, true
}

func loadLittle32(data []byte, offset int) uint32 {
	return uint32(data[offset]) |
		uint32(data[offset+1])<<8 |
		uint32(data[offset+2])<<16 |
		uint32(data[offset+3])<<24
}
