package interpreter

import "github.com/mirusu400/aram-core/cpu"

// Fuse a complete load / immediate / AND / compare / conditional branch.
// The final comparison overwrites the intermediate arithmetic flags. A proven
// private read is the only potentially faulting instruction in this sequence.
type jitThumbLoadMaskBranch struct {
	start, target                                         uint32
	loadBase, loadDestination, loadOffset                 uint32
	moveDestination, moveValue, andSource, andDestination uint32
	compareRegister, compareImmediate                     uint32
	condition                                             uint8
}

func classifyThumbLoadMaskBranch(block *jitBlock) *jitThumbLoadMaskBranch {
	if len(block.thumb) != 5 || uint64(block.start)+12 >= 1<<32 {
		return nil
	}
	w := block.thumb
	if w[0].raw&0xf800 != 0x6800 || w[1].raw&0xf800 != 0x2000 || w[2].raw&0xffc0 != 0x4000 ||
		w[3].raw&0xf800 != 0x2800 || w[4].raw&0xf000 != 0xd000 || (w[4].raw>>8)&15 >= 14 {
		return nil
	}
	return &jitThumbLoadMaskBranch{
		start: block.start, target: thumbConditionalTarget(block.start+8, w[4].raw),
		loadBase: uint32(w[0].raw>>3) & 7, loadDestination: uint32(w[0].raw) & 7, loadOffset: (uint32(w[0].raw>>6) & 31) * 4,
		moveDestination: uint32(w[1].raw>>8) & 7, moveValue: uint32(w[1].raw & 255),
		andSource: uint32(w[2].raw>>3) & 7, andDestination: uint32(w[2].raw) & 7,
		compareRegister: uint32(w[3].raw>>8) & 7, compareImmediate: uint32(w[3].raw & 255), condition: uint8(w[4].raw>>8) & 15,
	}
}

func (b *Backend) accelerateThumbLoadMaskBranch(p *jitThumbLoadMaskBranch, remaining uint64, wholeSystem, hasExecutionTraps, traced bool) uint64 {
	if remaining < 5 || wholeSystem || hasExecutionTraps || traced || b.physicalAccess || b.tlb != nil {
		return 0
	}
	address := b.regs[p.loadBase] + p.loadOffset
	entry := &b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)]
	value, hit := cachedJITWord(entry, address)
	if !hit {
		return 0
	}
	b.regs[p.loadDestination] = value
	b.regs[p.moveDestination] = p.moveValue
	b.regs[p.andDestination] &= b.regs[p.andSource]
	result, carry, overflow := addWithCarry(b.regs[p.compareRegister], ^p.compareImmediate, 1)
	b.setNZCV(result, carry, overflow)
	b.regs[cpu.RegisterPC] = p.start + 10
	if b.conditionPassed(p.condition) {
		b.regs[cpu.RegisterPC] = p.target
	}
	if collectThumbMicro {
		b.recordThumbBranchBlock(false)
	}
	return 5
}
