package interpreter

import "github.com/mirusu400/aram-core/cpu"

// accelerateCountedLoop skips only complete, proven-taken iterations and
// reserves one complete iteration for the ordinary translated executor. That
// final iteration materializes the same NZCV state and branch decision the
// precise JIT would expose at the end of every Run batch.
func (b *Backend) accelerateCountedLoop(
	block *jitBlock,
	remaining uint64,
	wholeSystem, hasExecutionTraps, traced bool,
) uint64 {
	loop := block.countedLoop
	if !b.loopAcceleration || loop == nil || loop.instructions == 0 ||
		hasExecutionTraps || traced {
		return 0
	}
	if wholeSystem && b.regs[cpu.RegisterCPSR]&(statusIRQDisable|statusFIQDisable) !=
		statusIRQDisable|statusFIQDisable {
		return 0
	}

	loopInstructions := uint64(loop.instructions)
	if remaining <= loopInstructions {
		return 0
	}
	counter := b.regs[loop.register]
	if counter <= 1 {
		return 0
	}

	iterations := (remaining - loopInstructions) / loopInstructions
	if counterLimit := uint64(counter - 1); iterations > counterLimit {
		iterations = counterLimit
	}
	if iterations == 0 {
		return 0
	}

	b.regs[loop.register] = counter - uint32(iterations)
	instructions := iterations * loopInstructions
	b.executionStatistics.AcceleratedLoopIterations += iterations
	b.executionStatistics.AcceleratedLoopInstructions += instructions
	return instructions
}

func classifyThumbCountedLoop(block *jitBlock) *jitCountedLoop {
	if len(block.thumb) != 2 || len(block.arm) != 0 ||
		block.thumb[0].pc != block.start || block.thumb[1].pc != block.start+2 {
		return nil
	}
	register, ok := thumbDecrementByOneRegister(block.thumb[0])
	if !ok || !thumbBNETo(block.thumb[1], block.start) {
		return nil
	}
	return &jitCountedLoop{register: register, instructions: 2}
}

func thumbDecrementByOneRegister(in thumbMicroInstr) (uint32, bool) {
	switch in.op {
	case thumbSubtractImmediate: // SUBS Rd, #1
		if in.raw&0xf800 != 0x3800 || in.raw&0xff != 1 {
			return 0, false
		}
		return uint32(in.raw>>8) & 7, true

	case thumbAddSubtract: // SUBS Rd, Rd, #1
		if in.raw&0xfe00 != 0x1e00 || uint32(in.raw>>6)&7 != 1 {
			return 0, false
		}
		source := uint32(in.raw>>3) & 7
		destination := uint32(in.raw) & 7
		return destination, source == destination
	}
	return 0, false
}

func thumbBNETo(in thumbMicroInstr, target uint32) bool {
	if in.op != thumbConditionalBranch || uint8(in.raw>>8)&0xf != 1 {
		return false
	}
	offset := int32(int8(in.raw&0xff)) << 1
	return uint32(int64(in.pc)+4+int64(offset)) == target
}

func classifyARMCountedLoop(block *jitBlock) *jitCountedLoop {
	if len(block.arm) != 2 || len(block.thumb) != 0 ||
		block.arm[0].pc != block.start || block.arm[1].pc != block.start+4 {
		return nil
	}
	register, ok := armDecrementByOneRegister(block.arm[0])
	if !ok || !armBNETo(block.arm[1], block.start) {
		return nil
	}
	return &jitCountedLoop{register: register, instructions: 2}
}

func armDecrementByOneRegister(in jitInstr) (uint32, bool) {
	instruction := in.raw
	if instruction>>28 != 0xe || instruction&0x0e000000 != 0x02000000 ||
		instruction>>21&0xf != 0x2 || instruction&(1<<20) == 0 ||
		instruction>>8&0xf != 0 || instruction&0xff != 1 {
		return 0, false
	}
	source := instruction >> 16 & 0xf
	destination := instruction >> 12 & 0xf
	if source != destination || destination == cpu.RegisterLR || destination == cpu.RegisterPC {
		return 0, false
	}
	return destination, true
}

func armBNETo(in jitInstr, target uint32) bool {
	instruction := in.raw
	if instruction>>28 != 1 || instruction&0x0f000000 != 0x0a000000 {
		return false
	}
	offset := int32(instruction & 0x00ffffff)
	if offset&(1<<23) != 0 {
		offset |= ^int32(0x00ffffff)
	}
	return uint32(int64(in.pc)+8+int64(offset<<2)) == target
}
