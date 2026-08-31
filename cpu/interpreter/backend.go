// Package interpreter provides ARAM's portable, pure-Go ARM/Thumb CPU
// fallback. The initial implementation deliberately faults on instructions it
// does not understand instead of silently treating them as no-ops.
package interpreter

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	flagN uint32 = 1 << 31
	flagZ uint32 = 1 << 30
	flagC uint32 = 1 << 29
	flagV uint32 = 1 << 28
	flagT        = cpu.StatusThumb
)

const (
	BackendName        = "portable-interpreter"
	BackendVersion     = "1"
	DefaultMemoryLimit = uint64(512 << 20)
)

// runBatchInstructions bounds how many guest instructions a single batch
// executes before Run re-polls host cancellation. It matches the previous
// per-256-instruction poll cadence so interruption latency is unchanged.
const runBatchInstructions = 256

type region struct {
	address     uint32
	size        uint32
	permissions cpu.Permissions
	data        []byte
}

// PCRegisterCapture is a diagnostic snapshot taken immediately before the
// instruction at Address executes. Registers contains r0-r15 followed by the
// stored CPSR value; it deliberately does not alter execution or save state.
type PCRegisterCapture struct {
	Address   uint32
	Registers [17]uint32
}

// dataRegionCache is one entry of the per-permission data fast path. Empty data
// means the slot is cold.
type dataRegionCache struct {
	address uint32
	perms   cpu.Permissions
	data    []byte
}

const virtualDataCacheEntries = 4096

// Guest virtual pages use only 22 bits at the cache's 1 KiB granularity. The
// high bit distinguishes a populated slot from a zero-value entry without an
// extra pointer comparison on every load and store.
const virtualDataValid = uint32(1 << 31)

const (
	directDataMissCacheEntries = 256
	directDataMissValid        = uint32(1 << 31)
)

// virtualDataCacheEntry is the Go execution tiers' 1 KiB direct-RAM window.
// ARM926 permissions are selected per 1 KiB subpage, so this granularity lets
// a hit skip address translation and the physical bus without speculating
// across an AP boundary.
type virtualDataCacheEntry struct {
	data        []byte
	virtualPage uint32 // page | virtualDataValid
	gen         uint32
	privileged  bool
}

type virtualDataCache struct {
	read  [virtualDataCacheEntries]virtualDataCacheEntry
	write [virtualDataCacheEntries]virtualDataCacheEntry
}

