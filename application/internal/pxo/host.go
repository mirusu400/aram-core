package pxo

import (
	"context"
	"fmt"
	"sync"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	GuestHeapBase       = uint32(0x10000000)
	GuestHeapSize       = uint32(16 << 20)
	FramebufferBase     = uint32(0x20000000)
	FramebufferWidth    = 176
	FramebufferHeight   = 220
	FramebufferByteSize = uint32(FramebufferWidth * FramebufferHeight * 2)
)

// Host supplies the small LG handset service surface used by PXO1 games.
// Most of the game engine, resource decoder and renderer live in the guest;
// the handset supplies raw work memory, persistent integer properties, an
// RGB565 display pointer, event-handler registration and a few UI primitives.
type Host struct {
	CPU cpu.Backend

	mu                sync.Mutex
	Handlers          map[uint32]uint32
	ActiveHandler     uint32
	Properties        map[uint32]uint32
	MenuSelection     uint32
	PresentCount      uint64
	heapMapped        bool
	framebufferMapped bool
	heapCursor        uint32
	tick              uint32
}

func NewHost(backend cpu.Backend) *Host {
	return &Host{
		CPU:           backend,
		Handlers:      make(map[uint32]uint32),
		Properties:    make(map[uint32]uint32),
		heapCursor:    GuestHeapBase,
		MenuSelection: 0,
	}
}

func (h *Host) Dispatch(_ context.Context, runtime *Runtime, call Call) (uint32, error) {
	if h == nil || h.CPU == nil || runtime == nil {
		return 0, fmt.Errorf("PXO1 host is unavailable")
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	switch {
	case call.Group == 0x40 && call.Ordinal == 0x145 && len(call.Args) == 2:
		h.Handlers[call.Args[0]] = call.Args[1]
		return 1, nil
	case call.Group == 0x40 && (call.Ordinal == 0x14b || call.Ordinal == 0x141) && len(call.Args) == 1:
		h.ActiveHandler = call.Args[0]
		return 1, nil
	case call.Group == 0x48 && call.Ordinal == 0x32a && len(call.Args) == 2:
		h.Properties[call.Args[0]] = call.Args[1]
		return 1, nil
	case call.Group == 0x48 && call.Ordinal == 0x2e2 && len(call.Args) == 1:
		return h.Properties[call.Args[0]], nil
	case call.Group == 0x48 && call.Ordinal == 0x165 && len(call.Args) == 1:
		return h.allocate(call.Args[0])
	case call.Group == 0x48 && call.Ordinal == 0x164:
		// The handset allocator accepts frees, but the game obtains one arena and
		// suballocates it for its lifetime. Keeping the mapped arena is equivalent
		// until reset and avoids invalidating guest pointers still in flight.
		return 1, nil
	case call.Group == 0x40 && call.Ordinal == 0xc0:
		if err := h.ensureFramebuffer(); err != nil {
			return 0, err
		}
		return FramebufferBase, nil
	case call.Group == 0x44 && call.Ordinal == 0x5f && len(call.Args) >= 2:
		switch call.Args[1] {
		case 0, 2:
			if h.MenuSelection == 0 {
				h.MenuSelection = 1
			} else {
				h.MenuSelection--
			}
		case 1, 3:
			h.MenuSelection = (h.MenuSelection + 1) % 2
		}
		return 1, nil
	case call.Group == 0x44 && call.Ordinal == 0x127:
		return h.MenuSelection, nil
	case call.Group == 0x44 && call.Ordinal == 0x46 && len(call.Args) == 1:
		return handsetColour(call.Args[0]), nil
	case call.Group == 0x40 && (call.Ordinal == 0xc7 || call.Ordinal == 0xdb || call.Ordinal == 0x13d):
		h.PresentCount++
		return uint32(h.PresentCount), nil
	case call.Group == 0x40 && call.Ordinal == 0x120:
		h.tick += 16
		return h.tick, nil
	case call.Group == 0x48 && call.Ordinal == 0x347:
		// A deterministic nonzero clock/random seed is enough for the bundled
		// engines; the game advances it through its own per-frame state.
		h.tick = h.tick*1664525 + 1013904223
		return h.tick, nil
	default:
		return 0, nil
	}
}

func (h *Host) Handler(id uint32) uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.Handlers[id]
}

func (h *Host) Active() uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ActiveHandler
}

func (h *Host) Selection() uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.MenuSelection
}

func (h *Host) FramebufferMapped() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.framebufferMapped
}

func (h *Host) Presents() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.PresentCount
}

func (h *Host) allocate(size uint32) (uint32, error) {
	if size == 0 {
		return 0, nil
	}
	if !h.heapMapped {
		if err := h.CPU.Map(GuestHeapBase, GuestHeapSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
			return 0, fmt.Errorf("map PXO1 work memory: %w", err)
		}
		h.heapMapped = true
	}
	aligned := (size + 7) &^ 7
	if aligned < size || uint64(h.heapCursor)+uint64(aligned) > uint64(GuestHeapBase)+uint64(GuestHeapSize) {
		return 0, fmt.Errorf("PXO1 work-memory request %d exceeds %d-byte arena", size, GuestHeapSize)
	}
	address := h.heapCursor
	h.heapCursor += aligned
	return address, nil
}

func (h *Host) ensureFramebuffer() error {
	if h.framebufferMapped {
		return nil
	}
	// Reserve whole pages so other CPU backends can enforce page-granular maps.
	const mappedSize = uint32(0x20000)
	if err := h.CPU.Map(FramebufferBase, mappedSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		return fmt.Errorf("map PXO1 framebuffer: %w", err)
	}
	h.framebufferMapped = true
	return nil
}

func handsetColour(index uint32) uint32 {
	switch index {
	case 0, 1:
		return 0x0000
	case 2:
		return 0xf800
	case 3:
		return 0x07e0
	case 4:
		return 0x001f
	case 5:
		return 0xffe0
	case 6:
		return 0xf81f
	default:
		return 0xffff
	}
}
