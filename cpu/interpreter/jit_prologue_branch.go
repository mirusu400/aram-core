package interpreter

import "github.com/mirusu400/aram-core/cpu"

// A common compiler prologue saves registers, moves two arguments into callee
// saved registers, then checks an argument. The final compare defines every
// arithmetic flag, so the preceding flag-only intermediate results need not be
// materialized when all five instructions fit in the execution budget.
type jitThumbPrologueBranch struct {
	start, target       uint32
	push, first, second uint16
	compareRegister     uint32
	compareImmediate    uint32
	condition           uint8
}

func classifyThumbPrologueBranch(block *jitBlock) *jitThumbPrologueBranch {
	if len(block.thumb) != 5 || uint64(block.start)+12 >= 1<<32 {
		return nil
	}
	w := block.thumb
	if w[0].raw&0xfe00 != 0xb400 || w[0].raw&0x1ff == 0 ||
		w[1].raw&0xffc0 != 0x1c00 || w[2].raw&0xffc0 != 0x1c00 ||
		w[3].raw&0xf800 != 0x2800 || w[4].raw&0xf000 != 0xd000 || (w[4].raw>>8)&15 >= 14 {
		return nil
	}
	return &jitThumbPrologueBranch{
		start: block.start, target: thumbConditionalTarget(block.start+8, w[4].raw),
		push: w[0].raw, first: w[1].raw, second: w[2].raw,
		compareRegister:  uint32(w[3].raw>>8) & 7,
		compareImmediate: uint32(w[3].raw & 255), condition: uint8(w[4].raw>>8) & 15,
	}
}

func (b *Backend) accelerateThumbPrologueBranch(p *jitThumbPrologueBranch, remaining uint64, wholeSystem, hasExecutionTraps, traced bool) uint64 {
	if remaining < 5 || wholeSystem || hasExecutionTraps || traced || !b.pushPrivateThumbRegisters(p.push) {
		return 0
	}
	b.regs[p.first&7] = b.regs[(p.first>>3)&7]
	b.regs[p.second&7] = b.regs[(p.second>>3)&7]
	result, carry, overflow := addWithCarry(b.regs[p.compareRegister], ^p.compareImmediate, 1)
	b.setNZCV(result, carry, overflow)
	b.regs[cpu.RegisterPC] = p.start + 10
	if b.conditionPassed(p.condition) {
		b.regs[cpu.RegisterPC] = p.target
	}
	if collectThumbMicro {
		b.recordThumbBranchBlock(true)
	}
	return 5
}