// Backend is a bounds-checked ARMv5TE interpreter. It currently implements
// the ARM/Thumb control-flow and integer instructions needed by the first
// application-entry milestone; unsupported encodings produce a precise fault.
type Backend struct {
	mu             sync.Mutex
	regions        []region
	regionHints    [8]int
	executeAddress uint32
	executeData    []byte
	// dataCache caches the most recently accessed data region per access
	// permission, so a routine that reads one region and writes another - a
	// software blitter reading source pixels and writing the framebuffer, the
	// dominant cost of a heavy frame - keeps BOTH on the fast path instead of
	// thrashing one slot and paying findRegion on every access. Indexed by the
	// access permission bit (Read=1, Write=2, Execute=4). Each entry is a value
	// copy of the region's slice/address/permissions and stays valid across
	// region re-sorts (regions never overlap and their backing arrays are
	// stable); it is invalidated wherever executeData is.
	dataCache [8]dataRegionCache
	// directDataCacheAlt retains the previous whole-system RAM region for each
	// permission. MMU table walks and the translated data they resolve commonly
	// alternate between two physical RAM regions; this second slot prevents
	// those misses from taking the system bus mutex every time. Private mappings
	// keep using dataCache's single-entry hot path.
	directDataCacheAlt [8]dataRegionCache
	// directDataMissCache remembers physical 1 KiB pages that the attached bus
	// declined as plain RAM (normally MMIO, ROM, or sparse RAM). Repeated scalar
	// accesses can then go straight to the semantic bus path instead of locking
	// once to rediscover that direct access is impossible and again to perform
	// the access. The bus invalidator clears these topology-dependent entries.
	directDataMissCache [directDataMissCacheEntries]uint32
	// virtualData is allocated lazily after the MMU first resolves a direct RAM
	// data access. Native blocks have their own 1 KiB host-pointer TLB; this one
	// removes the remaining MMU/permission/bus work from precise and Go-JIT ARM.
	virtualData *virtualDataCache
	regs        [17]uint32
	// flags holds condition N/Z/C/V lazily: setNZCV records the defining
	// operation here instead of writing CPSR, and resolveFlags materializes it
	// only when a reader actually needs the bits. See pendingFlags.
	flags   pendingFlags
	mode    cpu.Mode
	stopped atomic.Bool
	closed  bool
	// physicalAccess is the one-byte summary of "this machine does not resolve
	// memory through its own private mappings" - a bus is attached, or CP15 has
	// turned on the MMU or the instruction cache. Every guest load, store, and
	// fetch tests it to pick the route, so it lives in padding the fields
	// around it already had: the bus, CP15, and the rest of the whole-system
	// state sit at the end of this struct instead, which keeps regs, flags, and
	// the translated-block caches at the offsets they had before whole-system
	// support and off an extra cache line. See refreshPhysicalAccess.
	physicalAccess bool
	// loopAcceleration enables the deliberately narrow counted-loop fast path
	// in the portable translated tiers. It is fixed at construction time and
	// remains false for NewJIT so the established JIT stays instruction-exact.
	loopAcceleration bool
	mapped           uint64
	memoryLimit      uint64
	pcHits           map[uint32]uint64 // env ARAM_PC_TRACE: per-PC execution histogram
	// jitBlocks is the translated-block cache of the optional pure-Go dynamic
	// recompiler (see jit.go). Nil keeps the precise tree-walking path; non-nil
	// enables the JIT for Thumb alongside armJITBlocks, falling back to the
	// interpreter per instruction for anything it does not translate. It is invalidated on Map/Close and on
	// a guest write into an executable region (self-modifying code).
	jitBlocks map[uint32]*jitBlock
	// armJITBlocks is the ARM counterpart of jitBlocks. It is separate because
	// an aligned address can contain either ARM or Thumb code over the lifetime
	// of a backend, while each cache entry must retain mode-specific decode.
	// Native-JIT backends also allocate this map as the precise decoded fallback
	// for ARM instructions their host emitter does not cover.
	armJITBlocks map[uint32]*jitBlock
	// jitCache is a two-way front for jitBlocks: hot loops dispatch the
	// same few blocks repeatedly, so caching (pc -> block) in a fixed array
	// skips the map hash+lookup that otherwise dominates block dispatch. jitGen
	// is bumped on every invalidation of jitBlocks; an entry whose gen no longer
	// matches is treated as a miss, so the cache never returns a stale block
	// without touching the array on the (rare) invalidation path.
	jitCache    []jitCacheSet
	armJITCache []jitCacheSet
	jitGen      uint64
	// jitCodeLo/jitCodeHi bound the guest-address span of every translated
	// block. smcInvalidate uses them to invalidate only on a write that
	// overlaps translated code, not on the blitter's ordinary framebuffer
	// writes into the same read-write-execute region. Empty span is
	// (^uint32(0), 0), which no write overlaps.
	jitCodeLo uint32
	jitCodeHi uint32
	// jitCodePages makes the Go/ARM translated span page-precise. It also keeps
	// native inline stores away from pages containing portable ARM closures.
	jitCodePages []uint64
	// nativeBlocks and nativeArena drive the optional native machine-code JIT
	// (see native_common.go and the per-host native_*.go emitters). Non-nil
	// nativeBlocks holds emitted Thumb code, while nativeARMBlocks holds ARM host
	// code; both live in nativeArena and fall back instruction-by-instruction to
	// the portable translated tiers. A Windows hybrid whole-system backend keeps
	// nativeBlocks for application mode but selects jitBlocks for firmware Thumb.
	// Like jitBlocks they are invalidated on Map/Close and on a self-modifying
	// write. They coexist only in that explicit hybrid configuration.
	nativeBlocks map[uint32]*nativeBlock
	// nativeARMBlocks is the machine-code counterpart used by native backends.
	// armJITBlocks remains allocated as its decoded-closure fallback.
	nativeARMBlocks map[uint32]*nativeBlock
	nativeArena     *codeArena
	// nativeCodeLo/nativeCodeHi bound the guest-address span of every
	// translated native block, exactly as jitCodeLo/jitCodeHi do for the
	// pure-Go JIT. KTF/WIPI map the guest image read-write-execute, so without
	// the span every framebuffer store the guest blitter makes would look like
	// self-modifying code and flush the whole translation cache. Empty span is
	// (^uint32(0), 0), which no write overlaps.
	nativeCodeLo uint32
	nativeCodeHi uint32
	// nativeCache is the two-way dispatch front for nativeBlocks, the
	// native counterpart of jitCache. Real Thumb ends a translated block every
	// few instructions, so a title dispatches blocks hundreds of thousands of
	// times per frame and the map hash dominated dispatch. nativeGen is bumped
	// on invalidation so stale entries miss without walking the array.
	nativeCache    *[nativeCacheSize]nativeCacheSet
	nativeARMCache *[nativeCacheSize]nativeCacheSet
	nativeGen      uint64
	// nativeLinks are stable indirection slots baked into terminal branches.
	// A translated target publishes its gate address into the slot, allowing
	// subsequent executions to jump block-to-block without returning to Go.
	// Range invalidation zeros only the affected target slots.
	nativeLinks map[nativeLinkKey]*atomic.Uintptr
	// nativeSlow remembers memory instructions that repeatedly miss the inline
	// TLB (normally MMIO). Unconditional slow PCs become interpreter boundaries;
	// conditional ARM PCs remain in the native block and exit only when their
	// condition actually passes.
	nativeSlow map[nativeLinkKey]nativeSlowState
	// nativeCodePages marks, one bit per 4 KiB guest page, the pages that hold
	// translated code. The lo/hi span above is only a hull: KTF/WIPI titles run
	// from a read-write-execute image and allocate their heap and framebuffer
	// inside it, so the hull covers ordinary data and every pixel write looked
	// like self-modifying code - which flushed and re-translated the whole
	// cache several times a frame. The bitmap is the precise test.
	nativeCodePages []uint64
	// tlb is the native JIT's software translation-lookaside buffer (see
	// native_tlb.go): the page table translated blocks probe inline so a guest
	// load/store is a few host instructions instead of a call back into Go. It
	// is non-nil exactly when the native JIT is active; the interpreter's memory
	// path fills it, which is also how a native bail recovers.
	tlb []tlbEntry
	// nativeRemain is the remaining instruction budget for the current native
	// run, passed to translated blocks by pointer so their in-code budget gate
	// can decrement it and stop exactly at the limit (frame pacing depends on an
	// exact retired count). It lives on the Backend so &nativeRemain is stable
	// across the block call and the interpreter tail shares the same counter.
	nativeRemain uint32
	// nativeActiveCount is written by every native budget gate. It makes bail
	// and IRQ refund accounting independent of which linked block was originally
	// entered from Go.
	nativeActiveCount uint32
	// nativeBailAddress is written only on the emitted miss stub. The Go slow
	// path uses it to distinguish cold RAM that just populated the TLB from an
	// address that persistently cannot be admitted (MMIO/page crossing).
	nativeBailAddress uint32
	// currentProcess caches the windows/amd64 pseudo-handle the code arena's
	// WriteProcessMemory copy needs, so emitting a block does not re-query it.
	// It is unused on other hosts.
	currentProcess uintptr

	// Whole-system state. An application machine never reads any of it, so it
	// is kept behind the hot fields above rather than between them.
	systemBus  cpu.MemoryBus
	contextBus cpu.ContextMemoryBus
	blockBus   cpu.BlockMemoryBus
	directBus  cpu.DirectMemoryBus
	cp15       cp15State
	banks      bankedRegisters
	spsr       savedProgramStatus
	// instructionAddress is the guest instruction currently executing. The
	// whole-system loops record it so a bus access can name what caused it,
	// which is cheaper than building the whole attribution per instruction;
	// see accessAttribution.
	instructionAddress uint32
	// readScratch and writeScratch back the width-sized transfers a bus-backed
	// access makes. A local array would be handed to an interface method and so
	// escape to the heap, putting an allocation on every guest load and store.
	// Reads and writes keep separate buffers so neither is reused underneath
	// the other; nothing may hold a slice of these past its own access.
	readScratch    [4]byte
	writeScratch   [4]byte
	executionTraps map[cpu.ExecutionTrap]struct{}
	interruptLines uint32
	closedState    atomic.Bool
	// instructionCacheTable is the functional ARM926 VIVT shadow, consulted
	// only while CP15 enables it, which no application machine does. It is a
	// pointer so an application backend carries eight bytes rather than the
	// whole table.
	instructionCacheTable *[instructionCacheSets]instructionCacheEntry
	// instructionWindow is the line currently feeding the execution loops. A
	// non-zero tag is (virtual PC >> 5) + 1, so a straight ARM run pays one tag
	// comparison for seven of the eight words in a 32-byte line instead of
	// repeating the MVA, privilege, set-index, generation, and resident-tag
	// checks. Every operation that can change those checks invalidates it.
	instructionWindow    *instructionCacheLine
	instructionWindowTag uint32
	mmuTLBTable          *[mmuTLBEntries]mmuTLBEntry
	// mappingGen validates both tables above. Every change that could alter a
	// translation or the permission derived from it -- a TLB flush, an I-cache
	// flush, the control register, the domain access control, the process ID --
	// advances it, which retires every cached entry in constant time.
	mappingGen uint32

	// Translated-block page indexes, one per block map. Only invalidation reads
	// them, so they sit at the tail: inserting anything between regs and the
	// dispatch state above pushes that state onto another cache line and costs
	// the JIT tiers double-digit throughput with no change to their code.
	jitBlockPages       blockPageIndex
	armJITBlockPages    blockPageIndex
	nativeBlockPages    blockPageIndex
	nativeARMBlockPages blockPageIndex

	// Diagnostics. Every one of these is off unless a host explicitly turns it
	// on, and tracing() reports whether any per-instruction ring is armed.
	pcHistory                      []uint32
	pcHistoryNext                  uint64
	pcCaptureAddress               uint32
	pcCaptureLimit                 uint32
	pcRegisterCaptures             []PCRegisterCapture
	cp15ControlHistory             []CP15ControlAccess
	cp15ControlHistoryNext         uint64
	instructionPrefetchHistory     []InstructionCachePrefetchAccess
	instructionPrefetchHistoryNext uint64
	executionStatistics            cpu.ExecutionStatistics
	hostCallScratch                [cpu.MaxHostCallWords * 4]byte
}

