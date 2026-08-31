package unicornbackend

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/mirusu400/aram-core/cpu"
)

var ErrUnavailable = errors.New("Unicorn comparison backend unavailable")

const (
	BackendName        = "unicorn-comparison"
	BackendVersion     = "1"
	DefaultMemoryLimit = uint64(512 << 20)
)

// Options configures the development backend. LibraryPath takes precedence
// over ARAM_UNICORN_LIBRARY and the platform's conventional library names.
type Options struct {
	LibraryPath string
	MemoryLimit uint64
}

type mapping struct {
	address     uint32
	size        uint32
	permissions cpu.Permissions
}

// Backend is a private-memory, application-mode Unicorn adapter. It executes
// one guest instruction per native call so every cpu.Backend sync point keeps
// exact retirement and stop semantics.
type Backend struct {
	mu          sync.Mutex
	api         *unicornAPI
	engine      uintptr
	identity    cpu.Identity
	mappings    []mapping
	mapped      uint64
	memoryLimit uint64
	stopped     atomic.Bool
	running     atomic.Bool
	closedState atomic.Bool
	closed      bool
}

func New() (*Backend, error) {
	return NewWithOptions(Options{})
}

func NewWithOptions(options Options) (*Backend, error) {
	api, err := loadUnicornAPI(options)
	if err != nil {
		return nil, err
	}
	var engine uintptr
	if code := api.openEngine(ucArchARM, ucModeARM926, &engine); code != ucErrOK {
		openErr := api.callError("open ARM926", code)
		_ = api.release()
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, openErr)
	}
	limit := options.MemoryLimit
	if limit == 0 {
		limit = DefaultMemoryLimit
	}
	backend := &Backend{
		api:         api,
		engine:      engine,
		memoryLimit: limit,
		identity: cpu.Identity{
			Name:         BackendName,
			Version:      fmt.Sprintf("%s+uc%d.%d", BackendVersion, api.major, api.minor),
			Architecture: cpu.ARMv5TE,
		},
	}
	// Unicorn starts ARM engines in a privileged reset state whose Z flag is
	// set. ARAM's application backend starts with a zeroed public context, so
	// clear the application-visible flags while retaining Unicorn's private
	// control and processor-mode bits.
	if err := backend.writeRegisterLocked(cpu.RegisterCPSR, 0); err != nil {
		_ = backend.Close()
		return nil, fmt.Errorf("%w: initialize ARM status: %v", ErrUnavailable, err)
	}
	return backend, nil
}

func (b *Backend) Identity() cpu.Identity { return b.identity }

func (b *Backend) Architecture() cpu.Architecture { return cpu.ARMv5TE }

func (b *Backend) Map(address, size uint32, permissions cpu.Permissions) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	end := uint64(address) + uint64(size)
	if size == 0 || address%ucPageSize != 0 || size%ucPageSize != 0 ||
		end > 1<<32 || !permissions.Valid() ||
		uint64(size) > b.memoryLimit-b.mapped {
		return cpu.ErrInvalidMapping
	}
	for _, existing := range b.mappings {
		existingEnd := uint64(existing.address) + uint64(existing.size)
		if uint64(address) < existingEnd && uint64(existing.address) < end {
			return fmt.Errorf(
				"%w: 0x%08x..0x%08x overlaps 0x%08x..0x%08x",
				cpu.ErrInvalidMapping, address, end, existing.address, existingEnd,
			)
		}
	}
	if code := b.api.mapMemory(
		b.engine, uint64(address), uint64(size), uint32(permissions),
	); code != ucErrOK {
		return fmt.Errorf("%w: %v", cpu.ErrInvalidMapping, b.api.callError("map memory", code))
	}
	b.mappings = append(b.mappings, mapping{
		address: address, size: size, permissions: permissions,
	})
	sort.Slice(b.mappings, func(left, right int) bool {
		return b.mappings[left].address < b.mappings[right].address
	})
	b.mapped += uint64(size)
	return nil
}

func (b *Backend) ReadMemory(address uint32, destination []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if len(destination) == 0 {
		return nil
	}
	if err := b.checkRange(address, len(destination), cpu.PermissionRead); err != nil {
		return err
	}
	code := b.api.readMemory(
		b.engine,
		uint64(address),
		unsafe.Pointer(unsafe.SliceData(destination)),
		uint64(len(destination)),
	)
	runtime.KeepAlive(destination)
	if code != ucErrOK {
		return b.memoryCallError("read memory", code)
	}
	return nil
}

