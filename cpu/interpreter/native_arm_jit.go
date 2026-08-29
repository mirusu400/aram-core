//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package interpreter

import "github.com/mirusu400/aram-core/cpu"

// runARMNative is the ARM counterpart of runThumbNative. Unsupported native
// encodings retain the portable ARM translated-block tier as their oracle and
// fallback, so adding emitter coverage never creates a second semantic path.
func (b *Backend) runARMNative(limit uint64) (uint64, *cpu.StopReason, error) {
	wholeSystem := b.systemBus != nil
	if wholeSystem && b.tracing() {
		return b.runARMJIT(limit)
	}
	b.nativeRemain = uint32(limit)
	for b.nativeRemain > 0 {
		pc := b.regs[cpu.RegisterPC]
		if wholeSystem {
			if b.takePendingInterrupt() {
				return limit - uint64(b.nativeRemain), nil, nil
			}
			pc = b.regs[cpu.RegisterPC]
			if b.executionTrapAt(cpu.ModeARM, pc) {
				reason := cpu.StopExecutionTrap
				return limit - uint64(b.nativeRemain), &reason, nil
			}
			b.instructionAddress = pc
		}
		block := b.nativeARMBlockAt(pc)
		if block == nil {
			if reason, err, done := b.interpretARMNative(1); done {
				return limit - uint64(b.nativeRemain), reason, err
			}
			continue
		}
		b.resolveFlags()
		status := callNativeBlock(block.entry, &b.regs[0], &b.nativeRemain)
		b.flags.dirty = false
		switch status & 0xff {
		case nativeStatusBail:
			b.refundNativeTail(status)
			bailPC, bailAddress := b.regs[cpu.RegisterPC], b.nativeBailAddress
			b.invalidateVirtualDataAt(bailAddress)
			if reason, err, done := b.interpretARMNative(1); done {
				return limit - uint64(b.nativeRemain), reason, err
			}
			b.noteNativeBail(cpu.ModeARM, bailPC, bailAddress)
		case nativeStatusSlow:
			b.refundNativeTail(status)
			if reason, err, done := b.interpretConditionalSlowARMNative(); done {
				return limit - uint64(b.nativeRemain), reason, err
			}
		case nativeStatusIRQ:
			b.refundNativeTail(status)
			if b.takePendingInterrupt() {
				return limit - uint64(b.nativeRemain), nil, nil
			}
			if reason, err, done := b.interpretARMNative(1); done {
				return limit - uint64(b.nativeRemain), reason, err
			}
		case nativeStatusBKPT:
			reason := cpu.StopBreakpoint
			return limit - uint64(b.nativeRemain), &reason, nil
		case nativeStatusMode:
			if b.regs[cpu.RegisterCPSR]&cpu.StatusThumb != 0 {
				b.mode = cpu.ModeThumb
				return limit - uint64(b.nativeRemain), nil, nil
			}
			b.mode = cpu.ModeARM
		case nativeStatusBudget:
			if b.nativeRemain > 0 {
				if reason, err, done := b.interpretARMNative(b.nativeRemain); done {
					return limit - uint64(b.nativeRemain), reason, err
				}
			}
		}
	}
	return limit - uint64(b.nativeRemain), nil, nil
}

// interpretConditionalSlowARMNative executes a promoted MMIO instruction after
// emitted condition evaluation has already proved it will run. It avoids
// re-entering the full ARM block runner (interrupt/trap/condition/dispatch) for
// this common boundary while retaining the same decoded closure as the oracle.
func (b *Backend) interpretConditionalSlowARMNative() (*cpu.StopReason, error, bool) {
	pc := b.regs[cpu.RegisterPC]
	block := b.armJITBlockAt(pc)
	if block == nil || len(block.arm) == 0 || block.arm[0].pc != pc {
		return b.interpretARMNative(1)
	}
	instruction := &block.arm[0]
	if b.systemBus != nil {
		b.instructionAddress = pc
	}
	b.regs[cpu.RegisterPC] = pc + 4
	_, reason, err := b.executeARMJITInstruction(instruction)
	if err != nil {
		return nil, err, true
	}
	b.nativeRemain--
	if reason != nil {
		return reason, nil, true
	}
	return nil, nil, b.mode != cpu.ModeARM
}

func (b *Backend) interpretARMNative(count uint32) (*cpu.StopReason, error, bool) {
	n, reason, err := b.runARMJIT(uint64(count))
	b.nativeRemain -= uint32(n)
	if err != nil {
		return nil, err, true
	}
	if reason != nil {
		return reason, nil, true
	}
	return nil, nil, b.mode != cpu.ModeARM
}