func New() *Backend {
	return NewWithMemoryLimit(DefaultMemoryLimit)
}

// JITOptions configures opt-in translated-tier behavior. The zero value keeps
// the pure-Go JIT instruction-exact.
type JITOptions struct {
	// LoopAcceleration recognizes only side-effect-free decrement-and-branch
	// loops. It preserves architectural state and retired-instruction counts,
	// but may execute several proven iterations as one host operation.
	LoopAcceleration bool
}

// NewJIT returns a backend that runs ARM and Thumb through pure-Go translated
// blocks instead of repeatedly decoding them, falling back to the interpreter
// for unsupported instructions. It is
// architecturally a second CPU backend behind the same identity; use
// cpu/conformance to confirm it reproduces the interpreter exactly.
func NewJIT() *Backend {
	return NewJITWithOptions(JITOptions{})
}

// NewJITWithOptions returns the portable translated backend with explicit,
// default-off execution options.
func NewJITWithOptions(options JITOptions) *Backend {
	b := NewWithMemoryLimit(DefaultMemoryLimit)
	b.loopAcceleration = options.LoopAcceleration
	b.jitBlocks = make(map[uint32]*jitBlock)
	b.jitBlockPages = make(blockPageIndex)
	b.jitCache = make([]jitCacheSet, jitCacheSize)
	b.armJITBlocks = make(map[uint32]*jitBlock)
	b.armJITBlockPages = make(blockPageIndex)
	b.armJITCache = make([]jitCacheSet, jitCacheSize)
	b.jitCodePages = make([]uint64, nativeCodePageWords)
	b.jitCodeLo, b.jitCodeHi = ^uint32(0), 0
	return b
}

