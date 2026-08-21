package interpreter

import (
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

type cp15State struct {
	control                uint32
	translationTableBase   uint32
	domainAccessControl    uint32
	dataFaultStatus        uint32
	instructionFaultStatus uint32
	faultAddress           uint32
	processID              uint32
}

func (b *Backend) executeCP15(pc, instruction uint32) error {
	op1 := uint8(instruction>>21) & 7
	read := instruction&(1<<20) != 0
	crn := uint8(instruction >> 16 & 0xf)
	rd := uint32(instruction >> 12 & 0xf)
	coprocessor := uint8(instruction >> 8 & 0xf)
	op2 := uint8(instruction>>5) & 7
	crm := uint8(instruction & 0xf)
	if coprocessor != 15 || op1 != 0 {
		return b.unsupportedARM(pc, instruction)
	}
	if read {
		value, err := b.readCP15(crn, crm, op2)
		if err != nil {
			return fmt.Errorf("ARM MRC p15 at 0x%08x: %w", pc, err)
		}
		if rd == cpu.RegisterPC {
			b.resolveFlags()
			b.regs[cpu.RegisterCPSR] = b.regs[cpu.RegisterCPSR]&^0xf0000000 | value&0xf0000000
		} else {
			b.regs[rd] = value
		}
		return nil
	}
	if rd == cpu.RegisterPC {
		return b.unsupportedARM(pc, instruction)
	}
	if err := b.writeCP15(crn, crm, op2, b.regs[rd]); err != nil {
		return fmt.Errorf("ARM MCR p15 at 0x%08x: %w", pc, err)
	}
	return nil
}

func (b *Backend) readCP15(crn, crm, op2 uint8) (uint32, error) {
	switch {
	case crn == 1 && crm == 0 && op2 == 0:
		return b.cp15.control, nil
	case crn == 2 && crm == 0 && op2 == 0:
		return b.cp15.translationTableBase, nil
	case crn == 3 && crm == 0 && op2 == 0:
		return b.cp15.domainAccessControl, nil
	case crn == 5 && crm == 0 && op2 == 0:
		return b.cp15.dataFaultStatus, nil
	case crn == 5 && crm == 0 && op2 == 1:
		return b.cp15.instructionFaultStatus, nil
	case crn == 6 && crm == 0 && op2 == 0:
		return b.cp15.faultAddress, nil
	case crn == 13 && crm == 0 && op2 == 0:
		return b.cp15.processID, nil
	default:
		return 0, fmt.Errorf("unsupported CP15 read c%d,c%d,%d", crn, crm, op2)
	}
}

func (b *Backend) writeCP15(crn, crm, op2 uint8, value uint32) error {
	switch {
	case crn == 1 && crm == 0 && op2 == 0:
		if value != b.cp15.control {
			b.invalidateTLB()
			b.executeData = nil
			b.dataData = nil
		}
		b.cp15.control = value
		return nil
	case crn == 2 && crm == 0 && op2 == 0:
		b.cp15.translationTableBase = value
		b.invalidateTLB()
		return nil
	case crn == 3 && crm == 0 && op2 == 0:
		b.cp15.domainAccessControl = value
		return nil
	case crn == 5 && crm == 0 && op2 == 0:
		b.cp15.dataFaultStatus = value
		return nil
	case crn == 5 && crm == 0 && op2 == 1:
		b.cp15.instructionFaultStatus = value
		return nil
	case crn == 6 && crm == 0 && op2 == 0:
		b.cp15.faultAddress = value
		return nil
	case crn == 7 && crm == 5 && op2 == 0:
		// Invalidate the instruction cache. Interpreted fetches are coherent.
		b.executeData = nil
		return nil
	case crn == 7 && crm == 7 && op2 == 0:
		// Invalidate unified instruction and data caches. Guest memory is
		// coherent in the interpreter; clear both host lookup accelerators.
		b.executeData = nil
		b.dataData = nil
		return nil
	case crn == 8 && crm == 7 && op2 == 0:
		// Invalidate the unified software TLB. Translation-table contents are
		// otherwise permitted to remain stale exactly as cached hardware is.
		b.invalidateTLB()
		return nil
	case crn == 13 && crm == 0 && op2 == 0:
		b.cp15.processID = value
		b.invalidateTLB()
		return nil
	default:
		return fmt.Errorf("unsupported CP15 write c%d,c%d,%d", crn, crm, op2)
	}
}
