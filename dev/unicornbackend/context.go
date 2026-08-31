package unicornbackend

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	contextMagic   = "UCBC"
	contextVersion = uint32(1)
	contextSize    = 8 + 17*4
)

func (b *Backend) SaveContext() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, cpu.ErrClosed
	}
	data := make([]byte, contextSize)
	copy(data, contextMagic)
	binary.LittleEndian.PutUint32(data[4:8], contextVersion)
	for id := uint32(0); id < 17; id++ {
		value, err := b.readRegisterLocked(id)
		if err != nil {
			return nil, err
		}
		binary.LittleEndian.PutUint32(data[8+id*4:], value)
	}
	return data, nil
}

func (b *Backend) RestoreContext(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if len(data) != contextSize || string(data[:4]) != contextMagic ||
		binary.LittleEndian.Uint32(data[4:8]) != contextVersion {
		return fmt.Errorf("Unicorn CPU context: %w", cpu.ErrInvalidAddress)
	}
	values := [17]uint32{}
	for id := range values {
		values[id] = binary.LittleEndian.Uint32(data[8+id*4:])
	}
	// Install CPSR before PC so Unicorn interprets the restored address in the
	// saved ARM/Thumb state. ARMv5 application state has no ITSTATE or vector
	// registers outside this public integer context.
	if err := b.writeRegisterLocked(cpu.RegisterCPSR, values[cpu.RegisterCPSR]); err != nil {
		return err
	}
	for id := uint32(0); id < cpu.RegisterCPSR; id++ {
		if err := b.writeRegisterLocked(id, values[id]); err != nil {
			return err
		}
	}
	b.stopped.Store(false)
	return nil
}
