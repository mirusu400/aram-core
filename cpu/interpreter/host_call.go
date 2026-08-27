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
