package application

import (
	"fmt"
	"sort"
)

const (
	maxSavedHeapAllocations = 1 << 20
	maxSavedEADSImages      = 4096
	maxSavedEADSEvents      = 4096
)

type savedEADSImage struct {
	target      uint32
	pixels      uint32
	width       int
	height      int
	transparent byte
	source      uint32
}

type minigameSavedState struct {
	stage        int
	rngState     uint32
	tickMS       uint32
	presentCount uint32
	enabled      bool

	screenHandle uint32
	screenPixels uint32
	width        int
	height       int

	clipLeft   int
	clipTop    int
	clipRight  int
	clipBottom int
	drawColor  uint32
	drawStyle  uint32
	palette    uint32

	surfaceFormat  uint32
	surfacePalette uint32
	surfaceWidth   int
	surfaceHeight  int
	surfacePixels  uint32
	surfaceWork    uint32
	textSurface    [5]uint32

	imageHeapAllocations []heapBlock
	images               []savedEADSImage
	stats                EADSFrameStats

	imageHeapMemory []byte
}

func (m *Machine) writeMinigameState(writer *stateWriter) error {
	runtime := m.minigame
	if runtime == nil {
		writer.u8(0)
		writer.write([]byte{0, 0, 0})
		return nil
	}
	if runtime.stage < 0 ||
		len(runtime.imageHeap.allocations) > maxSavedHeapAllocations ||
		len(runtime.images) > maxSavedEADSImages ||
		len(runtime.stats.Events) > maxSavedEADSEvents {
		return fmt.Errorf("save EADS runtime: metadata exceeds format limits")
	}
	writer.u8(1)
	writer.write([]byte{0, 0, 0})
	writer.u32(uint32(runtime.stage))
	writer.u32(runtime.rngState)
	writer.u32(runtime.tickMS)
	writer.u32(runtime.presentCount)
	if runtime.enabled {
		writer.u8(1)
	} else {
		writer.u8(0)
	}
	writer.write([]byte{0, 0, 0})

	writer.u32(runtime.screenHandle)
	writer.u32(runtime.screenPixels)
	writer.u32(uint32(runtime.width))
	writer.u32(uint32(runtime.height))
	for _, value := range []int{
		runtime.clipLeft,
		runtime.clipTop,
		runtime.clipRight,
		runtime.clipBottom,
	} {
		writer.u32(uint32(int32(value)))
	}
	writer.u32(runtime.drawColor)
	writer.u32(runtime.drawStyle)
	writer.u32(runtime.palette)
	writer.u32(runtime.surfaceFormat)
	writer.u32(runtime.surfacePalette)
	writer.u32(uint32(int32(runtime.surfaceWidth)))
	writer.u32(uint32(int32(runtime.surfaceHeight)))
	writer.u32(runtime.surfacePixels)
	writer.u32(runtime.surfaceWork)
	for _, value := range runtime.textSurface {
		writer.u32(value)
	}

	writeHeapAllocations(writer, runtime.imageHeap.allocations)

	targets := make([]uint32, 0, len(runtime.images))
	for target := range runtime.images {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })
	writer.u32(uint32(len(targets)))
	for _, target := range targets {
		decoded := runtime.images[target]
		writer.u32(target)
		writer.u32(decoded.pixels)
		writer.u32(uint32(decoded.width))
		writer.u32(uint32(decoded.height))
		writer.u8(decoded.transparent)
		writer.write([]byte{0, 0, 0})
		writer.u32(decoded.source)
	}

	writer.u32(runtime.stats.PresentCount)
	writer.u32(runtime.stats.TickMS)
	writer.u32(uint32(len(runtime.stats.Events)))
	for _, event := range runtime.stats.Events {
		writer.u32(event.Event)
		writer.u64(event.Instructions)
		writer.u64(event.APICalls)
		writer.u32(event.ReturnValue)
	}

	if err := m.writeMemoryState(
		writer,
		eadsImageHeapBase,
		eadsImageHeapSize,
	); err != nil {
		return err
	}
	return nil
}

func writeHeapAllocations(writer *stateWriter, allocations map[uint32]uint32) {
	addresses := make([]uint32, 0, len(allocations))
	for address := range allocations {
		addresses = append(addresses, address)
	}
	sort.Slice(addresses, func(i, j int) bool { return addresses[i] < addresses[j] })
	writer.u32(uint32(len(addresses)))
	for _, address := range addresses {
		writer.u32(address)
		writer.u32(allocations[address])
	}
}

