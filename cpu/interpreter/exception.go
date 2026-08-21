package interpreter

import (
	"errors"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	statusFIQDisable = uint32(1 << 6)
	statusIRQDisable = uint32(1 << 7)

	vectorUndefined     = uint32(0x04)
	vectorSoftware      = uint32(0x08)
	vectorPrefetchAbort = uint32(0x0c)
	vectorDataAbort     = uint32(0x10)
	vectorIRQ           = uint32(0x18)
	vectorFIQ           = uint32(0x1c)
)

func (b *Backend) exceptionVector(offset uint32) uint32 {
	if b.cp15.control&(1<<13) != 0 {
		return 0xffff0000 + offset
	}
	return offset
}

func (b *Backend) enterException(mode processorMode, vector, returnLink uint32) {
	b.resolveFlags()
	oldStatus := b.regs[cpu.RegisterCPSR]
	oldMode, valid := decodeProcessorMode(oldStatus)
	if !valid {
		oldMode = processorModeSystem
	}
	if oldMode != mode {
		b.switchProcessorMode(oldMode, mode)
	}
	*b.savedStatus(mode) = oldStatus
	b.regs[cpu.RegisterLR] = returnLink

	nextStatus := oldStatus&^(processorModeMask|cpu.StatusThumb) |
		uint32(mode) | statusIRQDisable
	if mode == processorModeFIQ {
		nextStatus |= statusFIQDisable
	}
	b.regs[cpu.RegisterCPSR] = nextStatus
	b.regs[cpu.RegisterPC] = b.exceptionVector(vector)
	b.flags.dirty = false
	b.mode = cpu.ModeARM
}

func (b *Backend) takePendingInterrupt() bool {
	lines := b.interruptLines.Load()
	status := b.regs[cpu.RegisterCPSR]
	returnLink := b.regs[cpu.RegisterPC] + 4
	if lines&(uint32(1)<<uint32(cpu.InterruptFIQ)) != 0 && status&statusFIQDisable == 0 {
		b.enterException(processorModeFIQ, vectorFIQ, returnLink)
		return true
	}
	if lines&(uint32(1)<<uint32(cpu.InterruptIRQ)) != 0 && status&statusIRQDisable == 0 {
		b.enterException(processorModeIRQ, vectorIRQ, returnLink)
		return true
	}
	return false
}

func (b *Backend) handleMMUFault(err error, instructionAddress uint32) bool {
	var fault *MMUFault
	if !errors.As(err, &fault) {
		return false
	}
	if fault.Permission&cpu.PermissionExecute != 0 {
		b.enterException(processorModeAbort, vectorPrefetchAbort, instructionAddress+4)
	} else {
		b.enterException(processorModeAbort, vectorDataAbort, instructionAddress+8)
	}
	return true
}

func (b *Backend) handleCurrentMMUFault(err error) bool {
	var fault *MMUFault
	if !errors.As(err, &fault) {
		return false
	}
	instructionAddress := b.regs[cpu.RegisterPC]
	if fault.Permission&cpu.PermissionExecute == 0 {
		if b.mode == cpu.ModeThumb {
			instructionAddress -= 2
		} else {
			instructionAddress -= 4
		}
	}
	return b.handleMMUFault(err, instructionAddress)
}
