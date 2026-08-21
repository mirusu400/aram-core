package interpreter

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	contextVersion       = 2
	legacyContextVersion = 1
	bankedContextWords   = 22
	spsrContextWords     = 5
	cp15ContextWords     = 7
)

func (b *Backend) SaveContext() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, cpu.ErrClosed
	}
	b.resolveFlags()
	wordCount := len(b.regs) + bankedContextWords + spsrContextWords + cp15ContextWords + 1
	data := make([]byte, 8+wordCount*4)
	copy(data, "ARMC")
	binary.LittleEndian.PutUint32(data[4:8], contextVersion)
	offset := 8
	putWords := func(values []uint32) {
		for _, value := range values {
			binary.LittleEndian.PutUint32(data[offset:offset+4], value)
			offset += 4
		}
	}
	putWords(b.regs[:])
	putWords(b.banks.userHigh[:])
	putWords(b.banks.userStackLink[:])
	putWords(b.banks.fiq[:])
	putWords(b.banks.irq[:])
	putWords(b.banks.supervisor[:])
	putWords(b.banks.abort[:])
	putWords(b.banks.undefined[:])
	putWords([]uint32{b.spsr.fiq, b.spsr.irq, b.spsr.supervisor, b.spsr.abort, b.spsr.undefined})
	putWords([]uint32{
		b.cp15.control,
		b.cp15.translationTableBase,
		b.cp15.domainAccessControl,
		b.cp15.dataFaultStatus,
		b.cp15.instructionFaultStatus,
		b.cp15.faultAddress,
		b.cp15.processID,
	})
	binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(b.mode))
	return data, nil
}

func (b *Backend) RestoreContext(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if len(data) < 8 || string(data[:4]) != "ARMC" {
		return fmt.Errorf("CPU context: %w", cpu.ErrInvalidAddress)
	}
	version := binary.LittleEndian.Uint32(data[4:8])
	legacySize := 8 + (len(b.regs)+1)*4
	currentSize := 8 + (len(b.regs)+bankedContextWords+spsrContextWords+cp15ContextWords+1)*4
	if version == legacyContextVersion && len(data) != legacySize ||
		version == contextVersion && len(data) != currentSize ||
		version != legacyContextVersion && version != contextVersion {
		return fmt.Errorf("CPU context: %w", cpu.ErrInvalidAddress)
	}

	var (
		restoredRegs  [17]uint32
		restoredBanks bankedRegisters
		restoredSPSR  savedProgramStatus
		restoredCP15  cp15State
	)
	offset := 8
	readWords := func(values []uint32) {
		for index := range values {
			values[index] = binary.LittleEndian.Uint32(data[offset : offset+4])
			offset += 4
		}
	}
	readWords(restoredRegs[:])
	if version == contextVersion {
		readWords(restoredBanks.userHigh[:])
		readWords(restoredBanks.userStackLink[:])
		readWords(restoredBanks.fiq[:])
		readWords(restoredBanks.irq[:])
		readWords(restoredBanks.supervisor[:])
		readWords(restoredBanks.abort[:])
		readWords(restoredBanks.undefined[:])
		statuses := []*uint32{
			&restoredSPSR.fiq,
			&restoredSPSR.irq,
			&restoredSPSR.supervisor,
			&restoredSPSR.abort,
			&restoredSPSR.undefined,
		}
		for _, status := range statuses {
			*status = binary.LittleEndian.Uint32(data[offset : offset+4])
			offset += 4
		}
		cp15Registers := []*uint32{
			&restoredCP15.control,
			&restoredCP15.translationTableBase,
			&restoredCP15.domainAccessControl,
			&restoredCP15.dataFaultStatus,
			&restoredCP15.instructionFaultStatus,
			&restoredCP15.faultAddress,
			&restoredCP15.processID,
		}
		for _, register := range cp15Registers {
			*register = binary.LittleEndian.Uint32(data[offset : offset+4])
			offset += 4
		}
	}
	mode := cpu.Mode(binary.LittleEndian.Uint32(data[offset : offset+4]))
	if !mode.Valid() {
		return fmt.Errorf("CPU context mode: %w", cpu.ErrInvalidAddress)
	}
	b.regs = restoredRegs
	b.banks = restoredBanks
	b.spsr = restoredSPSR
	b.cp15 = restoredCP15
	b.flags.dirty = false
	b.mode = mode
	b.setModeFlag()
	return nil
}
