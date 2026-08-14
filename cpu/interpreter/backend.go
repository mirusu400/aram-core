// Package interpreter provides ARAM's portable, pure-Go ARM/Thumb CPU
// fallback. The initial implementation deliberately faults on instructions it
// does not understand instead of silently treating them as no-ops.
package interpreter

import (
	"context"
	"fmt"
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
	regs           [17]uint32
	mode           cpu.Mode
	stopped        atomic.Bool
	closed         bool
	mapped         uint64
	memoryLimit    uint64
}

func New() *Backend {
	return NewWithMemoryLimit(DefaultMemoryLimit)
}

func NewWithMemoryLimit(limit uint64) *Backend {
	return &Backend{mode: cpu.ModeARM, memoryLimit: limit}
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
		// Poll host cancellation in bounded batches instead of performing a
		// channel select for every guest instruction. At the portable
		// interpreter's throughput this remains promptly interruptible while
		// keeping the inner dispatch loop inexpensive.
		if executed&0xff == 0 {
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
		}

		var (
			reason *cpu.StopReason
			err    error
		)
		if b.mode == cpu.ModeThumb {
			reason, err = b.stepThumb()
		} else {
			reason, err = b.stepARM()
		}
		if err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: executed,
				PC:           b.regs[cpu.RegisterPC],
				Err:          err,
			}
		}
		executed++
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
