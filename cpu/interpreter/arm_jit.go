package interpreter

// Portable ARM translated blocks. The Thumb closure JIT in jit.go established
// the cache/execute shape; this file adds the ARM decoder and keeps the same
// precise-interpreter fallback contract for encodings that are not worthwhile
// to retain as closures.

import (
	"fmt"
	"math/bits"

	"github.com/mirusu400/aram-core/cpu"
)

// runARMJIT executes cached ARM closures up to limit. Whole-system checks stay
// at architectural instruction boundaries, exactly as in runARMInstrumented.
func (b *Backend) runARMJIT(limit uint64) (uint64, *cpu.StopReason, error) {
	wholeSystem := b.systemBus != nil
	hasExecutionTraps := len(b.executionTraps) != 0
	traced := b.tracing()
	var executed uint64
outer:
	for executed < limit && b.mode == cpu.ModeARM {
		pc := b.regs[cpu.RegisterPC]
		if wholeSystem {
			// Check host boundaries before a cache miss fetches/translates the
			// instruction at PC. Translation itself must not get ahead of an IRQ
			// or touch a trapped address.
			if b.takePendingInterrupt() {
				continue
			}
			pc = b.regs[cpu.RegisterPC]
			if hasExecutionTraps && b.executionTrapAt(cpu.ModeARM, pc) {
				reason := cpu.StopExecutionTrap
				return executed, &reason, nil
			}
		}
		block := b.armJITBlockAt(pc)
		if block == nil {
			n, reason, err := b.runARM(1)
			executed += n
			if err != nil {
				return executed, nil, err
			}
			if reason != nil {
				return executed, reason, nil
			}
			continue
		}
		blockInstructions := len(block.instrs)
		if remaining := limit - executed; uint64(blockInstructions) > remaining {
			blockInstructions = int(remaining)
		}
		for index := 0; index < blockInstructions; index++ {
			in := &block.instrs[index]
			if wholeSystem {
				// The outer dispatch already checked the first instruction's
				// boundary before fetching or translating this block. Later
				// instructions still poll individually, so MMIO-raised interrupts
				// and traps retain instruction-boundary precision.
				if index != 0 {
					if b.takePendingInterrupt() {
						continue outer
					}
					pc = b.regs[cpu.RegisterPC]
					if hasExecutionTraps && b.executionTrapAt(cpu.ModeARM, pc) {
						reason := cpu.StopExecutionTrap
						return executed, &reason, nil
					}
				}
				if traced {
					b.recordPC(pc)
				}
				b.instructionAddress = pc
			} else if traced {
				b.recordPC(in.pc)
			}
			b.regs[cpu.RegisterPC] = in.pc + 4
			if in.condition < 0xe && !b.conditionPassed(in.condition) {
				executed++
				continue
			}
			branched, reason, err := in.exec(b)
			if err != nil {
				return executed, nil, err
			}
			executed++
			if reason != nil {
				return executed, reason, nil
			}
			if b.mode != cpu.ModeARM {
				return executed, nil, nil
			}
			if branched {
				continue outer
			}
		}
	}
	return executed, nil, nil
}

func (b *Backend) armJITBlockAt(pc uint32) *jitBlock {
	slot := &b.armJITCache[int(pc>>2)&(jitCacheSize-1)]
	if slot.block != nil && slot.pc == pc && slot.gen == b.jitGen {
		return slot.block
	}
	block, ok := b.armJITBlocks[pc]
	if !ok {
		block = b.translateARMBlock(pc)
		b.cacheARMJITBlock(pc, block)
		if block != nil {
			if b.markJITCodePages(block.start, block.end-block.start) && b.nativeBlocks != nil {
				b.tlbClearWrite()
			}
			if block.start < b.jitCodeLo {
				b.jitCodeLo = block.start
			}
			if block.end > b.jitCodeHi {
				b.jitCodeHi = block.end
			}
		}
	}
	slot.pc, slot.gen, slot.block = pc, b.jitGen, block
	return block
}

