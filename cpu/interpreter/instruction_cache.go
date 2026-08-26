package interpreter

import (
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	instructionCacheLineSize  = uint32(32)
	instructionCacheLineShift = 5
	// instructionCacheSets holds the functional ARM926 VIVT shadow. Real
	// implementations expose at most 128 KiB per cache; 2048 lines is 64 KiB,
	// which covers the resident code of the firmware phases measured here while
	// keeping the table itself cache-resident. It is direct-mapped: replacement
	// timing is not modelled, so associativity would only cost lookups.
	instructionCacheSets = 2048
	// maximumInstructionCacheLines bounds a restored context. It stays at the
	// historical value so a snapshot written before the table replaced the map
	// still deserializes; anything beyond a set simply replaces it.
	maximumInstructionCacheLines = uint32(1 << 20)
)

type instructionCacheLine [instructionCacheLineSize]byte

// instructionCacheEntry is one direct-mapped line. gen ties it to the mapping
// generation it was filled under, and privileged records the CPU privilege its
// permission check passed at: a line stays usable without re-walking the page
// table only while both still hold. See loadInstructionCacheLine.
type instructionCacheEntry struct {
	line       instructionCacheLine
	tag        uint32
	gen        uint32
	privileged bool
	valid      bool
}

// InstructionCacheLine returns a copy of the functional ARM926 instruction
// cache line containing address. It is host diagnostics and does not fill or
// otherwise mutate the guest cache.
func (b *Backend) InstructionCacheLine(address uint32) (uint32, []byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	mvaLine := b.modifiedVirtualAddress(address) &^ (instructionCacheLineSize - 1)
	entry := b.instructionCacheEntry(mvaLine)
	if entry == nil || !entry.valid || entry.gen != b.mappingGen || entry.tag != mvaLine {
		return mvaLine, nil, false
	}
	return mvaLine, append([]byte(nil), entry.line[:]...), true
}