func NewWithMemoryLimit(limit uint64) *Backend {
	b := &Backend{mode: cpu.ModeARM, memoryLimit: limit}
	if os.Getenv("ARAM_PC_TRACE") != "" {
		b.pcHits = make(map[uint32]uint64, 1<<16)
	}
	return b
}

// PCHits returns a copy of the per-PC execution histogram (env ARAM_PC_TRACE),
// a diagnostic for finding which guest code runs. Nil when tracing is off.
func (b *Backend) PCHits() map[uint32]uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pcHits == nil {
		return nil
	}
	out := make(map[uint32]uint64, len(b.pcHits))
	for k, v := range b.pcHits {
		out[k] = v
	}
	return out
}

// ExecutionStatistics returns low-overhead cumulative scheduler and
// translation counters. Unlike instruction tracing, maintaining these counters
// does not change the execution tier or allocate on the hot path.
func (b *Backend) ExecutionStatistics() cpu.ExecutionStatistics {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.executionStatistics
}

// SetPCHistoryLimit configures a bounded diagnostic ring of instruction
// addresses. The history is host instrumentation and is not CPU save state.
func (b *Backend) SetPCHistoryLimit(limit uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if limit > 1<<20 {
		return fmt.Errorf("PC history limit %d exceeds diagnostic maximum", limit)
	}
	b.pcHistory = make([]uint32, limit)
	b.pcHistoryNext = 0
	return nil
}

// PCHistory returns the configured diagnostic ring in execution order.
func (b *Backend) PCHistory() []uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := min(b.pcHistoryNext, uint64(len(b.pcHistory)))
	history := make([]uint32, int(count))
	if count == 0 {
		return history
	}
	start := b.pcHistoryNext - count
	for index := range history {
		history[index] = b.pcHistory[(start+uint64(index))%uint64(len(b.pcHistory))]
	}
	return history
}

// SetPCRegisterCapture configures a bounded, non-stopping register trace for
// one virtual instruction address. A zero limit disables and clears it.
func (b *Backend) SetPCRegisterCapture(address, limit uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if limit > 4096 {
		return fmt.Errorf("PC register capture limit %d exceeds diagnostic maximum", limit)
	}
	b.pcCaptureAddress = address
	b.pcCaptureLimit = limit
	b.pcRegisterCaptures = nil
	if limit != 0 {
		b.pcRegisterCaptures = make([]PCRegisterCapture, 0, limit)
	}
	return nil
}