func (m *Machine) parseMinigameState(
	decoder *stateDecoder,
) (*minigameSavedState, error) {
	present := decoder.u8()
	decoder.reserved(3)
	if decoder.err != nil {
		return nil, decoder.err
	}
	if present > 1 {
		return nil, decoder.fail(fmt.Sprintf("invalid EADS runtime flag %d", present))
	}
	if (present == 1) != (m.minigame != nil) {
		return nil, decoder.fail("EADS runtime profile mismatch")
	}
	if present == 0 {
		return nil, nil
	}

	saved := &minigameSavedState{
		stage:        int(decoder.u32()),
		rngState:     decoder.u32(),
		tickMS:       decoder.u32(),
		presentCount: decoder.u32(),
		enabled:      decoder.u8() != 0,
	}
	decoder.reserved(3)
	saved.screenHandle = decoder.u32()
	saved.screenPixels = decoder.u32()
	saved.width = int(decoder.u32())
	saved.height = int(decoder.u32())
	saved.clipLeft = int(int32(decoder.u32()))
	saved.clipTop = int(int32(decoder.u32()))
	saved.clipRight = int(int32(decoder.u32()))
	saved.clipBottom = int(int32(decoder.u32()))
	saved.drawColor = decoder.u32()
	saved.drawStyle = decoder.u32()
	saved.palette = decoder.u32()
	saved.surfaceFormat = decoder.u32()
	saved.surfacePalette = decoder.u32()
	saved.surfaceWidth = int(int32(decoder.u32()))
	saved.surfaceHeight = int(int32(decoder.u32()))
	saved.surfacePixels = decoder.u32()
	saved.surfaceWork = decoder.u32()
	for index := range saved.textSurface {
		saved.textSurface[index] = decoder.u32()
	}
	if decoder.err != nil {
		return nil, decoder.err
	}

	var err error
	saved.imageHeapAllocations, err = readHeapAllocations(
		decoder,
		eadsImageHeapBase,
		eadsImageHeapSize,
	)
	if err != nil {
		return nil, err
	}

	imageCount := decoder.u32()
	if imageCount > maxSavedEADSImages {
		return nil, decoder.fail(fmt.Sprintf(
			"EADS image count %d exceeds limit",
			imageCount,
		))
	}
	saved.images = make([]savedEADSImage, 0, imageCount)
	for index := uint32(0); index < imageCount; index++ {
		decoded := savedEADSImage{
			target: decoder.u32(),
			pixels: decoder.u32(),
			width:  int(decoder.u32()),
			height: int(decoder.u32()),
		}
		decoded.transparent = decoder.u8()
		decoder.reserved(3)
		decoded.source = decoder.u32()
		if decoded.target == 0 || decoded.pixels == 0 ||
			decoded.width <= 0 || decoded.height <= 0 ||
			uint64(decoded.width)*uint64(decoded.height) > 16<<20 {
			return nil, decoder.fail(fmt.Sprintf("invalid EADS image %d", index))
		}
		saved.images = append(saved.images, decoded)
	}

	saved.stats.PresentCount = decoder.u32()
	saved.stats.TickMS = decoder.u32()
	eventCount := decoder.u32()
	if eventCount > maxSavedEADSEvents {
		return nil, decoder.fail(fmt.Sprintf(
			"EADS event count %d exceeds limit",
			eventCount,
		))
	}
	saved.stats.Events = make([]EADSEventResult, 0, eventCount)
	for index := uint32(0); index < eventCount; index++ {
		saved.stats.Events = append(saved.stats.Events, EADSEventResult{
			Event:        decoder.u32(),
			Instructions: decoder.u64(),
			APICalls:     decoder.u64(),
			ReturnValue:  decoder.u32(),
		})
	}
	if decoder.err != nil {
		return nil, decoder.err
	}

	runtime := m.minigame
	if saved.stage < 0 || saved.stage > maxSavedEADSEvents ||
		saved.screenHandle != runtime.screenHandle ||
		saved.screenPixels != runtime.screenPixels ||
		saved.width != runtime.width ||
		saved.height != runtime.height ||
		saved.stats.PresentCount != saved.presentCount ||
		saved.stats.TickMS != saved.tickMS {
		return nil, decoder.fail("EADS runtime geometry or counters mismatch")
	}
	saved.imageHeapMemory = append(
		[]byte(nil),
		decoder.bytes(int(eadsImageHeapSize))...,
	)
	if decoder.err != nil {
		return nil, decoder.err
	}
	return saved, nil
}

