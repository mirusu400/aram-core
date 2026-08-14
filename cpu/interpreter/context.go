package interpreter

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

func (b *Backend) SaveContext() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, cpu.ErrClosed
	}
	data := make([]byte, 4+4+len(b.regs)*4+4)
	copy(data, "ARMC")
	binary.LittleEndian.PutUint32(data[4:8], 1)
	offset := 8
	for _, value := range b.regs {
		binary.LittleEndian.PutUint32(data[offset:offset+4], value)
		offset += 4
	}
	binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(b.mode))
	return data, nil
}

func (b *Backend) RestoreContext(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	expected := 4 + 4 + len(b.regs)*4 + 4
	if len(data) != expected || string(data[:4]) != "ARMC" ||
		binary.LittleEndian.Uint32(data[4:8]) != 1 {
		return fmt.Errorf("CPU context: %w", cpu.ErrInvalidAddress)
	}
	var restored [17]uint32
	offset := 8
	for index := range restored {
		restored[index] = binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4
	}
	mode := cpu.Mode(binary.LittleEndian.Uint32(data[offset : offset+4]))
	if !mode.Valid() {
		return fmt.Errorf("CPU context mode: %w", cpu.ErrInvalidAddress)
	}
	b.regs = restored
	b.mode = mode
	b.setModeFlag()
	return nil
}