// PCRegisterCaptures returns the configured diagnostic snapshots in execution
// order. The returned slice does not alias the backend.
func (b *Backend) PCRegisterCaptures() []PCRegisterCapture {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]PCRegisterCapture(nil), b.pcRegisterCaptures...)
}

// tracing reports whether any per-instruction diagnostic is configured. Run
// loops hoist it so an untraced batch never calls recordPC at all; every
// setter that can turn one on holds the backend mutex, so it cannot change
// while a batch is executing.
// setCP15Control is the only writer of the system control register. Routing
// and the hot summary of it are derived from that word, so keeping the two in
// one place is what stops them from drifting apart.
func (b *Backend) setCP15Control(value uint32) {
	b.cp15.control = value
	// The system and ROM protection bits feed the permission check that cached
	// instruction lines record as passed, and the MMU and cache enables change
	// what a translation means at all.
	b.mappingGen++
	b.invalidateInstructionWindow()
	b.tlbClear()
	b.refreshPhysicalAccess()
}

// refreshPhysicalAccess recomputes the hot routing flag. Every writer of the
// state it summarizes -- the attached bus and the CP15 control register -- has
// to call it, so it is deliberately the only place the summary is derived.
func (b *Backend) refreshPhysicalAccess() {
	b.physicalAccess = b.systemBus != nil || b.mmuEnabled() || b.instructionCacheEnabled()
}

func (b *Backend) tracing() bool {
	return b.pcHits != nil || len(b.pcHistory) != 0 || b.pcCaptureLimit != 0
}

func (b *Backend) recordPC(address uint32) {
	if b.pcHits != nil {
		b.pcHits[address]++
	}
	if len(b.pcHistory) != 0 {
		b.pcHistory[b.pcHistoryNext%uint64(len(b.pcHistory))] = address
		b.pcHistoryNext++
	}
	if b.pcCaptureLimit != 0 && address == b.pcCaptureAddress &&
		uint32(len(b.pcRegisterCaptures)) < b.pcCaptureLimit {
		b.pcRegisterCaptures = append(b.pcRegisterCaptures, PCRegisterCapture{
			Address: address, Registers: b.regs,
		})
	}
}

func (b *Backend) Identity() cpu.Identity {
	name := BackendName
	switch {
	case b.nativeBlocks != nil:
		// Same architecture and portable context as the interpreter; a distinct
		// name makes the active native core observable in diagnostics/UI. A
		// windows whole-system backend may also carry the Go Thumb micro-op tier.
		name = BackendName + "-native"
	case b.jitBlocks != nil && b.loopAcceleration:
		name = BackendName + "-jit-loops"
	case b.jitBlocks != nil:
		// The JIT is the same architecture and context format as the precise
		// interpreter (so saves stay portable), but reports a distinct name so
		// the active core is observable in diagnostics and the settings UI.
		name = BackendName + "-jit"
	}
	return cpu.Identity{
		Name:         name,
		Version:      BackendVersion,
		Architecture: cpu.ARMv5TE,
	}
}

func (b *Backend) Architecture() cpu.Architecture {
	return cpu.ARMv5TE
}

func (b *Backend) SystemCapabilities() cpu.SystemCapabilities {
	return cpu.SystemCapabilities(
		cpu.CapabilityPhysicalBus |
			cpu.CapabilityPrivilegedModes |
			cpu.CapabilityCP15Control |
			cpu.CapabilityMMU |
			cpu.CapabilityInterruptLines |
			cpu.CapabilityExecutionTraps,
	)
}

// SetInterruptLine drives a level-sensitive architectural IRQ or FIQ input.
// It is lock-free so an MMIO device can update its output while the backend is
// executing and holding the CPU mutex.
func (b *Backend) SetInterruptLine(line cpu.InterruptLine, asserted bool) error {
	if !line.Valid() {
		return fmt.Errorf("interrupt line %d: %w", line, cpu.ErrInvalidAddress)
	}
	if b.closedState.Load() {
		return cpu.ErrClosed
	}
	mask := uint32(1) << uint32(line)
	for {
		current := atomic.LoadUint32(&b.interruptLines)
		next := current &^ mask
		if asserted {
			next |= mask
		}
		if atomic.CompareAndSwapUint32(&b.interruptLines, current, next) {
			if b.closedState.Load() {
				atomic.AndUint32(&b.interruptLines, ^mask)
				return cpu.ErrClosed
			}
			return nil
		}
	}
}