func readHeapAllocations(
	decoder *stateDecoder,
	base uint32,
	size uint32,
) ([]heapBlock, error) {
	count := decoder.u32()
	if count > maxSavedHeapAllocations {
		return nil, decoder.fail(fmt.Sprintf(
			"heap allocation count %d exceeds limit",
			count,
		))
	}
	blocks := make([]heapBlock, 0, count)
	end := uint64(base) + uint64(size)
	var previousEnd uint64 = uint64(base)
	for index := uint32(0); index < count; index++ {
		block := heapBlock{
			address: decoder.u32(),
			size:    decoder.u32(),
		}
		blockEnd := uint64(block.address) + uint64(block.size)
		if block.size == 0 || block.size&7 != 0 ||
			uint64(block.address) < previousEnd ||
			uint64(block.address) < uint64(base) ||
			blockEnd > end {
			return nil, decoder.fail(fmt.Sprintf("invalid heap allocation %d", index))
		}
		blocks = append(blocks, block)
		previousEnd = blockEnd
	}
	if decoder.err != nil {
		return nil, decoder.err
	}
	return blocks, nil
}

func (r *minigameRuntime) restoreState(saved *minigameSavedState) error {
	if saved == nil {
		return fmt.Errorf("restore EADS runtime: state is missing")
	}
	for _, memory := range []struct {
		address uint32
		data    []byte
		label   string
	}{
		{eadsImageHeapBase, saved.imageHeapMemory, "image heap"},
	} {
		if err := r.cpu.WriteMemory(memory.address, memory.data); err != nil {
			return fmt.Errorf("restore EADS %s: %w", memory.label, err)
		}
	}
	if err := restoreHeapMetadata(
		&r.imageHeap,
		eadsImageHeapBase,
		eadsImageHeapSize,
		saved.imageHeapAllocations,
	); err != nil {
		return err
	}

	r.images = make(map[uint32]eadsDecodedImage, len(saved.images))
	for _, decoded := range saved.images {
		if _, ok := r.imageHeap.allocations[decoded.pixels]; !ok {
			return fmt.Errorf(
				"restore EADS image 0x%08x: pixels are not allocated",
				decoded.target,
			)
		}
		r.images[decoded.target] = eadsDecodedImage{
			pixels:      decoded.pixels,
			width:       decoded.width,
			height:      decoded.height,
			transparent: decoded.transparent,
			source:      decoded.source,
		}
	}

	r.stage = saved.stage
	r.rngState = saved.rngState
	r.tickMS = saved.tickMS
	r.presentCount = saved.presentCount
	r.enabled = saved.enabled
	r.clipLeft = saved.clipLeft
	r.clipTop = saved.clipTop
	r.clipRight = saved.clipRight
	r.clipBottom = saved.clipBottom
	r.drawColor = saved.drawColor
	r.drawStyle = saved.drawStyle
	r.palette = saved.palette
	r.surfaceFormat = saved.surfaceFormat
	r.surfacePalette = saved.surfacePalette
	r.surfaceWidth = saved.surfaceWidth
	r.surfaceHeight = saved.surfaceHeight
	r.surfacePixels = saved.surfacePixels
	r.surfaceWork = saved.surfaceWork
	r.textSurface = saved.textSurface
	r.stats = saved.stats
	r.stats.Events = append([]EADSEventResult(nil), saved.stats.Events...)
	r.syncFrame()
	return nil
}

func restoreHeapMetadata(
	heap *guestHeap,
	base uint32,
	size uint32,
	blocks []heapBlock,
) error {
	heap.allocations = make(map[uint32]uint32, len(blocks))
	heap.free = heap.free[:0]
	cursor := base
	for _, block := range blocks {
		if block.address > cursor {
			heap.free = append(heap.free, heapBlock{
				address: cursor,
				size:    block.address - cursor,
			})
		}
		heap.allocations[block.address] = block.size
		cursor = block.address + block.size
	}
	end := base + size
	if cursor < end {
		heap.free = append(heap.free, heapBlock{
			address: cursor,
			size:    end - cursor,
		})
	}
	if len(blocks) == 0 && len(heap.free) != 1 {
		return fmt.Errorf("restore guest heap: inconsistent free list")
	}
	return nil
}
