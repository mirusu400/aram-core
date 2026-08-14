package guest

import (
	"fmt"
	"sort"
)

const MaxSavedHeapAllocations = 1 << 20

func WriteHeapAllocations(writer *StateWriter, allocations map[uint32]uint32) {
	addresses := make([]uint32, 0, len(allocations))
	for address := range allocations {
		addresses = append(addresses, address)
	}
	sort.Slice(addresses, func(i, j int) bool { return addresses[i] < addresses[j] })
	writer.U32(uint32(len(addresses)))
	for _, address := range addresses {
		writer.U32(address)
		writer.U32(allocations[address])
	}
}

func ReadHeapAllocations(
	decoder *StateDecoder,
	base uint32,
	size uint32,
) ([]Block, error) {
	count := decoder.U32()
	if count > MaxSavedHeapAllocations {
		return nil, decoder.Fail(fmt.Sprintf(
			"heap allocation count %d exceeds limit",
			count,
		))
	}
	blocks := make([]Block, 0, count)
	end := uint64(base) + uint64(size)
	var previousEnd uint64 = uint64(base)
	for index := uint32(0); index < count; index++ {
		block := Block{
			Address: decoder.U32(),
			Size:    decoder.U32(),
		}
		blockEnd := uint64(block.Address) + uint64(block.Size)
		if block.Size == 0 || block.Size&7 != 0 ||
			uint64(block.Address) < previousEnd ||
			uint64(block.Address) < uint64(base) ||
			blockEnd > end {
			return nil, decoder.Fail(fmt.Sprintf("invalid heap allocation %d", index))
		}
		blocks = append(blocks, block)
		previousEnd = blockEnd
	}
	if decoder.Err != nil {
		return nil, decoder.Err
	}
	return blocks, nil
}

func RestoreHeapMetadata(
	heap *Heap,
	base uint32,
	size uint32,
	blocks []Block,
) error {
	heap.Allocations = make(map[uint32]uint32, len(blocks))
	heap.Free = heap.Free[:0]
	cursor := base
	for _, block := range blocks {
		if block.Address > cursor {
			heap.Free = append(heap.Free, Block{
				Address: cursor,
				Size:    block.Address - cursor,
			})
		}
		heap.Allocations[block.Address] = block.Size
		cursor = block.Address + block.Size
	}
	end := base + size
	if cursor < end {
		heap.Free = append(heap.Free, Block{
			Address: cursor,
			Size:    end - cursor,
		})
	}
	if len(blocks) == 0 && len(heap.Free) != 1 {
		return fmt.Errorf("restore guest heap: inconsistent free list")
	}
	return nil
}