func (b *Backend) translateARMBlock(pc uint32) *jitBlock {
	instrs := make([]jitInstr, 0, 16)
	cur := pc
	for len(instrs) < jitMaxBlock {
		instruction, err := b.fetch32(cur)
		if err != nil {
			break
		}
		exec, terminates, ok := b.translateARMInstr(instruction, cur)
		if !ok {
			break
		}
		instrs = append(instrs, jitInstr{
			pc: cur, condition: uint8(instruction >> 28), exec: exec,
		})
		cur += 4
		if terminates {
			break
		}
	}
	if len(instrs) == 0 {
		return nil
	}
	return &jitBlock{start: pc, end: cur, instrs: instrs}
}

// ARM conditions and the architectural PC advance are block-runner metadata,
// not closure work. Keeping this helper at decoder call sites makes the return
// shape readable while avoiding a second indirect closure call per instruction.
func armConditional(_ uint32, _ uint8, execute jitExec) jitExec {
	return execute
}

type armJITShifter struct {
	immediate      bool
	immediateValue uint32
	rotate         uint8
	rm             uint32
	shiftType      uint8
	registerShift  bool
	amount         uint8
	rs             uint32
}

func decodeARMJITShifter(instruction uint32) (armJITShifter, bool) {
	if instruction&(1<<25) != 0 {
		rotate := uint8(instruction>>8&0xf) * 2
		return armJITShifter{
			immediate:      true,
			immediateValue: bits.RotateLeft32(uint32(instruction&0xff), -int(rotate)),
			rotate:         rotate,
		}, true
	}
	shifter := armJITShifter{
		rm:        instruction & 0xf,
		shiftType: uint8(instruction>>5) & 3,
		amount:    uint8(instruction>>7) & 0x1f,
	}
	if instruction&(1<<4) != 0 {
		if instruction&(1<<7) != 0 {
			return armJITShifter{}, false
		}
		shifter.registerShift = true
		shifter.rs = instruction >> 8 & 0xf
	}
	return shifter, true
}

func (s armJITShifter) value(b *Backend, pc uint32) (uint32, bool) {
	carry := b.carry()
	if s.immediate {
		if s.rotate != 0 {
			carry = s.immediateValue&flagN != 0
		}
		return s.immediateValue, carry
	}
	value := b.readOperandRegister(s.rm, pc, cpu.ModeARM)
	amount := s.amount
	if s.registerShift {
		amount = uint8(b.readOperandRegister(s.rs, pc, cpu.ModeARM))
	}
	switch s.shiftType {
	case 0:
		return shiftLSL(value, amount, carry)
	case 1:
		if !s.registerShift && amount == 0 {
			amount = 32
		}
		return shiftLSR(value, amount, carry)
	case 2:
		if !s.registerShift && amount == 0 {
			amount = 32
		}
		return shiftASR(value, amount, carry)
	default:
		if !s.registerShift && amount == 0 {
			result := value >> 1
			if carry {
				result |= flagN
			}
			return result, value&1 != 0
		}
		return shiftROR(value, amount, carry)
	}
}

// valueNoCarry is the arithmetic-data-processing shifter path. Arithmetic
// instructions ignore the shifter's carry-out, so immediate operands and all
// shifts except RRX need not materialize the old CPSR carry just to discard it.
func (s armJITShifter) valueNoCarry(b *Backend, pc uint32) uint32 {
	if s.immediate {
		return s.immediateValue
	}
	value := b.readOperandRegister(s.rm, pc, cpu.ModeARM)
	amount := s.amount
	if s.registerShift {
		amount = uint8(b.readOperandRegister(s.rs, pc, cpu.ModeARM))
	}
	var result uint32
	switch s.shiftType {
	case 0:
		result, _ = shiftLSL(value, amount, false)
	case 1:
		if !s.registerShift && amount == 0 {
			amount = 32
		}
		result, _ = shiftLSR(value, amount, false)
	case 2:
		if !s.registerShift && amount == 0 {
			amount = 32
		}
		result, _ = shiftASR(value, amount, false)
	default:
		if !s.registerShift && amount == 0 {
			result = value >> 1
			if b.carry() {
				result |= flagN
			}
			return result
		}
		result, _ = shiftROR(value, amount, false)
	}
	return result
}

