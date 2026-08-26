package interpreter

import (
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

type processorMode uint32

const (
	processorModeUser       processorMode = 0x10
	processorModeFIQ        processorMode = 0x11
	processorModeIRQ        processorMode = 0x12
	processorModeSupervisor processorMode = 0x13
	processorModeAbort      processorMode = 0x17
	processorModeUndefined  processorMode = 0x1b
	processorModeSystem     processorMode = 0x1f
	processorModeMask                     = 0x1f
)

type bankedRegisters struct {
	userHigh      [5]uint32
	userStackLink [2]uint32
	fiq           [7]uint32
	irq           [2]uint32
	supervisor    [2]uint32
	abort         [2]uint32
	undefined     [2]uint32
}

type savedProgramStatus struct {
	fiq        uint32
	irq        uint32
	supervisor uint32
	abort      uint32
	undefined  uint32
}

func decodeProcessorMode(value uint32) (processorMode, bool) {
	mode := processorMode(value & processorModeMask)
	switch mode {
	case processorModeUser, processorModeFIQ, processorModeIRQ,
		processorModeSupervisor, processorModeAbort,
		processorModeUndefined, processorModeSystem:
		return mode, true
	default:
		return processorModeSystem, false
	}
}

func (b *Backend) currentProcessorMode() processorMode {
	mode, _ := decodeProcessorMode(b.regs[cpu.RegisterCPSR])
	return mode
}

func (b *Backend) switchProcessorMode(oldMode, newMode processorMode) {
	if oldMode == newMode {
		return
	}
	// A resident instruction line records whether its permission check passed
	// in User or privileged mode. Drop the one-line execution window before the
	// privilege can change underneath it.
	b.invalidateInstructionWindow()
	// Native data entries encode the access check performed in the old
	// privileged/user mode, so they cannot survive a bank/mode switch.
	b.tlbClear()
	b.saveProcessorBank(oldMode)
	b.loadProcessorBank(newMode)
}

func (b *Backend) saveProcessorBank(mode processorMode) {
	if mode == processorModeFIQ {
		copy(b.banks.fiq[:], b.regs[cpu.RegisterR8:cpu.RegisterPC])
		return
	}
	copy(b.banks.userHigh[:], b.regs[cpu.RegisterR8:cpu.RegisterSP])
	bank := b.stackLinkBank(mode)
	copy(bank[:], b.regs[cpu.RegisterSP:cpu.RegisterPC])
}

func (b *Backend) loadProcessorBank(mode processorMode) {
	if mode == processorModeFIQ {
		copy(b.regs[cpu.RegisterR8:cpu.RegisterPC], b.banks.fiq[:])
		return
	}
	copy(b.regs[cpu.RegisterR8:cpu.RegisterSP], b.banks.userHigh[:])
	bank := b.stackLinkBank(mode)
	copy(b.regs[cpu.RegisterSP:cpu.RegisterPC], bank[:])
}

func (b *Backend) stackLinkBank(mode processorMode) *[2]uint32 {
	switch mode {
	case processorModeIRQ:
		return &b.banks.irq
	case processorModeSupervisor:
		return &b.banks.supervisor
	case processorModeAbort:
		return &b.banks.abort
	case processorModeUndefined:
		return &b.banks.undefined
	default:
		return &b.banks.userStackLink
	}
}

func (b *Backend) savedStatus(mode processorMode) *uint32 {
	switch mode {
	case processorModeFIQ:
		return &b.spsr.fiq
	case processorModeIRQ:
		return &b.spsr.irq
	case processorModeSupervisor:
		return &b.spsr.supervisor
	case processorModeAbort:
		return &b.spsr.abort
	case processorModeUndefined:
		return &b.spsr.undefined
	default:
		return nil
	}
}

// readUserRegister returns the unbanked User/System view while the CPU remains
// in its current privileged mode. ARM block transfers use this view when their
// S bit is set without loading the program counter.
func (b *Backend) readUserRegister(register uint32) uint32 {
	if register < cpu.RegisterR8 {
		return b.regs[register]
	}
	mode := b.currentProcessorMode()
	if register < cpu.RegisterSP {
		if mode == processorModeFIQ {
			return b.banks.userHigh[register-cpu.RegisterR8]
		}
		return b.regs[register]
	}
	if register < cpu.RegisterPC {
		if mode == processorModeUser || mode == processorModeSystem {
			return b.regs[register]
		}
		return b.banks.userStackLink[register-cpu.RegisterSP]
	}
	return b.regs[register]
}

func (b *Backend) writeUserRegister(register, value uint32) {
	if register < cpu.RegisterR8 {
		b.regs[register] = value
		return
	}
	mode := b.currentProcessorMode()
	if register < cpu.RegisterSP {
		if mode == processorModeFIQ {
			b.banks.userHigh[register-cpu.RegisterR8] = value
		} else {
			b.regs[register] = value
		}
		return
	}
	if register < cpu.RegisterPC {
		if mode == processorModeUser || mode == processorModeSystem {
			b.regs[register] = value
		} else {
			b.banks.userStackLink[register-cpu.RegisterSP] = value
		}
		return
	}
	b.regs[register] = value
}

func (b *Backend) readProgramStatus(saved bool) (uint32, error) {
	if !saved {
		b.resolveFlags()
		return b.regs[cpu.RegisterCPSR], nil
	}
	status := b.savedStatus(b.currentProcessorMode())
	if status == nil {
		return 0, fmt.Errorf("SPSR is unavailable in processor mode 0x%02x", b.currentProcessorMode())
	}
	return *status, nil
}

// programStatusWriteAllowed reports whether an MSR may retire. An application
// guest has no banked state of its own and reports an invalid CPSR mode field,
// so the architectural "ignored in User mode" masking cannot protect it: a
// control-byte write would be taken as privileged and bank its live stack
// pointer away. Restrict it to the condition flags, which no mode can change.
func (b *Backend) programStatusWriteAllowed(saved bool, fields uint32) bool {
	if b.systemBus != nil {
		return true
	}
	return !saved && fields&^0x8 == 0
}

func (b *Backend) writeProgramStatus(saved bool, fields uint32, value uint32) error {
	var mask uint32
	if fields&1 != 0 {
		mask |= 0x000000ff
	}
	if fields&2 != 0 {
		mask |= 0x0000ff00
	}
	if fields&4 != 0 {
		mask |= 0x00ff0000
	}
	if fields&8 != 0 {
		mask |= 0xff000000
	}
	if saved {
		status := b.savedStatus(b.currentProcessorMode())
		if status == nil {
			return fmt.Errorf("SPSR is unavailable in processor mode 0x%02x", b.currentProcessorMode())
		}
		*status = *status&^mask | value&mask
		return nil
	}

	b.resolveFlags()
	old := b.regs[cpu.RegisterCPSR]
	oldMode, oldValid := decodeProcessorMode(old)
	if !oldValid {
		oldMode = processorModeSystem
	}
	if oldMode == processorModeUser {
		mask &= 0xff000000
	}
	next := old&^mask | value&mask
	newMode, newValid := decodeProcessorMode(next)
	if mask&processorModeMask != 0 && !newValid {
		return fmt.Errorf("invalid CPSR processor mode 0x%02x", next&processorModeMask)
	}
	if newValid && oldMode != newMode {
		b.switchProcessorMode(oldMode, newMode)
	}
	b.regs[cpu.RegisterCPSR] = next
	b.flags.dirty = false
	if next&cpu.StatusThumb != 0 {
		b.mode = cpu.ModeThumb
	} else {
		b.mode = cpu.ModeARM
	}
	return nil
}