// SetExecutionTraps replaces the host-owned instruction boundaries. Traps are
// configuration, not guest CPU state, and remain installed across reset-state
// restoration until the system machine replaces or clears them.
func (b *Backend) SetExecutionTraps(traps []cpu.ExecutionTrap) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	configured := make(map[cpu.ExecutionTrap]struct{}, len(traps))
	for _, trap := range traps {
		if !trap.Valid() {
			return fmt.Errorf("invalid execution trap at 0x%08x", trap.Address)
		}
		if _, duplicate := configured[trap]; duplicate {
			return fmt.Errorf("duplicate execution trap at 0x%08x", trap.Address)
		}
		configured[trap] = struct{}{}
	}
	b.executionTraps = configured
	b.invalidateTranslations()
	return nil
}

func (b *Backend) executionTrapAt(mode cpu.Mode, address uint32) bool {
	// Whole-system execution consults this between instructions, and a machine
	// with no host boundary installed is the common case. The length test is
	// inlined; the map lookup it guards is a call.
	if len(b.executionTraps) == 0 {
		return false
	}
	_, ok := b.executionTraps[cpu.ExecutionTrap{Address: address, Mode: mode}]
	return ok
}

func (b *Backend) Map(address, size uint32, permissions cpu.Permissions) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return cpu.ErrClosed
	}
	if b.systemBus != nil {
		return fmt.Errorf("private mapping with attached system bus: %w", cpu.ErrInvalidMapping)
	}
	end := uint64(address) + uint64(size)
	if size == 0 || end > 1<<32 || !permissions.Valid() ||
		uint64(size) > b.memoryLimit-b.mapped {
		return cpu.ErrInvalidMapping
	}
	for _, mapped := range b.regions {
		mappedEnd := uint64(mapped.address) + uint64(mapped.size)
		if uint64(address) < mappedEnd && uint64(mapped.address) < end {
			return fmt.Errorf("%w: 0x%08x..0x%08x overlaps 0x%08x..0x%08x",
				cpu.ErrInvalidMapping,
				address,
				end,
				mapped.address,
				mappedEnd,
			)
		}
	}
	b.regions = append(b.regions, region{
		address:     address,
		size:        size,
		permissions: permissions,
		data:        make([]byte, int(size)),
	})
	b.mapped += uint64(size)
	sort.Slice(b.regions, func(i, j int) bool {
		return b.regions[i].address < b.regions[j].address
	})
	clear(b.regionHints[:])
	b.executeData = nil
	b.invalidateInstructionWindow()
	b.clearDataCaches()
	b.virtualData = nil
	b.tlbClear()
	if b.jitBlocks != nil {
		clear(b.jitBlocks)
		clear(b.jitBlockPages)
	}
	if b.armJITBlocks != nil {
		clear(b.armJITBlocks)
		clear(b.armJITBlockPages)
	}
	if b.jitBlocks != nil || b.armJITBlocks != nil {
		b.jitGen++
		clear(b.jitCodePages)
		b.jitCodeLo, b.jitCodeHi = ^uint32(0), 0
	}
	b.nativeInvalidate()
	return nil
}

// AttachSystemBus selects bus-backed physical accesses for whole-system
// execution. It is intentionally one-way for a backend instance so a running
// machine cannot switch address spaces underneath saved CPU state.
func (b *Backend) AttachSystemBus(bus cpu.MemoryBus) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if bus == nil {
		return fmt.Errorf("attach system bus: nil bus")
	}
	if b.systemBus != nil {
		return fmt.Errorf("attach system bus: already attached")
	}
	if b.mapped != 0 {
		return fmt.Errorf("attach system bus with private mappings: %w", cpu.ErrInvalidMapping)
	}
	// Blocks emitted before a bus was attached have a different prologue and
	// epilogue shape (no interrupt-line base, and on x86-64 no saved RDI), so a
	// direct link from one of those into a post-attach block would unbalance the
	// host stack. Nothing can have executed yet - attaching rejects a backend
	// with private mappings - so this only makes that structural rather than
	// incidental.
	b.invalidateTranslations()
	b.systemBus = bus
	b.contextBus, _ = bus.(cpu.ContextMemoryBus)
	b.blockBus, _ = bus.(cpu.BlockMemoryBus)
	b.directBus, _ = bus.(cpu.DirectMemoryBus)
	if b.directBus != nil {
		b.directBus.SetDirectMemoryInvalidator(func() {
			// Configuration changes may come from a host goroutine. Waiting for
			// Run's mutex makes sure no generated block is still using a direct
			// host pointer when the observer/mapping change returns.
			b.mu.Lock()
			defer b.mu.Unlock()
			if !b.closed {
				b.invalidateDirectMemory()
			}
		})
	}
	b.refreshPhysicalAccess()
	clear(b.regionHints[:])
	b.executeData = nil
	b.invalidateInstructionWindow()
	b.clearDataCaches()
	b.tlbClear()
	return nil
}