func (b *Backend) WriteMemory(address uint32, source []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if len(source) == 0 {
		return nil
	}
	if err := b.checkRange(address, len(source), cpu.PermissionWrite); err != nil {
		return err
	}
	code := b.api.writeMemory(
		b.engine,
		uint64(address),
		unsafe.Pointer(unsafe.SliceData(source)),
		uint64(len(source)),
	)
	runtime.KeepAlive(source)
	if code != ucErrOK {
		return b.memoryCallError("write memory", code)
	}
	return nil
}

func (b *Backend) checkRange(address uint32, size int, permission cpu.Permissions) error {
	end := uint64(address) + uint64(size)
	if size < 0 || end > 1<<32 {
		return cpu.ErrInvalidAddress
	}
	for _, region := range b.mappings {
		regionEnd := uint64(region.address) + uint64(region.size)
		if address >= region.address && end <= regionEnd {
			if region.permissions&permission == 0 {
				return cpu.ErrPermissionDenied
			}
			return nil
		}
	}
	return cpu.ErrInvalidAddress
}

func (b *Backend) ReadRegister(id uint32) (uint32, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, cpu.ErrClosed
	}
	return b.readRegisterLocked(id)
}

func (b *Backend) readRegisterLocked(id uint32) (uint32, error) {
	if id >= uint32(len(unicornRegisterIDs)) {
		return 0, fmt.Errorf("register %d: %w", id, cpu.ErrInvalidAddress)
	}
	var value uint32
	var nativeValue uint64
	if code := b.api.readRegister(
		b.engine, unicornRegisterIDs[id], unsafe.Pointer(&nativeValue),
	); code != ucErrOK {
		return 0, fmt.Errorf(
			"register %d: %w: %v", id, cpu.ErrInvalidAddress,
			b.api.callError("read register", code),
		)
	}
	value = uint32(nativeValue)
	if id == cpu.RegisterCPSR {
		value &= applicationStatusMask
	} else if id == cpu.RegisterPC {
		value &^= 1
	}
	return value, nil
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
	if id >= uint32(len(unicornRegisterIDs)) {
		return fmt.Errorf("register %d: %w", id, cpu.ErrInvalidAddress)
	}
	nativeValue := uint64(value)
	if id == cpu.RegisterCPSR {
		var current uint64
		if code := b.api.readRegister(
			b.engine, unicornRegisterIDs[id], unsafe.Pointer(&current),
		); code != ucErrOK {
			return fmt.Errorf(
				"register %d: %w: %v", id, cpu.ErrInvalidAddress,
				b.api.callError("read status before write", code),
			)
		}
		nativeValue = current&^uint64(applicationStatusMask) |
			uint64(value&applicationStatusMask)
	} else if id == cpu.RegisterPC {
		var status uint64
		if code := b.api.readRegister(
			b.engine, ucARMRegCPSR, unsafe.Pointer(&status),
		); code != ucErrOK {
			return fmt.Errorf(
				"register %d: %w: %v", id, cpu.ErrInvalidAddress,
				b.api.callError("read status before PC write", code),
			)
		}
		if uint32(status)&cpu.StatusThumb != 0 {
			nativeValue |= 1
		}
	}
	if code := b.api.writeRegister(
		b.engine, unicornRegisterIDs[id], unsafe.Pointer(&nativeValue),
	); code != ucErrOK {
		return fmt.Errorf(
			"register %d: %w: %v", id, cpu.ErrInvalidAddress,
			b.api.callError("write register", code),
		)
	}
	return nil
}

func (b *Backend) memoryCallError(operation string, code int32) error {
	callErr := b.api.callError(operation, code)
	switch code {
	case ucErrReadProtection, ucErrWriteProtection, ucErrFetchProtection:
		return fmt.Errorf("%w: %v", cpu.ErrPermissionDenied, callErr)
	default:
		return fmt.Errorf("%w: %v", cpu.ErrInvalidAddress, callErr)
	}
}

func (b *Backend) Stop() error {
	if b.closedState.Load() {
		return cpu.ErrClosed
	}
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
	b.stopped.Store(true)
	var closeErr error
	if b.engine != 0 {
		if code := b.api.closeEngine(b.engine); code != ucErrOK {
			closeErr = b.api.callError("close engine", code)
		}
		b.engine = 0
	}
	libraryErr := b.api.release()
	b.mappings = nil
	b.mapped = 0
	return errors.Join(closeErr, libraryErr)
}

var _ cpu.Backend = (*Backend)(nil)
