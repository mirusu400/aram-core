package interpreter

import "github.com/mirusu400/aram-core/cpu"

const (
	instructionCacheLineSize = uint32(32)
	// ARM926EJ-S implementations expose at most 128 KiB per cache. This
	// functional VIVT shadow intentionally does not model replacement timing,
	// but the safety bound exceeds the maximum number of resident hardware
	// lines by a generous factor while keeping serialized state bounded.
	maximumInstructionCacheLines = uint32(1 << 20)
)

type instructionCacheLine [instructionCacheLineSize]byte

// InstructionCacheLine returns a copy of the functional ARM926 instruction
// cache line containing address. It is host diagnostics and does not fill or
// otherwise mutate the guest cache.
func (b *Backend) InstructionCacheLine(address uint32) (uint32, []byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	mvaLine := b.modifiedVirtualAddress(address) &^ (instructionCacheLineSize - 1)
	line, ok := b.instructionCache[mvaLine]
	if !ok && b.instructionCacheHotValid && b.instructionCacheHotMVA == mvaLine {
		line, ok = b.instructionCacheHot, true
	}
	if !ok {
		return mvaLine, nil, false
	}
	return mvaLine, append([]byte(nil), line[:]...), true
}

func (b *Backend) instructionCacheEnabled() bool {
	return b.cp15.control&(1<<12) != 0
}

func (b *Backend) modifiedVirtualAddress(address uint32) uint32 {
	if address < 0x02000000 {
		return address | b.cp15.processID&0xfe000000
	}
	return address
}

func (b *Backend) fetchInstructionCache(address uint32, size uint32) ([]byte, error) {
	line, cacheable, err := b.loadInstructionCacheLine(address)
	if err != nil {
		return nil, err
	}
	if !cacheable {
		physical, _, translationErr := b.translateAddressWithAttributes(
			address,
			cpu.PermissionExecute,
		)
		if translationErr != nil {
			return nil, translationErr
		}
		data := make([]byte, size)
		if err := b.copyOut(physical, data, cpu.PermissionExecute); err != nil {
			return nil, err
		}
		return data, nil
	}

	mvaLine := b.modifiedVirtualAddress(address) &^ (instructionCacheLineSize - 1)
	offset := address & (instructionCacheLineSize - 1)
	if offset+size > instructionCacheLineSize {
		panic("instruction fetch crosses an ARM926 cache line")
	}
	b.instructionCacheHot = line
	b.instructionCacheHotMVA = mvaLine
	b.instructionCacheHotValid = true
	return b.instructionCacheHot[offset : offset+size], nil
}

// loadInstructionCacheLine performs the ARM926 I-cache lookup and, for a
// cacheable miss, fills the complete 32-byte line. It is shared by normal
// instruction fetches and the CP15 prefetch-I-cache-line operation.
func (b *Backend) loadInstructionCacheLine(address uint32) (instructionCacheLine, bool, error) {
	_, translation, err := b.translateAddressWithAttributes(address, cpu.PermissionExecute)
	if err != nil {
		return instructionCacheLine{}, false, err
	}
	if !translation.cacheable {
		return instructionCacheLine{}, false, nil
	}

	mvaLine := b.modifiedVirtualAddress(address) &^ (instructionCacheLineSize - 1)
	if b.instructionCacheHotValid && b.instructionCacheHotMVA == mvaLine {
		return b.instructionCacheHot, true, nil
	}
	line, ok := b.instructionCache[mvaLine]
	if ok {
		return line, true, nil
	}
	virtualLine := address &^ (instructionCacheLineSize - 1)
	if err := b.readVirtual(virtualLine, line[:], cpu.PermissionExecute); err != nil {
		return instructionCacheLine{}, false, err
	}
	if uint32(len(b.instructionCache)) < maximumInstructionCacheLines {
		if b.instructionCache == nil {
			b.instructionCache = make(map[uint32]instructionCacheLine)
		}
		b.instructionCache[mvaLine] = line
	}
	return line, true, nil
}

func (b *Backend) prefetchInstructionCacheLine(address uint32) error {
	if !b.instructionCacheEnabled() {
		return nil
	}
	_, _, err := b.loadInstructionCacheLine(address)
	return err
}

func (b *Backend) invalidateInstructionCache() {
	b.instructionCache = nil
	b.instructionCacheHotValid = false
}

func (b *Backend) invalidateInstructionCacheMVA(address uint32) {
	mvaLine := b.modifiedVirtualAddress(address) &^ (instructionCacheLineSize - 1)
	delete(b.instructionCache, mvaLine)
	if b.instructionCacheHotValid && b.instructionCacheHotMVA == mvaLine {
		b.instructionCacheHotValid = false
	}
}
