package interpreter

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	contextVersion           = 3
	bankedCP15ContextVersion = 2
	legacyContextVersion     = 1
	bankedContextWords       = 22
	spsrContextWords         = 5
	cp15ContextWords         = 7
)

func (b *Backend) SaveContext() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, cpu.ErrClosed
	}
	b.resolveFlags()
	lineAddresses := make([]uint32, 0, len(b.instructionCache))
	for address := range b.instructionCache {
		lineAddresses = append(lineAddresses, address)
	}
	sort.Slice(lineAddresses, func(left, right int) bool {
		return lineAddresses[left] < lineAddresses[right]
	})
	fixedWordCount := len(b.regs) + bankedContextWords + spsrContextWords + cp15ContextWords + 2
	data := make([]byte, 8+fixedWordCount*4+len(lineAddresses)*(4+int(instructionCacheLineSize)))
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
	binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(len(lineAddresses)))
	offset += 4
	for _, address := range lineAddresses {
		binary.LittleEndian.PutUint32(data[offset:offset+4], address)
		offset += 4
		line := b.instructionCache[address]
		copy(data[offset:offset+int(instructionCacheLineSize)], line[:])
		offset += int(instructionCacheLineSize)
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
	if len(data) < 8 || string(data[:4]) != "ARMC" {
		return fmt.Errorf("CPU context: %w", cpu.ErrInvalidAddress)
	}
	version := binary.LittleEndian.Uint32(data[4:8])
	legacySize := 8 + (len(b.regs)+1)*4
	bankedCP15Size := 8 + (len(b.regs)+bankedContextWords+spsrContextWords+cp15ContextWords+1)*4
	currentMinimumSize := bankedCP15Size + 4
	if version == legacyContextVersion && len(data) != legacySize ||
		version == bankedCP15ContextVersion && len(data) != bankedCP15Size ||
		version == contextVersion && len(data) < currentMinimumSize ||
		version != legacyContextVersion && version != bankedCP15ContextVersion &&
			version != contextVersion {
		return fmt.Errorf("CPU context: %w", cpu.ErrInvalidAddress)
	}

	var (
		restoredRegs             [17]uint32
		restoredBanks            bankedRegisters
		restoredSPSR             savedProgramStatus
		restoredCP15             cp15State
		restoredInstructionCache map[uint32]instructionCacheLine
	)
	offset := 8
	readWords := func(values []uint32) {
		for index := range values {
			values[index] = binary.LittleEndian.Uint32(data[offset : offset+4])
			offset += 4
		}
	}
	readWords(restoredRegs[:])
	if version == bankedCP15ContextVersion || version == contextVersion {
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
	if version == contextVersion {
		lineCount := binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4
		expectedSize := uint64(currentMinimumSize) +
			uint64(lineCount)*(4+uint64(instructionCacheLineSize))
		if lineCount > maximumInstructionCacheLines || expectedSize != uint64(len(data)) {
			return fmt.Errorf("CPU instruction cache context: %w", cpu.ErrInvalidAddress)
		}
		if lineCount != 0 {
			restoredInstructionCache = make(map[uint32]instructionCacheLine, lineCount)
		}
		for range lineCount {
			address := binary.LittleEndian.Uint32(data[offset : offset+4])
			offset += 4
			if address&(instructionCacheLineSize-1) != 0 {
				return fmt.Errorf("CPU instruction cache address: %w", cpu.ErrInvalidAddress)
			}
			if _, duplicate := restoredInstructionCache[address]; duplicate {
				return fmt.Errorf("CPU instruction cache duplicate: %w", cpu.ErrInvalidAddress)
			}
			var line instructionCacheLine
			copy(line[:], data[offset:offset+int(instructionCacheLineSize)])
			offset += int(instructionCacheLineSize)
			restoredInstructionCache[address] = line
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
	b.instructionCache = restoredInstructionCache
	b.instructionCacheHotValid = false
	b.invalidateTLB()
	b.executeData = nil
	b.dataData = nil
	b.flags.dirty = false
	b.mode = mode
	b.setModeFlag()
	return nil
}