func (b *Backend) nativeARMBlockAt(pc uint32) *nativeBlock {
	slot := &b.nativeARMCache[pc>>2&(nativeCacheSize-1)]
	if block, ok := slot.lookup(pc, b.nativeGen); ok {
		return block
	}
	block, ok := b.nativeARMBlocks[pc]
	if !ok {
		block = b.translateNativeARMBlock(pc)
		b.cacheNativeARMBlock(pc, block)
	}
	slot.store(pc, b.nativeGen, block)
	return block
}

func (b *Backend) translateNativeARMBlock(pc uint32) *nativeBlock {
	body := b.newEmitter()
	cur := pc
	count := 0
	end := pc
	term := terminator{kind: termNone, next: pc}
	for count < nativeMaxBlock {
		if b.systemBus != nil && b.executionTrapAt(cpu.ModeARM, cur) {
			break
		}
		instruction, err := b.fetch32(cur)
		if err != nil {
			break
		}
		if b.systemBus != nil {
			body.interruptPoll(cur, count)
		}
		if b.nativeSlowAt(cpu.ModeARM, cur) {
			condition := uint8(instruction >> 28)
			if condition >= 0xe {
				break
			}
			body.conditionalSlow(condition, cur, count)
			count++
			cur += 4
			end = cur
			continue
		}
		kind, decoded := b.translateOneNativeARM(body, instruction, cur, count)
		if kind == translateTerminator {
			count++
			term = decoded
			end = cur + 4
			break
		}
		if kind == translateBail {
			break
		}
		count++
		cur += 4
		end = cur
	}
	if count == 0 {
		return nil
	}
	if term.kind == termNone {
		term.next = cur
	}

	main := b.newEmitter()
	main.prologue()
	gateOff := main.mark()
	main.gate(count, pc)
	main.appendCode(body.code())
	b.emitNativeTerminator(main, cpu.ModeARM, term, pc, gateOff)
	entry := b.arenaAppend(main.code())
	if entry == 0 {
		b.nativeInvalidate()
		if entry = b.arenaAppend(main.code()); entry == 0 {
			return nil
		}
	}
	grew := b.markCodePages(pc, end-pc)
	if pc < b.nativeCodeLo {
		b.nativeCodeLo, grew = pc, true
	}
	if end > b.nativeCodeHi {
		b.nativeCodeHi, grew = end, true
	}
	if grew {
		b.tlbClearWrite()
	}
	gate := entry + uintptr(gateOff)
	b.publishNativeLink(cpu.ModeARM, pc, gate)
	return &nativeBlock{
		start: pc, end: end, mode: cpu.ModeARM,
		count: count, entry: entry, gate: gate,
	}
}