func (b *Backend) ReadMemory(address uint32, destination []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if b.mmuEnabled() {
		return b.readVirtual(address, destination, cpu.PermissionRead)
	}
	return b.copyOut(address, destination, cpu.PermissionRead)
}

func (b *Backend) WriteMemory(address uint32, source []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if b.mmuEnabled() {
		return b.writeVirtual(address, source, cpu.PermissionWrite)
	}
	return b.copyIn(address, source, cpu.PermissionWrite)
}

func (b *Backend) ReadRegister(id uint32) (uint32, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, cpu.ErrClosed
	}
	if id >= uint32(len(b.regs)) {
		return 0, fmt.Errorf("register %d: %w", id, cpu.ErrInvalidAddress)
	}
	if id == cpu.RegisterCPSR {
		b.resolveFlags()
	}
	return b.regs[id], nil
}

func (b *Backend) WriteRegister(id, value uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	return b.writeRegisterLocked(id, value)
}

func (b *Backend) writeRegisterLocked(id, value uint32) error {
	if id >= uint32(len(b.regs)) {
		return fmt.Errorf("register %d: %w", id, cpu.ErrInvalidAddress)
	}
	if id == cpu.RegisterCPSR {
		// The written value is authoritative; drop any deferred flags so a
		// stale pending update cannot later clobber it.
		b.resolveFlags()
		b.flags.dirty = false
		oldMode, oldValid := decodeProcessorMode(b.regs[id] & processorModeMask)
		newMode, newValid := decodeProcessorMode(value & processorModeMask)
		if oldValid && newValid && oldMode != newMode {
			b.switchProcessorMode(oldMode, newMode)
		}
		b.regs[id] = value
		if value&cpu.StatusThumb != 0 {
			b.mode = cpu.ModeThumb
		} else {
			b.mode = cpu.ModeARM
		}
		b.invalidateInstructionWindow()
		b.tlbClear()
	} else {
		b.regs[id] = value
	}
	return nil
}

func (b *Backend) Run(ctx context.Context, address uint32, mode cpu.Mode, budget uint64) cpu.Result {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return cpu.Result{Reason: cpu.StopFault, PC: address, Err: cpu.ErrClosed}
	}
	if mode != cpu.ModeARM && mode != cpu.ModeThumb {
		return cpu.Result{
			Reason: cpu.StopFault,
			PC:     address,
			Err:    fmt.Errorf("CPU mode %d: %w", mode, cpu.ErrInvalidAddress),
		}
	}
	if mode == cpu.ModeARM && address&3 != 0 || mode == cpu.ModeThumb && address&1 != 0 {
		return cpu.Result{Reason: cpu.StopFault, PC: address, Err: cpu.ErrInvalidAddress}
	}

	b.mode = mode
	b.setModeFlag()
	b.regs[cpu.RegisterPC] = address
	b.stopped.Store(false)

	var executed uint64
	for budget == 0 || executed < budget {
		// Poll host cancellation between instruction batches instead of before
		// every guest instruction. Batches are capped at runBatchInstructions so
		// cancellation latency stays bounded, matching the previous
		// per-256-instruction cadence, while the batch executor runs the hot
		// dispatch without re-checking cancellation each instruction.
		if b.stopped.Load() {
			return cpu.Result{
				Reason:       cpu.StopRequested,
				Instructions: executed,
				PC:           b.regs[cpu.RegisterPC],
				Err:          cpu.ErrStopped,
			}
		}
		if err := ctx.Err(); err != nil {
			return cpu.Result{
				Reason:       cpu.StopRequested,
				Instructions: executed,
				PC:           b.regs[cpu.RegisterPC],
				Err:          err,
			}
		}

		batch := uint64(runBatchInstructions)
		if budget != 0 {
			if remaining := budget - executed; remaining < batch {
				batch = remaining
			}
		}
		var (
			retired uint64
			reason  *cpu.StopReason
			err     error
		)
		if b.mode == cpu.ModeThumb {
			switch {
			case b.nativeBlocks != nil && (b.jitBlocks == nil || b.systemBus == nil):
				retired, reason, err = b.runThumbNative(batch)
			case b.jitBlocks != nil:
				retired, reason, err = b.runThumbJIT(batch)
			default:
				retired, reason, err = b.runThumb(batch)
			}
		} else if b.nativeARMBlocks != nil {
			retired, reason, err = b.runARMNative(batch)
		} else if b.armJITBlocks != nil {
			retired, reason, err = b.runARMJIT(batch)
		} else {
			retired, reason, err = b.runARM(batch)
		}
		executed += retired
		if err != nil {
			if b.handleCurrentMMUFault(err) {
				continue
			}
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: executed,
				PC:           b.regs[cpu.RegisterPC],
				Err:          err,
			}
		}
		if reason != nil {
			return cpu.Result{
				Reason:       *reason,
				Instructions: executed,
				PC:           b.regs[cpu.RegisterPC],
			}
		}
	}
	if b.stopped.Load() {
		return cpu.Result{
			Reason:       cpu.StopRequested,
			Instructions: executed,
			PC:           b.regs[cpu.RegisterPC],
			Err:          cpu.ErrStopped,
		}
	}
	if err := ctx.Err(); err != nil {
		return cpu.Result{
			Reason:       cpu.StopRequested,
			Instructions: executed,
			PC:           b.regs[cpu.RegisterPC],
			Err:          err,
		}
	}
	return cpu.Result{
		Reason:       cpu.StopBudget,
		Instructions: executed,
		PC:           b.regs[cpu.RegisterPC],
	}
}

