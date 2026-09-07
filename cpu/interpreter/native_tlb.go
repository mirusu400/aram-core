package interpreter

// Software translation-lookaside buffer for the native machine-code JIT.
//
// The native emitters translate a guest load/store into a handful of host
// instructions instead of a call back into Go: probe a direct-mapped table with
// the guest page number, and on a hit access host memory at
// entry.host + (address & 0x3ff). Without this every memory access bails to the
// interpreter, which is why the native backend used to lose to the interpreter
// on the guest software blitters that dominate a heavy frame.
//
// The table points straight into the interpreter's own region.data, so there is
// no shadow arena and no coherence problem: a host-side WriteMemory, a service
// writing guest memory, and a native store all touch the same bytes. Entries
// are filled by the interpreter (tlbNote, called from memory.go after a
// successful access), so a native miss bails, the interpreter runs that one
// instruction and installs the page, and the next native access hits.
//
// Two half-tables keep a blitter's source and destination from evicting each
// other and keep permissions out of the emitted code: the read half is filled
// only for readable pages, the write half only for writable pages that do NOT
// overlap the translated-code span. That exclusion is what preserves
// self-modifying-code detection - a store the native code performs inline never
// reaches smcInvalidate, so pages holding translated code must never be
// reachable inline. tlbClearWrite drops the write half whenever the code span
// grows, and translation only ever happens between block executions.

import (
	"unsafe"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	// nativeTLBEntries is the number of pages cached per half-table. This is a
	// conflict budget rather than a capacity one: with 256 entries the hottest
	// loop in a real title evicted itself, because its literal-pool load and
	// its data load fell in the same set and knocked each other out about
	// 47000 times per 120 frames each. Sizing it to cover 16 MiB of distinct
	// 1 KiB subpages made that collision rare, but not gone: a handset image
	// is laid out at 16 MiB-multiple distances - 판타지포에버3's lookup table
	// at 0x1002ec00 and the code page at 0x0002ec00 its literal loads read,
	// its stack top at 0x7ffff400 and the image page at 0x00fff400 - so its
	// hottest load and push each evicted the other on every pass, and the
	// bail heuristic then retired both to the interpreter for good. The
	// table is therefore two-way set-associative: a set holds two pages, so
	// a pair of aliases stays resident.
	nativeTLBEntries = 16384
	nativeTLBMask    = nativeTLBEntries - 1
	// tlbWays is the associativity. The emitted probe compares both tags of a
	// set and picks the matching entry; the second way costs one compare and
	// one branch on the hit path.
	tlbWays          = 2
	nativeTLBSets    = nativeTLBEntries / tlbWays
	nativeTLBSetMask = nativeTLBSets - 1
	// tlbSetBytes is the stride of one set in the table, spelled out because
	// the emitters index with it as a shift.
	tlbSetBytes = tlbWays * tlbEntryBytes
	// tlbEntryBytes is the fixed stride the emitted code indexes with. It is a
	// power of two so the index is a shift, and it is spelled out here because
	// the machine-code emitters encode it as a constant.
	tlbEntryBytes = 16
	// tlbWriteOffset is the byte offset of the write half inside the table.
	tlbWriteOffset = nativeTLBEntries * tlbEntryBytes
	// ARM926 permissions and the Go virtual-data cache are both 1 KiB-granular.
	// Matching them avoids rejecting a hot subpage just because a neighbouring
	// quarter of its host 4 KiB page is mapped differently.
	tlbPageBits = 10
	tlbPageSize = 1 << tlbPageBits
	// tlbEmptyTag can never be a real page number (a 32-bit guest address
	// shifts down to at most 0x003fffff), so a zeroed table is all misses only
	// once page 0 is excluded; tlbClear writes this tag explicitly instead.
	tlbEmptyTag = ^uint32(0)
)

// tlbEntry is one cached page. Layout is fixed: the emitted code loads tag at
// offset 0 and host at offset 8, with a stride of tlbEntryBytes; a set is
// tlbWays consecutive entries. host is a raw host address into region.data,
// kept alive by b.regions; Go's heap does not move objects, and the table is
// dropped on Map and Close. victim is the padding that keeps the stride, used
// on a set's first entry to remember which way to replace next; the emitted
// code never reads it.
type tlbEntry struct {
	tag    uint32
	victim uint32
	host   uint64
}

// newNativeTLB allocates the two half-tables as one slice (read half first,
// write half at tlbWriteOffset) and marks every entry empty.
func newNativeTLB() []tlbEntry {
	table := make([]tlbEntry, 2*nativeTLBEntries)
	for i := range table {
		table[i].tag = tlbEmptyTag
	}
	return table
}

