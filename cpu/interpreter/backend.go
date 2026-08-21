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
	banks           bankedRegisters
	spsr            savedProgramStatus
	cp15            cp15State
	tlb             map[uint32]mmuTranslation
	// flags holds condition N/Z/C/V lazily: setNZCV records the defining
	// operation here instead of writing CPSR, and resolveFlags materializes it
	// only when a reader actually needs the bits. See pendingFlags.
	flags          pendingFlags
	mode           cpu.Mode
	stopped        atomic.Bool
	interruptLines atomic.Uint32
	closedState    atomic.Bool
	closed         bool
	mapped         uint64
	memoryLimit    uint64
	systemBus      cpu.MemoryBus
	contextBus     cpu.ContextMemoryBus
	accessContext  cpu.MemoryAccessContext
	executionTraps map[cpu.ExecutionTrap]struct{}
	pcHits         map[uint32]uint64 // env ARAM_PC_TRACE: per-PC execution histogram
}

func New() *Backend {
	return NewWithMemoryLimit(DefaultMemoryLimit)
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
	return cpu.Identity{
		Name:         BackendName,
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
	b.dataData = nil
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
	b.dataData = nil
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
			retired, reason, err = b.runThumb(batch)
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
	b.tlb = nil
	clear(b.regionHints[:])
	b.executeData = nil
	b.dataData = nil
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