func (b *Backend) translateARMInstr(instruction, pc uint32) (jitExec, bool, bool) {
	condition := uint8(instruction >> 28)
	body := func(execute jitExec, terminates bool) (jitExec, bool, bool) {
		return armConditional(pc, condition, execute), terminates, true
	}

	switch {
	case instruction&0x0ff000f0 == 0x01200070: // BKPT
		return body(func(*Backend) (bool, *cpu.StopReason, error) {
			reason := cpu.StopBreakpoint
			return false, &reason, nil
		}, true)

	case instruction&0x0ffffff0 == 0x012fff10: // BX Rm
		rm := instruction & 0xf
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			b.branchExchange(b.readOperandRegister(rm, pc, cpu.ModeARM))
			return true, nil, nil
		}, true)

	case instruction&0x0ffffff0 == 0x012fff30: // BLX Rm
		rm := instruction & 0xf
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			target := b.readOperandRegister(rm, pc, cpu.ModeARM)
			b.regs[cpu.RegisterLR] = pc + 4
			b.branchExchange(target)
			return true, nil, nil
		}, true)

	case instruction&0xfe000000 == 0xfa000000: // BLX immediate
		offset := int32(instruction&0x00ffffff) << 2
		if offset&(1<<25) != 0 {
			offset |= ^int32(0x03ffffff)
		}
		if instruction&(1<<24) != 0 {
			offset += 2
		}
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterLR] = pc + 4
			b.regs[cpu.RegisterPC] = uint32(int32(pc+8) + offset)
			b.mode = cpu.ModeThumb
			b.setModeFlag()
			return true, nil, nil
		}, true)

	case instruction&0x0f000010 == 0x0e000010: // MRC/MCR CP15
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			return true, nil, b.executeCP15(pc, instruction)
		}, true)

	case instruction&0x0e000000 == 0x0a000000: // B / BL
		offset := int32(instruction&0x00ffffff) << 2
		if offset&(1<<25) != 0 {
			offset |= ^int32(0x03ffffff)
		}
		link := instruction&(1<<24) != 0
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			if link {
				b.regs[cpu.RegisterLR] = pc + 4
			}
			b.regs[cpu.RegisterPC] = uint32(int32(pc+8) + offset)
			return true, nil, nil
		}, true)

	case instruction&0x0fff0ff0 == 0x016f0f10: // CLZ
		rd := instruction >> 12 & 0xf
		rm := instruction & 0xf
		if rd == cpu.RegisterPC || rm == cpu.RegisterPC {
			return nil, false, false
		}
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[rd] = uint32(bits.LeadingZeros32(b.regs[rm]))
			return false, nil, nil
		}, false)

	case instruction&0x0fb00ff0 == 0x01000090: // SWP / SWPB
		// Atomic bus semantics remain with the precise interpreter.
		return nil, false, false

	case instruction&0x0fc000f0 == 0x00000090: // MUL / MLA
		accumulate := instruction&(1<<21) != 0
		setFlags := instruction&(1<<20) != 0
		rd := instruction >> 16 & 0xf
		rn := instruction >> 12 & 0xf
		rs := instruction >> 8 & 0xf
		rm := instruction & 0xf
		if rd == cpu.RegisterPC || rm == cpu.RegisterPC || rs == cpu.RegisterPC ||
			(accumulate && rn == cpu.RegisterPC) {
			return nil, false, false
		}
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			result := b.regs[rm] * b.regs[rs]
			if accumulate {
				result += b.regs[rn]
			}
			b.regs[rd] = result
			if setFlags {
				b.setNZ(result)
			}
			return false, nil, nil
		}, false)

	case instruction&0x0f8000f0 == 0x00800090: // long multiply
		signed := instruction&(1<<22) != 0
		accumulate := instruction&(1<<21) != 0
		setFlags := instruction&(1<<20) != 0
		rdHi := instruction >> 16 & 0xf
		rdLo := instruction >> 12 & 0xf
		rs := instruction >> 8 & 0xf
		rm := instruction & 0xf
		if rdHi == cpu.RegisterPC || rdLo == cpu.RegisterPC || rm == cpu.RegisterPC ||
			rs == cpu.RegisterPC || rdHi == rdLo {
			return nil, false, false
		}
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			var product uint64
			if signed {
				product = uint64(int64(int32(b.regs[rm])) * int64(int32(b.regs[rs])))
			} else {
				product = uint64(b.regs[rm]) * uint64(b.regs[rs])
			}
			if accumulate {
				product += uint64(b.regs[rdHi])<<32 | uint64(b.regs[rdLo])
			}
			b.regs[rdHi], b.regs[rdLo] = uint32(product>>32), uint32(product)
			if setFlags {
				b.resolveFlags()
				b.regs[cpu.RegisterCPSR] &^= flagN | flagZ
				if product == 0 {
					b.regs[cpu.RegisterCPSR] |= flagZ
				}
				if product&(uint64(1)<<63) != 0 {
					b.regs[cpu.RegisterCPSR] |= flagN
				}
			}
			return false, nil, nil
		}, false)

	case instruction&0x0e000090 == 0x00000090 && instruction&0x00000060 != 0:
		return b.translateARMHalfword(instruction, pc, condition)

	case instruction&0x0fbf0fff == 0x010f0000: // MRS
		rd := instruction >> 12 & 0xf
		if rd == cpu.RegisterPC {
			return nil, false, false
		}
		saved := instruction&(1<<22) != 0
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			value, err := b.readProgramStatus(saved)
			if err != nil {
				return false, nil, fmt.Errorf("ARM MRS at 0x%08x: %w", pc, err)
			}
			b.regs[rd] = value
			return false, nil, nil
		}, false)

	case instruction&0x0fb0fff0 == 0x0120f000: // MSR register
		rm := instruction & 0xf
		if rm == cpu.RegisterPC {
			return nil, false, false
		}
		saved := instruction&(1<<22) != 0
		fields := instruction >> 16 & 0xf
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			if !b.programStatusWriteAllowed(saved, fields) {
				return false, nil, b.unsupportedARM(pc, instruction)
			}
			if err := b.writeProgramStatus(saved, fields, b.regs[rm]); err != nil {
				return false, nil, fmt.Errorf("ARM MSR at 0x%08x: %w", pc, err)
			}
			return true, nil, nil
		}, true)

	case instruction&0x0fb0f000 == 0x0320f000: // MSR immediate
		rotate := int((instruction >> 8 & 0xf) * 2)
		value := bits.RotateLeft32(uint32(instruction&0xff), -rotate)
		saved := instruction&(1<<22) != 0
		fields := instruction >> 16 & 0xf
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			if !b.programStatusWriteAllowed(saved, fields) {
				return false, nil, b.unsupportedARM(pc, instruction)
			}
			if err := b.writeProgramStatus(saved, fields, value); err != nil {
				return false, nil, fmt.Errorf("ARM MSR at 0x%08x: %w", pc, err)
			}
			return true, nil, nil
		}, true)

	case instruction&0x0c000000 == 0x00000000: // data processing
		return b.translateARMDataProcessing(instruction, pc, condition)

	case instruction&0x0c000000 == 0x04000000: // LDR / STR
		return b.translateARMSingleTransfer(instruction, pc, condition)

	case instruction&0x0e000000 == 0x08000000: // LDM / STM
		return b.translateARMBlockTransfer(instruction, pc, condition)

	case instruction&0x0f000000 == 0x0f000000: // SWI
		return body(func(b *Backend) (bool, *cpu.StopReason, error) {
			if instruction&0x00ffffff == semihostingARMImmediate && b.handleSemihosting() {
				return true, nil, nil
			}
			if b.systemBus != nil {
				b.enterException(processorModeSupervisor, vectorSoftware, pc+4)
				return true, nil, nil
			}
			reason := cpu.StopBreakpoint
			return false, &reason, nil
		}, true)
	}
	return nil, false, false
}