func (b *Backend) translateOneNativeARM(
	e emitter,
	instruction, pc uint32,
	retired int,
) (int, terminator) {
	condition := uint8(instruction >> 28)
	if condition == 0xf {
		return translateBail, terminator{}
	}
	switch {
	case instruction&0x0ff000f0 == 0x01200070: // BKPT
		if condition != 0xe {
			return translateBail, terminator{}
		}
		return translateTerminator, terminator{kind: termBkpt, next: pc + 4}
	case instruction&0x0ffffff0 == 0x012fff10, // BX Rm
		instruction&0x0ffffff0 == 0x012fff30: // BLX Rm
		blx := instruction&0x20 != 0
		term := terminator{
			kind: termBranchExchange, reg: instruction & 0xf, pcRead: pc + 8,
			link: pc + 4, blx: blx, next: pc + 4,
		}
		if condition != 0xe {
			term.kind, term.cond = termCondBranchExchange, condition
		}
		return translateTerminator, term
	case instruction&0x0fff0ff0 == 0x016f0f10: // CLZ (not a register shift)
		return translateBail, terminator{}
	case instruction&0x0fc000f0 == 0x00000090: // MUL / MLA
		op := nativeARMMultiply{
			accumulate: instruction&(1<<21) != 0,
			setFlags:   instruction&(1<<20) != 0,
			rd:         instruction >> 16 & 0xf,
			rn:         instruction >> 12 & 0xf,
			rs:         instruction >> 8 & 0xf,
			rm:         instruction & 0xf,
		}
		if op.rd == cpu.RegisterPC || op.rm == cpu.RegisterPC ||
			op.rs == cpu.RegisterPC || (op.accumulate && op.rn == cpu.RegisterPC) {
			return translateBail, terminator{}
		}
		site := e.conditionStart(condition)
		e.armMultiply(op)
		e.conditionEnd(site)
		return translateBody, terminator{}
	case instruction&0x0e000000 == 0x0a000000: // B / BL immediate
		offset := int32(instruction&0x00ffffff) << 2
		if offset&(1<<25) != 0 {
			offset |= ^int32(0x03ffffff)
		}
		target := uint32(int32(pc+8) + offset)
		if instruction&(1<<24) != 0 {
			term := terminator{
				kind: termBranchLink, link: pc + 4, target: target, next: pc + 4,
			}
			if condition != 0xe {
				term.kind, term.cond = termCondBranchLink, condition
			}
			return translateTerminator, term
		}
		if condition == 0xe {
			return translateTerminator, terminator{kind: termUncond, target: target}
		}
		return translateTerminator, terminator{
			kind: termCond, cond: condition, target: target, next: pc + 4,
		}
	case instruction&0x0e000090 == 0x00000090 &&
		instruction&0x00000060 != 0: // halfword / signed byte transfer
		preIndex := instruction&(1<<24) != 0
		immediate := instruction&(1<<22) != 0
		load := instruction&(1<<20) != 0
		rn := instruction >> 16 & 0xf
		rd := instruction >> 12 & 0xf
		operation := uint8(instruction>>5) & 3
		writeback := !preIndex || instruction&(1<<21) != 0
		if rd == cpu.RegisterPC || (!load && operation != 1) ||
			(rn == cpu.RegisterPC && writeback) {
			return translateBail, terminator{}
		}
		access := memAccess{
			store:     !load,
			size:      2,
			rd:        rd,
			base:      rn,
			subtract:  instruction&(1<<23) == 0,
			postIndex: !preIndex,
			writeback: writeback && !(load && rd == rn),
		}
		if load && operation == 2 {
			access.size, access.signed = 1, true
		} else if load && operation == 3 {
			access.signed = true
		}
		if immediate {
			access.offset = instruction>>4&0xf0 | instruction&0xf
		} else {
			rm := instruction & 0xf
			if instruction&0x00000f00 != 0 || rm == cpu.RegisterPC ||
				(access.writeback && load && rd == rm) || rn == cpu.RegisterPC {
				return translateBail, terminator{}
			}
			access.index, access.hasIndex = rm, true
		}
		if rn == cpu.RegisterPC {
			address := pc + 8
			if access.subtract {
				address -= access.offset
			} else {
				address += access.offset
			}
			access.absolute, access.offset, access.subtract = true, address, false
		}
		site := e.conditionStart(condition)
		e.memory(access, pc, retired)
		e.conditionEnd(site)
		return translateBody, terminator{}
	case instruction&0x0fbf0fff == 0x010f0000, // MRS
		instruction&0x0fb0fff0 == 0x0120f000, // MSR register
		instruction&0x0fb0f000 == 0x0320f000: // MSR immediate
		return translateBail, terminator{}
	case instruction&0x0e000000 == 0x08000000: // LDM / STM
		registerMask := uint16(instruction)
		rn := instruction >> 16 & 0xf
		load := instruction&(1<<20) != 0
		loadPC := load && registerMask&(1<<cpu.RegisterPC) != 0
		// S either transfers the user register bank or performs an exception
		// return. STM with PC has pc+12 store semantics; keep that uncommon form
		// on the portable tier. Ordinary LDM-to-PC is a hot function return and
		// the emitter branch-exchanges through its final loaded word.
		if registerMask == 0 || rn == cpu.RegisterPC ||
			instruction&(1<<22) != 0 || (!load && registerMask&(1<<cpu.RegisterPC) != 0) {
			return translateBail, terminator{}
		}
		regs := make([]uint32, 0, 16)
		for register := uint32(0); register <= cpu.RegisterPC; register++ {
			if registerMask&(1<<register) != 0 {
				regs = append(regs, register)
			}
		}
		span := int32(4 * len(regs))
		access := multiAccess{
			store:  !load,
			regs:   regs,
			base:   rn,
			loadPC: loadPC,
		}
		preIndex := instruction&(1<<24) != 0
		increment := instruction&(1<<23) != 0
		switch {
		case increment && preIndex: // IB: first word above the original base
			access.startOffset = 4
			access.writebackOffset = span - 4
		case increment: // IA
			access.writebackOffset = span
		case preIndex: // DB: first word is one whole span below the base
			access.startOffset = -span
		default: // DA: last word is at the original base
			access.startOffset = -span + 4
			access.writebackOffset = -4
		}
		access.writeback = instruction&(1<<21) != 0
		if !access.store && registerMask&(1<<rn) != 0 {
			access.writeback = false
		}
		site := e.conditionStart(condition)
		e.multi(access, pc, retired)
		e.conditionEnd(site)
		if loadPC {
			return translateTerminator, terminator{kind: termInlineFallthrough, next: pc + 4}
		}
		return translateBody, terminator{}
	case instruction&0x0c000000 == 0x04000000: // LDR/STR
		registerOffset := instruction&(1<<25) != 0
		preIndex := instruction&(1<<24) != 0
		load := instruction&(1<<20) != 0
		rn := instruction >> 16 & 0xf
		rd := instruction >> 12 & 0xf
		writeback := !preIndex || instruction&(1<<21) != 0
		if rd == cpu.RegisterPC || (rn == cpu.RegisterPC && writeback) {
			return translateBail, terminator{}
		}
		access := memAccess{
			store:     !load,
			size:      4,
			rd:        rd,
			base:      rn,
			subtract:  instruction&(1<<23) == 0,
			postIndex: !preIndex,
			writeback: writeback && !(load && rd == rn),
		}
		if registerOffset {
			if instruction&(1<<4) != 0 {
				return translateBail, terminator{}
			}
			shifter, ok := decodeARMJITShifter(instruction &^ (1 << 25))
			if !ok || shifter.rm == cpu.RegisterPC || rn == cpu.RegisterPC ||
				(shifter.shiftType == 3 && shifter.amount == 0) ||
				(access.writeback && load && rd == shifter.rm) {
				return translateBail, terminator{}
			}
			shift := shifter.amount
			if shift == 0 && (shifter.shiftType == 1 || shifter.shiftType == 2) {
				shift = 32
			}
			access.index, access.hasIndex = shifter.rm, true
			access.indexShiftType, access.indexShift = shifter.shiftType, shift
		} else {
			access.offset = instruction & 0xfff
		}
		if instruction&(1<<22) != 0 {
			access.size = 1
		}
		if rn == cpu.RegisterPC {
			address := pc + 8
			if access.subtract {
				address -= access.offset
			} else {
				address += access.offset
			}
			access.absolute, access.offset, access.subtract = true, address, false
		}
		site := e.conditionStart(condition)
		e.memory(access, pc, retired)
		e.conditionEnd(site)
		return translateBody, terminator{}
	case instruction&0x0c000000 != 0:
		return translateBail, terminator{}
	}

	shifter, ok := decodeARMJITShifter(instruction)
	if !ok {
		return translateBail, terminator{}
	}
	opcode := uint8(instruction >> 21 & 0xf)
	if !nativeARMDataOpEmittable(opcode) {
		return translateBail, terminator{}
	}
	writes := opcode < 8 || opcode >= 12
	rd := instruction >> 12 & 0xf
	if writes && rd == cpu.RegisterPC {
		return translateBail, terminator{}
	}
	op := nativeARMDataOp{
		opcode:   opcode,
		setFlags: instruction&(1<<20) != 0 || !writes,
		rd:       rd,
		rn:       instruction >> 16 & 0xf,
		pcValue:  pc + 8,
		carry:    -1,
	}
	if shifter.immediate {
		op.operand = shifter.immediateValue
		if shifter.rotate != 0 {
			op.carry = int8(shifter.immediateValue >> 31)
		}
	} else {
		if !shifter.registerShift && shifter.shiftType == 3 && shifter.amount == 0 {
			return translateBail, terminator{}
		}
		shift := uint8(0)
		if !shifter.registerShift {
			shift = shifter.amount
			if shift == 0 && (shifter.shiftType == 1 || shifter.shiftType == 2) {
				shift = 32
			}
		}
		// Register-specified logical shifts need a runtime carry calculation for
		// the full 0/1/31/32/>32 cases. Keep those uncommon forms precise on the
		// portable tier; immediate shifts derive their carry inline below.
		logical := opcode < 2 || opcode == 8 || opcode == 9 || opcode >= 12
		if op.setFlags && logical && shifter.registerShift {
			return translateBail, terminator{}
		}
		op.operandReg = true
		op.operand = shifter.rm
		op.shiftType = shifter.shiftType
		op.shift = shift
		op.shiftReg = shifter.registerShift
		op.shiftRegister = shifter.rs
		op.shifterCarry = op.setFlags && logical && shift != 0
	}
	site := e.conditionStart(condition)
	if !e.armDataProcessing(op) {
		// Unreachable while the emitters agree with nativeARMDataOpEmittable.
		// Closing the site regardless keeps a future divergence a wasted block
		// instead of an unpatched branch: the emitters only touch host scratch
		// before they decide, so the skipped-over bytes write no guest state.
		e.conditionEnd(site)
		return translateBail, terminator{}
	}
	e.conditionEnd(site)
	return translateBody, terminator{}
}