func (b *Backend) instructionCacheEntry(mvaLine uint32) *instructionCacheEntry {
	if b.instructionCacheTable == nil {
		return nil
	}
	return &b.instructionCacheTable[(mvaLine>>instructionCacheLineShift)&(instructionCacheSets-1)]
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

// currentlyPrivileged reports whether the CPU is outside User mode. An invalid
// mode field is privileged, matching decodeProcessorMode's fallback, so this
// agrees with the permission check it stands in for.
func (b *Backend) currentlyPrivileged() bool {
	return b.regs[cpu.RegisterCPSR]&processorModeMask != uint32(processorModeUser)
}

// loadInstructionCacheLine returns the resident line for address, filling it
// through the MMU on a miss.
//
// A hit deliberately skips address translation. Everything the permission check
// depends on - the translation itself, the domain access control, the system
// and ROM protection bits - is covered by the mapping generation, and the one
// input that changes without a flush, the CPU privilege, is compared here. That
// leaves a hit at a tag compare, where it used to walk the page table for every
// instruction the guest retired: translation was a quarter of whole-phone run
// time before this.
func (b *Backend) loadInstructionCacheLine(address uint32) (*instructionCacheLine, bool, error) {
	mvaLine := b.modifiedVirtualAddress(address) &^ (instructionCacheLineSize - 1)
	privileged := b.currentlyPrivileged()
	if entry := b.instructionCacheEntry(mvaLine); entry != nil &&
		entry.valid && entry.gen == b.mappingGen &&
		entry.tag == mvaLine && entry.privileged == privileged {
		return &entry.line, true, nil
	}
	return b.fillInstructionCacheLine(address, mvaLine, privileged)
}

// invalidateInstructionWindow drops only the execution loop's current-line
// pointer. The functional cache remains resident; the next fetch performs the
// full lookup once and then reopens the window.
func (b *Backend) invalidateInstructionWindow() {
	b.instructionWindow = nil
	b.instructionWindowTag = 0
}

func (b *Backend) fillInstructionCacheLine(
	address, mvaLine uint32,
	privileged bool,
) (*instructionCacheLine, bool, error) {
	_, translation, err := b.translateAddressWithAttributes(address, cpu.PermissionExecute)
	if err != nil {
		return nil, false, err
	}
	if !translation.cacheable {
		return nil, false, nil
	}
	if b.instructionCacheTable == nil {
		b.instructionCacheTable = new([instructionCacheSets]instructionCacheEntry)
	}
	entry := b.instructionCacheEntry(mvaLine)
	// A prefetch or conflicting miss can replace the table entry currently held
	// by the execution window. Retire that pointer before overwriting the entry.
	b.invalidateInstructionWindow()
	virtualLine := address &^ (instructionCacheLineSize - 1)
	if err := b.readVirtual(virtualLine, entry.line[:], cpu.PermissionExecute); err != nil {
		entry.valid = false
		return nil, false, err
	}
	entry.tag, entry.gen, entry.privileged, entry.valid = mvaLine, b.mappingGen, privileged, true
	return &entry.line, true, nil
}

func (b *Backend) fetchInstructionCache(address uint32, size uint32) ([]byte, error) {
	// Zero is the cold sentinel; adding one is safe because a 32-bit byte
	// address has only 27 line-number bits. Keeping the sentinel in the tag lets
	// the hot path be a single equality comparison.
	windowTag := (address >> instructionCacheLineShift) + 1
	offset := address & (instructionCacheLineSize - 1)
	if offset+size > instructionCacheLineSize {
		// Aligned ARM and Thumb fetches cannot straddle a 32-byte line, so
		// reaching this means the guest set an unaligned PC. That is its
		// mistake to be told about, not the host's to die on.
		return nil, fmt.Errorf(
			"instruction fetch at 0x%08x crosses an ARM926 cache line: %w",
			address, cpu.ErrInvalidAddress,
		)
	}
	if b.instructionWindowTag == windowTag {
		return b.instructionWindow[offset : offset+size], nil
	}

	line, cacheable, err := b.loadInstructionCacheLine(address)
	if err != nil {
		return nil, err
	}
	if !cacheable {
		b.invalidateInstructionWindow()
		physical, _, translationErr := b.translateAddressWithAttributes(
			address,
			cpu.PermissionExecute,
		)
		if translationErr != nil {
			return nil, translationErr
		}
		data := b.readScratch[:size]
		if err := b.copyOut(physical, data, cpu.PermissionExecute); err != nil {
			return nil, err
		}
		return data, nil
	}
	b.instructionWindow = line
	b.instructionWindowTag = windowTag
	return line[offset : offset+size], nil
}

// invalidateInstructionCache drops every resident line by advancing the shared
// mapping generation, so a full flush costs nothing per line.
func (b *Backend) invalidateInstructionCache() {
	b.mappingGen++
	b.invalidateInstructionWindow()
}

func (b *Backend) invalidateInstructionCacheMVA(address uint32) {
	b.invalidateInstructionWindow()
	mvaLine := b.modifiedVirtualAddress(address) &^ (instructionCacheLineSize - 1)
	if entry := b.instructionCacheEntry(mvaLine); entry != nil && entry.tag == mvaLine {
		entry.valid = false
	}
	b.invalidateTranslationRange(address&^(instructionCacheLineSize-1), instructionCacheLineSize)
}

// prefetchInstructionCacheLine services CP15 c7,c13,1. ARM926 performs an
// I-cache lookup and fills the line on a cacheable miss, which guest code
// relies on when it preserves instructions before reusing their RAM.
func (b *Backend) prefetchInstructionCacheLine(address uint32) error {
	if !b.instructionCacheEnabled() {
		return nil
	}
	_, _, err := b.loadInstructionCacheLine(address)
	return err
}

// residentInstructionCacheLines returns the resident lines in address order.
// Only context serialization needs it, so it is not on any execution path.
func (b *Backend) residentInstructionCacheLines() []uint32 {
	if b.instructionCacheTable == nil {
		return nil
	}
	addresses := make([]uint32, 0, instructionCacheSets)
	for index := range b.instructionCacheTable {
		entry := &b.instructionCacheTable[index]
		if entry.valid && entry.gen == b.mappingGen {
			addresses = append(addresses, entry.tag)
		}
	}
	return addresses
}

// restoreInstructionCacheLine reinstalls one serialized line. The privilege it
// was validated under is not part of the format; the restored CPSR names the
// same one, because both come from the same snapshot.
func (b *Backend) restoreInstructionCacheLine(mvaLine uint32, line instructionCacheLine) {
	if b.instructionCacheTable == nil {
		b.instructionCacheTable = new([instructionCacheSets]instructionCacheEntry)
	}
	b.invalidateInstructionWindow()
	entry := b.instructionCacheEntry(mvaLine)
	entry.line = line
	entry.tag, entry.gen, entry.privileged, entry.valid =
		mvaLine, b.mappingGen, b.currentlyPrivileged(), true
}