func (b *Backend) Stop() error {
	b.stopped.Store(true)
	return nil
}

func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	b.closedState.Store(true)
	atomic.StoreUint32(&b.interruptLines, 0)
	b.regions = nil
	if b.directBus != nil {
		b.directBus.SetDirectMemoryInvalidator(nil)
	}
	b.systemBus = nil
	b.contextBus = nil
	b.blockBus = nil
	b.directBus = nil
	b.physicalAccess = false
	b.executionTraps = nil
	b.mmuTLBTable = nil
	b.instructionCacheTable = nil
	b.invalidateInstructionWindow()
	clear(b.regionHints[:])
	b.executeData = nil
	b.clearDataCaches()
	b.virtualData = nil
	if b.jitBlocks != nil {
		clear(b.jitBlocks)
		clear(b.jitBlockPages)
		clear(b.armJITBlocks)
		clear(b.armJITBlockPages)
		b.jitGen++
		b.jitCodeLo, b.jitCodeHi = ^uint32(0), 0
	}
	if b.nativeBlocks != nil {
		clear(b.nativeBlocks)
		clear(b.nativeBlockPages)
		clear(b.nativeARMBlocks)
		clear(b.nativeARMBlockPages)
		for _, slot := range b.nativeLinks {
			slot.Store(0)
		}
		clear(b.nativeLinks)
		clear(b.nativeSlow)
		b.nativeCloseArena()
		b.nativeBlocks = nil
		b.nativeBlockPages = nil
		b.nativeARMBlocks = nil
		b.nativeARMBlockPages = nil
		b.nativeLinks = nil
		b.nativeSlow = nil
		b.nativeCodeLo, b.nativeCodeHi = ^uint32(0), 0
		b.tlbClear()
		b.tlb = nil
	}
	if b.armJITBlocks != nil {
		clear(b.armJITBlocks)
		b.armJITBlocks = nil
		b.armJITBlockPages = nil
		b.armJITCache = nil
		b.jitCodePages = nil
	}
	b.mapped = 0
	return nil
}

func (b *Backend) findRegion(address uint32, permission cpu.Permissions) (*region, int, error) {
	hintSlot := int(permission)
	if hintSlot >= 0 && hintSlot < len(b.regionHints) {
		index := b.regionHints[hintSlot]
		if index >= 0 && index < len(b.regions) {
			mapped := &b.regions[index]
			if address >= mapped.address &&
				uint64(address) < uint64(mapped.address)+uint64(mapped.size) {
				if mapped.permissions&permission != permission {
					return nil, 0, fmt.Errorf(
						"%w at 0x%08x",
						cpu.ErrPermissionDenied,
						address,
					)
				}
				return mapped, int(address - mapped.address), nil
			}
		}
	}
	index := sort.Search(len(b.regions), func(index int) bool {
		mapped := &b.regions[index]
		return uint64(address) <
			uint64(mapped.address)+uint64(mapped.size)
	})
	if index >= len(b.regions) || address < b.regions[index].address {
		return nil, 0, fmt.Errorf("%w: 0x%08x", cpu.ErrInvalidAddress, address)
	}
	mapped := &b.regions[index]
	if hintSlot >= 0 && hintSlot < len(b.regionHints) {
		b.regionHints[hintSlot] = index
	}
	if mapped.permissions&permission != permission {
		return nil, 0, fmt.Errorf("%w at 0x%08x", cpu.ErrPermissionDenied, address)
	}
	return mapped, int(address - mapped.address), nil
}

var _ cpu.Backend = (*Backend)(nil)
var _ cpu.SystemBusBackend = (*Backend)(nil)
var _ cpu.SystemBackend = (*Backend)(nil)
var _ cpu.ExecutionTrapBackend = (*Backend)(nil)
var _ cpu.InterruptLineBackend = (*Backend)(nil)
