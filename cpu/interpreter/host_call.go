package interpreter

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

func (b *Backend) CaptureHostCallFrame(
	destination *cpu.HostCallFrame,
	request cpu.HostCallFrameRequest,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if destination == nil || request.StackWords > cpu.MaxHostCallWords ||
		request.ParameterWords > cpu.MaxHostCallWords ||
		request.ParameterWords != 0 && request.ParameterAddress == 0 {
		return fmt.Errorf("host-call frame request: %w", cpu.ErrInvalidAddress)
	}
	b.resolveFlags()
	destination.Registers = b.regs
	destination.StackWords = request.StackWords
	destination.ParameterWords = request.ParameterWords
	if err := b.captureHostCallWords(
		b.regs[cpu.RegisterSP],
		destination.Stack[:request.StackWords],
	); err != nil {
		return err
	}
	if err := b.captureHostCallWords(
		request.ParameterAddress,
		destination.Parameters[:request.ParameterWords],
	); err != nil {
		return err
	}
	b.executionStatistics.HostFrameCaptures++
	return nil
}

func (b *Backend) captureHostCallWords(address uint32, words []uint32) error {
	if len(words) == 0 {
		return nil
	}
	byteCount := len(words) * 4
	if uint64(address)+uint64(byteCount) > 1<<32 {
		return cpu.ErrInvalidAddress
	}
	data := b.hostCallScratch[:byteCount]
	var err error
	if b.mmuEnabled() {
		err = b.readVirtual(address, data, cpu.PermissionRead)
	} else {
		err = b.copyOut(address, data, cpu.PermissionRead)
	}
	if err != nil {
		return err
	}
	for index := range words {
		words[index] = binary.LittleEndian.Uint32(data[index*4 : index*4+4])
	}
	return nil
}

func (b *Backend) CommitHostCallRegisters(commit cpu.RegisterCommit) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if commit.Mask>>17 != 0 {
		return fmt.Errorf("host-call register commit: %w", cpu.ErrInvalidAddress)
	}
	for register := uint32(0); register < 17; register++ {
		if commit.Mask&(1<<register) == 0 {
			continue
		}
		if err := b.writeRegisterLocked(register, commit.Values[register]); err != nil {
			return err
		}
	}
	b.executionStatistics.HostRegisterCommits++
	return nil
}

// ReturnHostCallFrame keeps the capture and the ordered register writes under
// one lock. The only CPSR change is the instruction-set bit: the processor
// mode and register banks are preserved, so no mode decode or TLB flush is needed.
func (b *Backend) ReturnHostCallFrame(destination *cpu.HostCallFrame, low, high uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if destination == nil {
		return fmt.Errorf("host-call frame request: %w", cpu.ErrInvalidAddress)
	}
	b.resolveFlags()
	destination.Registers = b.regs
	destination.StackWords = 0
	destination.ParameterWords = 0
	b.executionStatistics.HostFrameCaptures++
	lr := destination.Registers[cpu.RegisterLR]
	cpsr := destination.Registers[cpu.RegisterCPSR]
	if lr&1 != 0 {
		cpsr |= cpu.StatusThumb
	} else {
		cpsr &^= cpu.StatusThumb
	}
	b.regs[cpu.RegisterR0] = low
	b.regs[cpu.RegisterR1] = high
	b.regs[cpu.RegisterPC] = lr &^ 1
	b.regs[cpu.RegisterCPSR] = cpsr
	if cpsr&cpu.StatusThumb != 0 {
		b.mode = cpu.ModeThumb
	} else {
		b.mode = cpu.ModeARM
	}
	b.invalidateInstructionWindow()
	b.executionStatistics.HostRegisterCommits++
	b.executionStatistics.HostCallReturnBatches++
	return nil
}