// tlbBase is the host address the emitters bake into translated blocks. The
// slice is allocated once per backend and never reallocated, so the address is
// stable for the lifetime of every block that embeds it.
func (b *Backend) tlbBase() uintptr {
	if len(b.tlb) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&b.tlb[0]))
}

// tlbClear empties both halves. It runs wherever the region table itself
// changes (Map, Close), since the cached host pointers describe the old
// mapping.
func (b *Backend) tlbClear() {
	for i := range b.tlb {
		b.tlb[i].tag = tlbEmptyTag
	}
}

// tlbClearWrite empties the write half. It runs when the translated-code span
// grows, so a page that has just become code can no longer be written by
// inline native code without going through smcInvalidate.
func (b *Backend) tlbClearWrite() {
	for i := nativeTLBEntries; i < len(b.tlb); i++ {
		b.tlb[i].tag = tlbEmptyTag
	}
}

func (b *Backend) tlbHit(address uint32, permission cpu.Permissions) bool {
	if len(b.tlb) == 0 {
		return false
	}
	page := address >> tlbPageBits
	first := (page & nativeTLBSetMask) * tlbWays
	if permission&cpu.PermissionWrite != 0 {
		first += nativeTLBEntries
	}
	for way := uint32(0); way < tlbWays; way++ {
		if b.tlb[first+way].tag == page {
			return true
		}
	}
	return false
}

// tlbPlace installs page into the set it belongs to in the half-table that
// starts at entry half. A way already holding the page is refreshed, an empty
// way is taken, and otherwise the ways are replaced in turn - first-in,
// first-out - so two pages that share a set stay resident together while a
// third still cycles through.
func (b *Backend) tlbPlace(half, page uint32, host uint64) {
	first := half + (page&nativeTLBSetMask)*tlbWays
	set := b.tlb[first : first+tlbWays]
	for way := range set {
		if set[way].tag == page {
			set[way].host = host
			return
		}
	}
	for way := range set {
		if set[way].tag == tlbEmptyTag {
			set[way].tag, set[way].host = page, host
			return
		}
	}
	way := set[0].victim % tlbWays
	set[way].tag, set[way].host = page, host
	set[0].victim = way + 1
}

// tlbNote installs the page containing address, given the region that satisfied
// the access (its first byte is guest address regionAddr). It is called from the
// interpreter's memory path, which is where a native bail lands, so the page a
// native block missed on is resident by the time it retries.
//
// A page is installed only when it lies wholly inside the region: that single
// test is what lets the emitted code skip both the region lookup and the region
// bounds check and keep only a page-crossing check.
func (b *Backend) tlbNote(address, regionAddr uint32, data []byte, perms cpu.Permissions) {
	page := address >> tlbPageBits
	start := uint64(page) << tlbPageBits
	base := uint64(regionAddr)
	if start < base || start+tlbPageSize > base+uint64(len(data)) {
		return
	}
	b.tlbInstall(page, uint64(uintptr(unsafe.Pointer(&data[start-base]))), perms)
}

// tlbNoteMapped installs a virtual page whose bytes live at physicalStart in a
// direct system-RAM slice. Unlike tlbNote, the virtual and backing addresses
// may differ; the emitted tag is always virtual while host points at the first
// byte to which that virtual page translates.
func (b *Backend) tlbNoteMapped(
	virtualStart, physicalStart, regionAddr uint32,
	data []byte,
	perms cpu.Permissions,
) {
	if virtualStart&(tlbPageSize-1) != 0 {
		return
	}
	start := uint64(physicalStart)
	base := uint64(regionAddr)
	if start < base || start+tlbPageSize > base+uint64(len(data)) {
		return
	}
	b.tlbInstall(
		virtualStart>>tlbPageBits,
		uint64(uintptr(unsafe.Pointer(&data[start-base]))),
		perms,
	)
}

func (b *Backend) tlbInstall(page uint32, host uint64, perms cpu.Permissions) {
	if perms&cpu.PermissionRead != 0 {
		b.tlbPlace(0, page, host)
	}
	// A writable page that overlaps translated code stays off the inline path:
	// stores there must reach smcInvalidate through the interpreter.
	virtualStart := page << tlbPageBits
	if perms&cpu.PermissionWrite != 0 && !b.hasCodePages(virtualStart, tlbPageSize) &&
		!b.hasJITCodePages(virtualStart, tlbPageSize) {
		b.tlbPlace(nativeTLBEntries, page, host)
	}
}
