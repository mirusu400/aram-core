package ktf

import (
	"encoding/binary"
	"fmt"
	"github.com/mirusu400/aram-core/application/internal/guest"
)

// ValidateClipBuffers validates retained Java arrays against candidate saved
// memory, never the live CPU. external supplies candidate executable/BSS/stack
// regions for guest-provided incremental heaps. Call before committing a load.
func (s *SavedState) ValidateClipBuffers(external func(uint32, uint64) ([]byte, error)) error {
	if s == nil || len(s.clipBuffers) == 0 {
		return nil
	}
	read := func(address uint32, size uint64) ([]byte, error) {
		end := uint64(address) + size
		if end > 1<<32 {
			return nil, fmt.Errorf("retained clip address overflow")
		}
		for _, region := range []struct {
			base uint32
			data []byte
		}{{HostBase, s.hostMemory}, {guest.HeapBase, s.heapMemory}} {
			if uint64(address) >= uint64(region.base) && end <= uint64(region.base)+uint64(len(region.data)) {
				offset := uint64(address) - uint64(region.base)
				return region.data[offset : offset+size], nil
			}
		}
		if external != nil {
			data, err := external(address, size)
			if err != nil {
				return nil, err
			}
			if uint64(len(data)) != size {
				return nil, fmt.Errorf("incomplete saved clip memory range")
			}
			return data, nil
		}
		return nil, fmt.Errorf("retained clip address 0x%x is outside saved memory", address)
	}
	word := func(address uint32) (uint32, error) {
		b, e := read(address, 4)
		if e != nil {
			return 0, e
		}
		return binary.LittleEndian.Uint32(b), nil
	}
	allocated := func(address uint32, size uint64) bool {
		end := uint64(address) + size
		if end > 1<<32 {
			return false
		}
		// An incremental region is its own allocator. A containing parent arena
		// allocation must not make a freed inner block appear live.
		for _, heap := range s.incrementalHeaps {
			if uint64(address) >= uint64(heap.Base) && uint64(address) < uint64(heap.Base)+uint64(heap.Size) {
				for _, block := range heap.Allocations {
					if uint64(address) >= uint64(block.Address) && end <= uint64(block.Address)+uint64(block.Size) {
						return true
					}
				}
				return false
			}
		}
		for _, block := range s.heapAllocations {
			if uint64(address) >= uint64(block.Address) && end <= uint64(block.Address)+uint64(block.Size) {
				return true
			}
		}
		return false
	}
	for instance, alias := range s.clipBuffers {
		clip, ok := s.metadata.Clips[instance]
		if !ok || alias.Array == 0 || !clip.BufferSet || clip.Capacity < 0 || len(clip.Data) > int(clip.Capacity) || (clip.Capacity == 0 && alias.Front != 0) || (clip.Capacity > 0 && alias.Front >= uint32(clip.Capacity)) {
			return fmt.Errorf("invalid retained clip geometry for 0x%x", instance)
		}
		if serviceID := s.metadata.ClipServices[instance]; serviceID != 0 {
			source, err := s.Services.Media.Source(s.owner, serviceID)
			if err != nil || len(source) != len(clip.Data) {
				return fmt.Errorf("retained clip source count differs from metadata")
			}
		}
		if !allocated(alias.Array, 8) {
			return fmt.Errorf("retained clip array is not allocated")
		}
		fields, e := word(alias.Array)
		if e != nil {
			return e
		}
		class, e := word(alias.Array + 4)
		if e != nil {
			return e
		}
		if class == 0 || class != s.metadata.JavaClasses["[B"] {
			return fmt.Errorf("retained clip buffer is not a byte array")
		}
		if uint64(class)+12 > 1<<32 {
			return fmt.Errorf("invalid retained byte-array class")
		}
		descriptor, e := word(class + 8)
		if e != nil {
			return e
		}
		name, e := word(descriptor)
		if e != nil {
			return e
		}
		text, e := read(name, 3)
		if e != nil {
			return e
		}
		if string(text) != "[B\x00" {
			return fmt.Errorf("invalid retained byte-array class name")
		}
		if !allocated(fields, 8+uint64(clip.Capacity)) {
			return fmt.Errorf("retained clip payload is not allocated")
		}
		length, e := word(fields + 4)
		if e != nil {
			return e
		}
		if length != uint32(clip.Capacity) {
			return fmt.Errorf("retained clip capacity differs from saved array")
		}
		// Validate the full payload is present in the candidate without allocating
		// a second copy of a potentially large buffer.
		if _, e := read(fields, 8+uint64(length)); e != nil {
			return e
		}
	}
	return nil
}
