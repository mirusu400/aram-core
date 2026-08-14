// Package guest holds the platform-neutral guest-memory and savestate
// primitives shared by every runtime adapter under application.
package guest

import (
	"sort"

	"github.com/mirusu400/aram-core/cpu"
)

type Heap struct {
	CPU         cpu.Backend
	Free        []Block
	Allocations map[uint32]uint32
	Shared      *Heap
}

type Block struct {
	Address uint32
	Size    uint32
}

func NewHeap(backend cpu.Backend, base, size uint32) Heap {
	return Heap{
		CPU:         backend,
		Free:        []Block{{Address: base, Size: size}},
		Allocations: make(map[uint32]uint32),
	}
}

// Root returns the allocator that owns this heap's free list. Some runtimes
// share one guest address space, so copying a Heap would otherwise copy
// only the slice header and let their free lists diverge into overlapping
// allocations.
func (h *Heap) Root() *Heap {
	for h.Shared != nil {
		h = h.Shared
	}
	return h
}

func (h *Heap) Allocate(size uint32, clearMemory bool) (uint32, error) {
	h = h.Root()
	if size == 0 {
		size = 1
	}
	if size > ^uint32(0)-7 {
		return 0, nil
	}
	size = (size + 7) &^ 7
	for index, block := range h.Free {
		if block.Size < size {
			continue
		}
		address := block.Address
		h.Allocations[address] = size
		if block.Size == size {
			h.Free = append(h.Free[:index], h.Free[index+1:]...)
		} else {
			h.Free[index].Address += size
			h.Free[index].Size -= size
		}
		if clearMemory {
			if err := ZeroMemory(h.CPU, address, size); err != nil {
				return 0, err
			}
		}
		return address, nil
	}
	return 0, nil
}

func (h *Heap) Release(address uint32) bool {
	h = h.Root()
	size, ok := h.Allocations[address]
	if !ok {
		return false
	}
	delete(h.Allocations, address)
	h.Free = append(h.Free, Block{Address: address, Size: size})
	sort.Slice(h.Free, func(i, j int) bool {
		return h.Free[i].Address < h.Free[j].Address
	})
	merged := h.Free[:0]
	for _, block := range h.Free {
		if len(merged) != 0 {
			last := &merged[len(merged)-1]
			if last.Address+last.Size == block.Address {
				last.Size += block.Size
				continue
			}
		}
		merged = append(merged, block)
	}
	h.Free = merged
	return true
}
