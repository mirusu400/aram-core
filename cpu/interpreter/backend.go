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

// Backend is a bounds-checked ARMv5TE interpreter. It currently implements
// the ARM/Thumb control-flow and integer instructions needed by the first
// application-entry milestone; unsupported encodings produce a precise fault.
type Backend struct {
	mu             sync.Mutex
	regions        []region
	regionHints    [8]int
	executeAddress uint32
	executeData    []byte
	// dataData caches the most recently accessed data region so repeated
	// reads/writes with locality (stack frames, a struct, a framebuffer row)
	// skip the sorted findRegion lookup, mirroring the executeData fetch
	// cache. It is a value copy of the region's slice/address/permissions and
	// stays valid across region re-sorts (regions never overlap and their
	// backing arrays are stable); it is invalidated wherever executeData is.
	dataAddress     uint32
	dataPermissions cpu.Permissions
	dataData        []byte
	regs            [17]uint32
	// flags holds condition N/Z/C/V lazily: setNZCV records the defining
	// operation here instead of writing CPSR, and resolveFlags materializes it
	// only when a reader actually needs the bits. See pendingFlags.
	flags       pendingFlags
	mode        cpu.Mode
	stopped     atomic.Bool
	closed      bool
	mapped      uint64
	memoryLimit uint64
	pcHits      map[uint32]uint64 // env ARAM_PC_TRACE: per-PC execution histogram
	// jitBlocks is the translated-block cache of the optional pure-Go dynamic
	// recompiler (see jit.go). Nil keeps the precise tree-walking path; non-nil
	// enables the JIT for Thumb, falling back to the interpreter per instruction
	// for anything it does not translate. It is invalidated on Map/Close and on
	// a guest write into an executable region (self-modifying code).
	jitBlocks map[uint32]*jitBlock
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
	// nativeRemain is the remaining instruction budget for the current native
	// run, passed to translated blocks by pointer so their in-code budget gate
	// can decrement it and stop exactly at the limit (frame pacing depends on an
	// exact retired count). It lives on the Backend so &nativeRemain is stable
	// across the block call and the interpreter tail shares the same counter.
	nativeRemain uint32
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

func (b *Backend) Map(address, size uint32, permissions cpu.Permissions) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return cpu.ErrClosed
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
	b.dataData = nil
	if b.jitBlocks != nil {
		clear(b.jitBlocks)
	}
	b.nativeInvalidate()
	return nil
}

func (b *Backend) ReadMemory(address uint32, destination []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	return b.copyOut(address, destination, cpu.PermissionRead)
}

func (b *Backend) WriteMemory(address uint32, source []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
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
	b.regs[id] = value
	if id == cpu.RegisterCPSR {
		// The written value is authoritative; drop any deferred flags so a
		// stale pending update cannot later clobber it.
		b.flags.dirty = false
		if value&cpu.StatusThumb != 0 {
			b.mode = cpu.ModeThumb
		} else {
			b.mode = cpu.ModeARM
		}
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
	b.regions = nil
	clear(b.regionHints[:])
	b.executeData = nil
	b.dataData = nil
	if b.jitBlocks != nil {
		clear(b.jitBlocks)
	}
	if b.nativeBlocks != nil {
		clear(b.nativeBlocks)
		b.nativeCloseArena()
		b.nativeBlocks = nil
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
