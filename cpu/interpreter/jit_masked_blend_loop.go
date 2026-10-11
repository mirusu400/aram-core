package interpreter

import "github.com/mirusu400/aram-core/cpu"

// Match the complete in-place halfword blend, including its pre-test and two
// PC-relative mask loads. Masks and bias remain guest data and are reloaded
// each iteration, including when the destination aliases a mask literal.
type jitThumbMaskedBlendLoop struct {
	start, exit, firstMask, secondMask                  uint32
	destination, counter, temporary, result, mask, bias uint32
}

func (b *Backend) classifyThumbMaskedBlendLoop(start uint32) *jitThumbMaskedBlendLoop {
	if uint64(start)+32 >= 1<<32 {
		return nil
	}
	var w [16]uint16
	for i := range w {
		v, err := b.fetch16(start + uint32(i)*2)
		if err != nil {
			return nil
		}
		w[i] = v
	}
	l := &jitThumbMaskedBlendLoop{start: start, exit: start + 32}
	l.counter = uint32(w[0]>>8) & 7
	l.destination, l.temporary = uint32(w[2]>>3)&7, uint32(w[2])&7
	l.result, l.mask, l.bias = uint32(w[5])&7, uint32(w[3]>>8)&7, uint32(w[11]>>6)&7
	d, n, x, r, m, bias := uint16(l.destination), uint16(l.counter), uint16(l.temporary), uint16(l.result), uint16(l.mask), uint16(l.bias)
	if w[0] != 0x2800|n<<8 || w[1]&0xff00 != 0xdd00 || thumbConditionalTarget(start+2, w[1]) != l.exit ||
		w[2] != 0x8800|d<<3|x || w[3]&0xff00 != 0x4800|m<<8 || w[4] != 0x0400|x<<3|x ||
		w[5] != 0x0c40|x<<3|r || w[6] != 0x4000|m<<3|r || w[7]&0xff00 != 0x4800|m<<8 ||
		w[8] != 0x0c80|x<<3|x || w[9] != 0x4000|m<<3|x || w[10] != 0x1800|x<<6|r<<3|r ||
		w[11] != 0x1800|bias<<6|r<<3|r || w[12] != 0x8000|d<<3|r || w[13] != 0x3801|n<<8 ||
		w[14] != 0x3002|d<<8 || !thumbShortBranchTo(start, 15, w[15], start) {
		return nil
	}
	regs := []uint32{l.destination, l.counter, l.temporary, l.result, l.mask, l.bias}
	for i, r := range regs {
		for _, other := range regs[i+1:] {
			if r == other {
				return nil
			}
		}
	}
	l.firstMask = (start+10)&^3 + uint32(w[3]&255)*4
	l.secondMask = (start+18)&^3 + uint32(w[7]&255)*4
	return l
}

func (b *Backend) accelerateThumbMaskedBlendLoop(l *jitThumbMaskedBlendLoop, remaining uint64, wholeSystem, hasExecutionTraps, traced bool) uint64 {
	if l == nil || wholeSystem || hasExecutionTraps || traced || b.physicalAccess || b.tlb != nil || remaining < 2 {
		return 0
	}
	var destinationRegion, maskRegion *region
	var retired, iterations uint64
	generation := b.jitGen
	for remaining-retired >= 2 && b.regs[cpu.RegisterPC] == l.start {
		count := b.regs[l.counter]
		if int32(count) <= 0 {
			b.setNZCV(count, true, false)
			b.regs[cpu.RegisterPC] = l.exit
			retired += 2
			break
		}
		if remaining-retired < 16 {
			break
		}
		destination := b.regs[l.destination]
		if destination&1 != 0 || colorLoopRangesOverlap(destination, 2, l.start, 32) {
			break
		}
		dst, offset, perms, ok := b.privateRegionHit(&destinationRegion, destination, 2, cpu.PermissionRead|cpu.PermissionWrite)
		if !ok {
			break
		}
		first, firstOffset, _, ok := b.privateRegionHit(&maskRegion, l.firstMask, 4, cpu.PermissionRead)
		if !ok {
			break
		}
		firstValue := loadLittle32(first, firstOffset)
		second, secondOffset, _, ok := b.privateRegionHit(&maskRegion, l.secondMask, 4, cpu.PermissionRead)
		if !ok {
			break
		}
		secondValue := loadLittle32(second, secondOffset)
		pixel := uint32(dst[offset]) | uint32(dst[offset+1])<<8
		temporary := (pixel >> 2) & secondValue
		result := (pixel >> 1) & firstValue
		result += temporary + b.regs[l.bias]
		dst[offset], dst[offset+1] = byte(result), byte(result>>8)
		b.smcInvalidate(destination, 2, perms)
		b.regs[l.temporary], b.regs[l.result], b.regs[l.mask] = temporary, result, secondValue
		b.regs[l.counter] = count - 1
		next, carry, overflow := addWithCarry(destination, 2, 0)
		b.regs[l.destination] = next
		b.setNZCV(next, carry, overflow)
		retired += 16
		iterations++
		if b.jitGen != generation {
			break
		}
	}
	b.executionStatistics.AcceleratedLoopIterations += iterations
	b.executionStatistics.AcceleratedLoopInstructions += retired
	return retired
}