func (b *Backend) translateARMDataProcessing(
	instruction, pc uint32,
	condition uint8,
) (jitExec, bool, bool) {
	shifter, ok := decodeARMJITShifter(instruction)
	if !ok {
		return nil, false, false
	}
	opcode := uint8(instruction >> 21 & 0xf)
	requestedFlags := instruction&(1<<20) != 0
	rn := instruction >> 16 & 0xf
	rd := instruction >> 12 & 0xf
	writesResult := opcode < 8 || opcode >= 12
	// The overwhelmingly common non-PC forms get one operation-specific
	// closure. Keeping the opcode switch in the translator is the actual decode
	// win: a hot ADD/MOV/CMP no longer re-enters a 16-way switch every time its
	// block runs.
	if rd != cpu.RegisterPC {
		var exec jitExec
		switch opcode {
		case 0x0: // AND
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				operand, carry := shifter.value(b, pc)
				result := b.readOperandRegister(rn, pc, cpu.ModeARM) & operand
				b.regs[rd] = result
				if requestedFlags {
					b.setNZC(result, carry)
				}
				return false, nil, nil
			}
		case 0x1: // EOR
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				operand, carry := shifter.value(b, pc)
				result := b.readOperandRegister(rn, pc, cpu.ModeARM) ^ operand
				b.regs[rd] = result
				if requestedFlags {
					b.setNZC(result, carry)
				}
				return false, nil, nil
			}
		case 0x2: // SUB
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				result, carry, overflow := addWithCarry(
					b.readOperandRegister(rn, pc, cpu.ModeARM), ^shifter.valueNoCarry(b, pc), 1)
				b.regs[rd] = result
				if requestedFlags {
					b.setNZCV(result, carry, overflow)
				}
				return false, nil, nil
			}
		case 0x3: // RSB
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				operand := shifter.valueNoCarry(b, pc)
				result, carry, overflow := addWithCarry(
					operand, ^b.readOperandRegister(rn, pc, cpu.ModeARM), 1)
				b.regs[rd] = result
				if requestedFlags {
					b.setNZCV(result, carry, overflow)
				}
				return false, nil, nil
			}
		case 0x4: // ADD
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				result, carry, overflow := addWithCarry(
					b.readOperandRegister(rn, pc, cpu.ModeARM), shifter.valueNoCarry(b, pc), 0)
				b.regs[rd] = result
				if requestedFlags {
					b.setNZCV(result, carry, overflow)
				}
				return false, nil, nil
			}
		case 0x5: // ADC
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				carryIn := uint32(0)
				if b.carry() {
					carryIn = 1
				}
				result, carry, overflow := addWithCarry(
					b.readOperandRegister(rn, pc, cpu.ModeARM), shifter.valueNoCarry(b, pc), carryIn)
				b.regs[rd] = result
				if requestedFlags {
					b.setNZCV(result, carry, overflow)
				}
				return false, nil, nil
			}
		case 0x6: // SBC
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				carryIn := uint32(0)
				if b.carry() {
					carryIn = 1
				}
				result, carry, overflow := addWithCarry(
					b.readOperandRegister(rn, pc, cpu.ModeARM), ^shifter.valueNoCarry(b, pc), carryIn)
				b.regs[rd] = result
				if requestedFlags {
					b.setNZCV(result, carry, overflow)
				}
				return false, nil, nil
			}
		case 0x7: // RSC
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				carryIn := uint32(0)
				if b.carry() {
					carryIn = 1
				}
				operand := shifter.valueNoCarry(b, pc)
				result, carry, overflow := addWithCarry(
					operand, ^b.readOperandRegister(rn, pc, cpu.ModeARM), carryIn)
				b.regs[rd] = result
				if requestedFlags {
					b.setNZCV(result, carry, overflow)
				}
				return false, nil, nil
			}
		case 0x8: // TST
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				operand, carry := shifter.value(b, pc)
				b.setNZC(b.readOperandRegister(rn, pc, cpu.ModeARM)&operand, carry)
				return false, nil, nil
			}
		case 0x9: // TEQ
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				operand, carry := shifter.value(b, pc)
				b.setNZC(b.readOperandRegister(rn, pc, cpu.ModeARM)^operand, carry)
				return false, nil, nil
			}
		case 0xa: // CMP
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				result, carry, overflow := addWithCarry(
					b.readOperandRegister(rn, pc, cpu.ModeARM), ^shifter.valueNoCarry(b, pc), 1)
				b.setNZCV(result, carry, overflow)
				return false, nil, nil
			}
		case 0xb: // CMN
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				result, carry, overflow := addWithCarry(
					b.readOperandRegister(rn, pc, cpu.ModeARM), shifter.valueNoCarry(b, pc), 0)
				b.setNZCV(result, carry, overflow)
				return false, nil, nil
			}
		case 0xc: // ORR
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				operand, carry := shifter.value(b, pc)
				result := b.readOperandRegister(rn, pc, cpu.ModeARM) | operand
				b.regs[rd] = result
				if requestedFlags {
					b.setNZC(result, carry)
				}
				return false, nil, nil
			}
		case 0xd: // MOV
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				result, carry := shifter.value(b, pc)
				b.regs[rd] = result
				if requestedFlags {
					b.setNZC(result, carry)
				}
				return false, nil, nil
			}
		case 0xe: // BIC
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				operand, carry := shifter.value(b, pc)
				result := b.readOperandRegister(rn, pc, cpu.ModeARM) &^ operand
				b.regs[rd] = result
				if requestedFlags {
					b.setNZC(result, carry)
				}
				return false, nil, nil
			}
		case 0xf: // MVN
			exec = func(b *Backend) (bool, *cpu.StopReason, error) {
				result, carry := shifter.value(b, pc)
				result = ^result
				b.regs[rd] = result
				if requestedFlags {
					b.setNZC(result, carry)
				}
				return false, nil, nil
			}
		}
		return armConditional(pc, condition, exec), false, true
	}
	exec := armConditional(pc, condition, func(b *Backend) (bool, *cpu.StopReason, error) {
		operand2, operandCarry := shifter.value(b, pc)
		left := b.readOperandRegister(rn, pc, cpu.ModeARM)
		var result uint32
		var carry, overflow bool
		setFlags := requestedFlags
		writeResult := writesResult
		arithmeticFlags := false
		switch opcode {
		case 0x0:
			result = left & operand2
		case 0x1:
			result = left ^ operand2
		case 0x2:
			result, carry, overflow = addWithCarry(left, ^operand2, 1)
			arithmeticFlags = true
		case 0x3:
			result, carry, overflow = addWithCarry(operand2, ^left, 1)
			arithmeticFlags = true
		case 0x4:
			result, carry, overflow = addWithCarry(left, operand2, 0)
			arithmeticFlags = true
		case 0x5:
			carryIn := uint32(0)
			if b.carry() {
				carryIn = 1
			}
			result, carry, overflow = addWithCarry(left, operand2, carryIn)
			arithmeticFlags = true
		case 0x6:
			carryIn := uint32(0)
			if b.carry() {
				carryIn = 1
			}
			result, carry, overflow = addWithCarry(left, ^operand2, carryIn)
			arithmeticFlags = true
		case 0x7:
			carryIn := uint32(0)
			if b.carry() {
				carryIn = 1
			}
			result, carry, overflow = addWithCarry(operand2, ^left, carryIn)
			arithmeticFlags = true
		case 0x8:
			result, setFlags, writeResult = left&operand2, true, false
		case 0x9:
			result, setFlags, writeResult = left^operand2, true, false
		case 0xa:
			result, carry, overflow = addWithCarry(left, ^operand2, 1)
			setFlags, writeResult, arithmeticFlags = true, false, true
		case 0xb:
			result, carry, overflow = addWithCarry(left, operand2, 0)
			setFlags, writeResult, arithmeticFlags = true, false, true
		case 0xc:
			result = left | operand2
		case 0xd:
			result = operand2
		case 0xe:
			result = left &^ operand2
		case 0xf:
			result = ^operand2
		}
		exceptionReturn := false
		branched := false
		if writeResult {
			if rd == cpu.RegisterPC {
				branched = true
				var status *uint32
				if setFlags {
					status = b.savedStatus(b.currentProcessorMode())
				}
				if status != nil {
					exceptionReturn = true
					if err := b.writeProgramStatus(false, 0xf, *status); err != nil {
						return false, nil, fmt.Errorf(
							"ARM data-processing exception return at 0x%08x: %w", pc, err)
					}
				}
				if b.mode == cpu.ModeThumb {
					b.regs[cpu.RegisterPC] = result &^ 1
				} else {
					b.regs[cpu.RegisterPC] = result &^ 3
				}
			} else {
				b.regs[rd] = result
			}
		}
		if setFlags && !exceptionReturn {
			if arithmeticFlags {
				b.setNZCV(result, carry, overflow)
			} else {
				b.setNZC(result, operandCarry)
			}
		}
		return branched, nil, nil
	})
	return exec, writesResult && rd == cpu.RegisterPC, true
}

