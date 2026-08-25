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
	regs      [17]uint32
	banks     bankedRegisters
	spsr      savedProgramStatus
	cp15      cp15State
	mmuTLB    map[uint32]mmuTranslation
	// flags holds condition N/Z/C/V lazily: setNZCV records the defining
	// operation here instead of writing CPSR, and resolveFlags materializes it
	// only when a reader actually needs the bits. See pendingFlags.
	flags                          pendingFlags
	mode                           cpu.Mode
	stopped                        atomic.Bool
	interruptLines                 atomic.Uint32
	closedState                    atomic.Bool
	closed                         bool
	mapped                         uint64
	memoryLimit                    uint64
	systemBus                      cpu.MemoryBus
	contextBus                     cpu.ContextMemoryBus
	accessContext                  cpu.MemoryAccessContext
	executionTraps                 map[cpu.ExecutionTrap]struct{}
	pcHits                         map[uint32]uint64 // env ARAM_PC_TRACE: per-PC execution histogram
	pcHistory                      []uint32
	pcHistoryNext                  uint64
	pcCaptureAddress               uint32
	pcCaptureLimit                 uint32
	pcRegisterCaptures             []PCRegisterCapture
	cp15ControlHistory             []CP15ControlAccess
	cp15ControlHistoryNext         uint64
	instructionPrefetchHistory     []InstructionCachePrefetchAccess
	instructionPrefetchHistoryNext uint64
	instructionCache               map[uint32]instructionCacheLine
	instructionCacheHot            instructionCacheLine
	instructionCacheHotMVA         uint32
	instructionCacheHotValid       bool
	// jitBlocks is the translated-block cache of the optional pure-Go dynamic
	// recompiler (see jit.go). Nil keeps the precise tree-walking path; non-nil
	// enables the JIT for Thumb, falling back to the interpreter per instruction
	// for anything it does not translate. It is invalidated on Map/Close and on
	// a guest write into an executable region (self-modifying code).
	jitBlocks map[uint32]*jitBlock
	// jitCache is a direct-mapped front for jitBlocks: hot loops dispatch the
	// same few blocks repeatedly, so caching (pc -> block) in a fixed array
	// skips the map hash+lookup that otherwise dominates block dispatch. jitGen
	// is bumped on every invalidation of jitBlocks; an entry whose gen no longer
	// matches is treated as a miss, so the cache never returns a stale block
	// without touching the array on the (rare) invalidation path.
	jitCache []jitCacheEntry
	jitGen   uint64
	// jitCodeLo/jitCodeHi bound the guest-address span of every translated
	// block. smcInvalidate uses them to invalidate only on a write that
	// overlaps translated code, not on the blitter's ordinary framebuffer
	// writes into the same read-write-execute region. Empty span is
	// (^uint32(0), 0), which no write overlaps.
	jitCodeLo uint32
	jitCodeHi uint32
	// nativeBlocks and nativeArena drive the optional native machine-code JIT
	// (see native_common.go and the per-host native_*.go emitters). Non-nil
	// nativeBlocks enables it for Thumb, translating straight runs into host
	// code held in nativeArena and falling back to the interpreter for memory,
	// ARM, and untranslated instructions. Like jitBlocks it is invalidated on
	// Map/Close and on a self-modifying write. jitBlocks and nativeBlocks are
	// mutually exclusive: a backend is the pure-Go JIT or the native JIT, never
	// both.
	nativeBlocks map[uint32]*nativeBlock
	nativeArena  *codeArena
	// nativeCodeLo/nativeCodeHi bound the guest-address span of every
	// translated native block, exactly as jitCodeLo/jitCodeHi do for the
	// pure-Go JIT. KTF/WIPI map the guest image read-write-execute, so without
	// the span every framebuffer store the guest blitter makes would look like
	// self-modifying code and flush the whole translation cache. Empty span is
	// (^uint32(0), 0), which no write overlaps.
	nativeCodeLo uint32
	nativeCodeHi uint32
	// nativeCache is the direct-mapped dispatch front for nativeBlocks, the
	// native counterpart of jitCache. Real Thumb ends a translated block every
	// few instructions, so a title dispatches blocks hundreds of thousands of
	// times per frame and the map hash dominated dispatch. nativeGen is bumped
	// on invalidation so stale entries miss without walking the array.
	nativeCache *[nativeCacheSize]nativeCacheEntry
	nativeGen   uint64
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
	// currentProcess caches the windows/amd64 pseudo-handle the code arena's
	// WriteProcessMemory copy needs, so emitting a block does not re-query it.
	// It is unused on other hosts.
	currentProcess uintptr
}

func New() *Backend {
	return NewWithMemoryLimit(DefaultMemoryLimit)
}

// NewJIT returns a backend that runs Thumb through the pure-Go dynamic
// recompiler (jit.go) instead of the tree-walking interpreter, falling back to
// the interpreter for untranslated instructions and for ARM. It is
// architecturally a second CPU backend behind the same identity; use
// cpu/conformance to confirm it reproduces the interpreter exactly.
func NewJIT() *Backend {
	b := NewWithMemoryLimit(DefaultMemoryLimit)
	b.jitBlocks = make(map[uint32]*jitBlock)
	b.jitCache = make([]jitCacheEntry, jitCacheSize)
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
	case b.jitBlocks != nil:
		// The JIT is the same architecture and context format as the precise
		// interpreter (so saves stay portable), but reports a distinct name so
		// the active core is observable in diagnostics and the settings UI.
		name = BackendName + "-jit"
	case b.nativeBlocks != nil:
		// Same architecture and portable context as the interpreter; a distinct
		// name makes the active native core observable in diagnostics/UI.
		name = BackendName + "-native"
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
		current := b.interruptLines.Load()
		next := current &^ mask
		if asserted {
			next |= mask
		}
		if b.interruptLines.CompareAndSwap(current, next) {
			if b.closedState.Load() {
				b.interruptLines.And(^mask)
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
	return nil
}

func (b *Backend) executionTrapAt(mode cpu.Mode, address uint32) bool {
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
	clear(b.dataCache[:])
	b.tlbClear()
	if b.jitBlocks != nil {
		clear(b.jitBlocks)
		b.jitGen++
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
	b.systemBus = bus
	b.contextBus, _ = bus.(cpu.ContextMemoryBus)
	clear(b.regionHints[:])
	b.executeData = nil
	clear(b.dataCache[:])
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
			case b.jitBlocks != nil:
				retired, reason, err = b.runThumbJIT(batch)
			case b.nativeBlocks != nil:
				retired, reason, err = b.runThumbNative(batch)
			default:
				retired, reason, err = b.runThumb(batch)
			}
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
	b.interruptLines.Store(0)
	b.regions = nil
	b.systemBus = nil
	b.contextBus = nil
	b.executionTraps = nil
	b.mmuTLB = nil
	b.instructionCache = nil
	b.instructionCacheHotValid = false
	clear(b.regionHints[:])
	b.executeData = nil
	clear(b.dataCache[:])
	if b.jitBlocks != nil {
		clear(b.jitBlocks)
		b.jitGen++
		b.jitCodeLo, b.jitCodeHi = ^uint32(0), 0
	}
	if b.nativeBlocks != nil {
		clear(b.nativeBlocks)
		b.nativeCloseArena()
		b.nativeBlocks = nil
		b.nativeCodeLo, b.nativeCodeHi = ^uint32(0), 0
		b.tlbClear()
		b.tlb = nil
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