func (b *Backend) translateARMSingleTransfer(
	instruction, pc uint32,
	condition uint8,
) (jitExec, bool, bool) {
	registerOffset := instruction&(1<<25) != 0
	preIndex := instruction&(1<<24) != 0
	up := instruction&(1<<23) != 0
	byteTransfer := instruction&(1<<22) != 0
	writeBack := instruction&(1<<21) != 0
	load := instruction&(1<<20) != 0
	rn := instruction >> 16 & 0xf
	rd := instruction >> 12 & 0xf
	immediateOffset := instruction & 0xfff
	var shifter armJITShifter
	if registerOffset {
		if instruction&(1<<4) != 0 {
			return nil, false, false
		}
		var ok bool
		shifter, ok = decodeARMJITShifter(instruction &^ (1 << 25))
		if !ok {
			return nil, false, false
		}
	}
	exec := armConditional(pc, condition, func(b *Backend) (bool, *cpu.StopReason, error) {
		base := b.readOperandRegister(rn, pc, cpu.ModeARM)
		offset := immediateOffset
		if registerOffset {
			offset, _ = shifter.value(b, pc)
		}
		indexedAddress := base
		if up {
			indexedAddress += offset
		} else {
			indexedAddress -= offset
		}
		address := base
		if preIndex {
			address = indexedAddress
		}
		branched := false
		if load {
			if byteTransfer {
				value, err := b.read8(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				if rd == cpu.RegisterPC {
					b.branchExchange(uint32(value))
					branched = true
				} else {
					b.regs[rd] = uint32(value)
				}
			} else {
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				if rd == cpu.RegisterPC {
					b.branchExchange(value)
					branched = true
				} else {
					b.regs[rd] = value
				}
			}
		} else {
			value := b.regs[rd]
			if rd == cpu.RegisterPC {
				value = pc + 12
			}
			if byteTransfer {
				if err := b.write8(address, byte(value), cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			} else if err := b.write32(address, value, cpu.PermissionWrite); err != nil {
				return false, nil, err
			}
		}
		if (!preIndex || writeBack) && !(load && rd == rn) {
			b.regs[rn] = indexedAddress
		}
		return branched, nil, nil
	})
	return exec, load && rd == cpu.RegisterPC, true
}

func (b *Backend) translateARMHalfword(
	instruction, pc uint32,
	condition uint8,
) (jitExec, bool, bool) {
	preIndex := instruction&(1<<24) != 0
	up := instruction&(1<<23) != 0
	immediate := instruction&(1<<22) != 0
	writeBack := instruction&(1<<21) != 0
	load := instruction&(1<<20) != 0
	rn := instruction >> 16 & 0xf
	rd := instruction >> 12 & 0xf
	operation := uint8(instruction>>5) & 3
	if rd == cpu.RegisterPC || (!load && operation != 1) {
		return nil, false, false
	}
	immOffset := instruction>>4&0xf0 | instruction&0xf
	rm := instruction & 0xf
	if !immediate && (instruction&0x00000f00 != 0 || rm == cpu.RegisterPC) {
		return nil, false, false
	}
	exec := armConditional(pc, condition, func(b *Backend) (bool, *cpu.StopReason, error) {
		offset := immOffset
		if !immediate {
			offset = b.regs[rm]
		}
		base := b.readOperandRegister(rn, pc, cpu.ModeARM)
		indexedAddress := base
		if up {
			indexedAddress += offset
		} else {
			indexedAddress -= offset
		}
		address := base
		if preIndex {
			address = indexedAddress
		}
		switch {
		case !load:
			if err := b.write16(address, uint16(b.regs[rd]), cpu.PermissionWrite); err != nil {
				return false, nil, err
			}
		case operation == 1:
			value, err := b.read16(address, cpu.PermissionRead)
			if err != nil {
				return false, nil, err
			}
			b.regs[rd] = uint32(value)
		case operation == 2:
			value, err := b.read8(address, cpu.PermissionRead)
			if err != nil {
				return false, nil, err
			}
			b.regs[rd] = uint32(int32(int8(value)))
		default:
			value, err := b.read16(address, cpu.PermissionRead)
			if err != nil {
				return false, nil, err
			}
			b.regs[rd] = uint32(int32(int16(value)))
		}
		if (!preIndex || writeBack) && !(load && rd == rn) {
			b.regs[rn] = indexedAddress
		}
		return false, nil, nil
	})
	return exec, false, true
}

func (b *Backend) translateARMBlockTransfer(
	instruction, pc uint32,
	condition uint8,
) (jitExec, bool, bool) {
	preIndex := instruction&(1<<24) != 0
	increment := instruction&(1<<23) != 0
	userOrPSR := instruction&(1<<22) != 0
	writeBack := instruction&(1<<21) != 0
	load := instruction&(1<<20) != 0
	rn := instruction >> 16 & 0xf
	registers := uint16(instruction)
	if registers == 0 || rn == cpu.RegisterPC {
		return nil, false, false
	}
	exec := armConditional(pc, condition, func(b *Backend) (bool, *cpu.StopReason, error) {
		exceptionReturn := userOrPSR && load && registers&(1<<cpu.RegisterPC) != 0
		transferUser := userOrPSR && !exceptionReturn
		currentMode := b.currentProcessorMode()
		if transferUser && (currentMode == processorModeUser || currentMode == processorModeSystem) {
			return false, nil, b.unsupportedARM(pc, instruction)
		}
		var restoredStatus uint32
		if exceptionReturn {
			status := b.savedStatus(currentMode)
			if status == nil {
				return false, nil, b.unsupportedARM(pc, instruction)
			}
			restoredStatus = *status
		}
		count := uint32(bits.OnesCount16(registers))
		base := b.readOperandRegister(rn, pc, cpu.ModeARM)
		var address uint32
		if increment {
			address = base
			if preIndex {
				address += 4
			}
		} else {
			address = base - count*4
			if !preIndex {
				address += 4
			}
		}
		var loadedPC uint32
		loadedProgramCounter := false
		for register := uint32(0); register < 16; register++ {
			if registers&(1<<register) == 0 {
				continue
			}
			if load {
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				if register == cpu.RegisterPC {
					loadedPC, loadedProgramCounter = value, true
				} else if transferUser {
					b.writeUserRegister(register, value)
				} else {
					b.regs[register] = value
				}
			} else {
				var value uint32
				if register == cpu.RegisterPC {
					value = pc + 12
				} else if transferUser {
					value = b.readUserRegister(register)
				} else {
					value = b.regs[register]
					if register == rn {
						value = base
					}
				}
				if err := b.write32(address, value, cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			}
			address += 4
		}
		if writeBack && (!load || registers&(1<<rn) == 0) {
			if increment {
				b.regs[rn] = base + count*4
			} else {
				b.regs[rn] = base - count*4
			}
		}
		if loadedProgramCounter {
			if exceptionReturn {
				if err := b.writeProgramStatus(false, 0xf, restoredStatus); err != nil {
					return false, nil, fmt.Errorf("ARM LDM exception return at 0x%08x: %w", pc, err)
				}
				if b.mode == cpu.ModeThumb {
					b.regs[cpu.RegisterPC] = loadedPC &^ 1
				} else {
					b.regs[cpu.RegisterPC] = loadedPC &^ 3
				}
			} else {
				b.branchExchange(loadedPC)
			}
		}
		return loadedProgramCounter, nil, nil
	})
	return exec, load && registers&(1<<cpu.RegisterPC) != 0, true
}
