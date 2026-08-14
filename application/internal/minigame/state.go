package minigame

import (
	"fmt"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"sort"
)

const (
	maxSavedEADSImages = 4096
	maxSavedEADSEvents = 4096
)

type savedEADSImage struct {
	target      uint32
	pixels      uint32
	width       int
	height      int
	transparent byte
	source      uint32
}

type SavedState struct {
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

	imageHeapAllocations []guest.Block
	images               []savedEADSImage
	stats                EADSFrameStats

	imageHeapMemory []byte
}

func (r *Runtime) WriteState(writer *guest.StateWriter) error {
	runtime := r
	if runtime == nil {
		writer.U8(0)
		writer.Write([]byte{0, 0, 0})
		return nil
	}
	if runtime.stage < 0 ||
		len(runtime.imageHeap.Allocations) > guest.MaxSavedHeapAllocations ||
		len(runtime.images) > maxSavedEADSImages ||
		len(runtime.Stats.Events) > maxSavedEADSEvents {
		return fmt.Errorf("save EADS runtime: metadata exceeds format limits")
	}
	writer.U8(1)
	writer.Write([]byte{0, 0, 0})
	writer.U32(uint32(runtime.stage))
	writer.U32(runtime.rngState)
	writer.U32(runtime.tickMS)
	writer.U32(runtime.presentCount)
	if runtime.enabled {
		writer.U8(1)
	} else {
		writer.U8(0)
	}
	writer.Write([]byte{0, 0, 0})

	writer.U32(runtime.screenHandle)
	writer.U32(runtime.screenPixels)
	writer.U32(uint32(runtime.width))
	writer.U32(uint32(runtime.height))
	for _, value := range []int{
		runtime.clipLeft,
		runtime.clipTop,
		runtime.clipRight,
		runtime.clipBottom,
	} {
		writer.U32(uint32(int32(value)))
	}
	writer.U32(runtime.drawColor)
	writer.U32(runtime.drawStyle)
	writer.U32(runtime.palette)
	writer.U32(runtime.surfaceFormat)
	writer.U32(runtime.surfacePalette)
	writer.U32(uint32(int32(runtime.surfaceWidth)))
	writer.U32(uint32(int32(runtime.surfaceHeight)))
	writer.U32(runtime.surfacePixels)
	writer.U32(runtime.surfaceWork)
	for _, value := range runtime.textSurface {
		writer.U32(value)
	}

	guest.WriteHeapAllocations(writer, runtime.imageHeap.Allocations)

	targets := make([]uint32, 0, len(runtime.images))
	for target := range runtime.images {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })
	writer.U32(uint32(len(targets)))
	for _, target := range targets {
		decoded := runtime.images[target]
		writer.U32(target)
		writer.U32(decoded.pixels)
		writer.U32(uint32(decoded.width))
		writer.U32(uint32(decoded.height))
		writer.U8(decoded.transparent)
		writer.Write([]byte{0, 0, 0})
		writer.U32(decoded.source)
	}

	writer.U32(runtime.Stats.PresentCount)
	writer.U32(runtime.Stats.TickMS)
	writer.U32(uint32(len(runtime.Stats.Events)))
	for _, event := range runtime.Stats.Events {
		writer.U32(event.Event)
		writer.U64(event.Instructions)
		writer.U64(event.APICalls)
		writer.U32(event.ReturnValue)
	}

	if err := guest.WriteMemoryState(
		writer,
		runtime.cpu,
		ImageHeapBase,
		ImageHeapSize,
	); err != nil {
		return err
	}
	return nil
}

func ParseState(r *Runtime, decoder *guest.StateDecoder) (*SavedState, error) {
	present := decoder.U8()
	decoder.Reserved(3)
	if decoder.Err != nil {
		return nil, decoder.Err
	}
	if present > 1 {
		return nil, decoder.Fail(fmt.Sprintf("invalid EADS runtime flag %d", present))
	}
	if (present == 1) != (r != nil) {
		return nil, decoder.Fail("EADS runtime profile mismatch")
	}
	if present == 0 {
		return nil, nil
	}

	saved := &SavedState{
		stage:        int(decoder.U32()),
		rngState:     decoder.U32(),
		tickMS:       decoder.U32(),
		presentCount: decoder.U32(),
		enabled:      decoder.U8() != 0,
	}
	decoder.Reserved(3)
	saved.screenHandle = decoder.U32()
	saved.screenPixels = decoder.U32()
	saved.width = int(decoder.U32())
	saved.height = int(decoder.U32())
	saved.clipLeft = int(int32(decoder.U32()))
	saved.clipTop = int(int32(decoder.U32()))
	saved.clipRight = int(int32(decoder.U32()))
	saved.clipBottom = int(int32(decoder.U32()))
	saved.drawColor = decoder.U32()
	saved.drawStyle = decoder.U32()
	saved.palette = decoder.U32()
	saved.surfaceFormat = decoder.U32()
	saved.surfacePalette = decoder.U32()
	saved.surfaceWidth = int(int32(decoder.U32()))
	saved.surfaceHeight = int(int32(decoder.U32()))
	saved.surfacePixels = decoder.U32()
	saved.surfaceWork = decoder.U32()
	for index := range saved.textSurface {
		saved.textSurface[index] = decoder.U32()
	}
	if decoder.Err != nil {
		return nil, decoder.Err
	}

	var err error
	saved.imageHeapAllocations, err = guest.ReadHeapAllocations(
		decoder,
		ImageHeapBase,
		ImageHeapSize,
	)
	if err != nil {
		return nil, err
	}

	imageCount := decoder.U32()
	if imageCount > maxSavedEADSImages {
		return nil, decoder.Fail(fmt.Sprintf(
			"EADS image count %d exceeds limit",
			imageCount,
		))
	}
	saved.images = make([]savedEADSImage, 0, imageCount)
	for index := uint32(0); index < imageCount; index++ {
		decoded := savedEADSImage{
			target: decoder.U32(),
			pixels: decoder.U32(),
			width:  int(decoder.U32()),
			height: int(decoder.U32()),
		}
		decoded.transparent = decoder.U8()
		decoder.Reserved(3)
		decoded.source = decoder.U32()
		if decoded.target == 0 || decoded.pixels == 0 ||
			decoded.width <= 0 || decoded.height <= 0 ||
			uint64(decoded.width)*uint64(decoded.height) > 16<<20 {
			return nil, decoder.Fail(fmt.Sprintf("invalid EADS image %d", index))
		}
		saved.images = append(saved.images, decoded)
	}

	saved.stats.PresentCount = decoder.U32()
	saved.stats.TickMS = decoder.U32()
	eventCount := decoder.U32()
	if eventCount > maxSavedEADSEvents {
		return nil, decoder.Fail(fmt.Sprintf(
			"EADS event count %d exceeds limit",
			eventCount,
		))
	}
	saved.stats.Events = make([]EADSEventResult, 0, eventCount)
	for index := uint32(0); index < eventCount; index++ {
		saved.stats.Events = append(saved.stats.Events, EADSEventResult{
			Event:        decoder.U32(),
			Instructions: decoder.U64(),
			APICalls:     decoder.U64(),
			ReturnValue:  decoder.U32(),
		})
	}
	if decoder.Err != nil {
		return nil, decoder.Err
	}

	runtime := r
	if saved.stage < 0 || saved.stage > maxSavedEADSEvents ||
		saved.screenHandle != runtime.screenHandle ||
		saved.screenPixels != runtime.screenPixels ||
		saved.width != runtime.width ||
		saved.height != runtime.height ||
		saved.stats.PresentCount != saved.presentCount ||
		saved.stats.TickMS != saved.tickMS {
		return nil, decoder.Fail("EADS runtime geometry or counters mismatch")
	}
	saved.imageHeapMemory = append(
		[]byte(nil),
		decoder.Bytes(int(ImageHeapSize))...,
	)
	if decoder.Err != nil {
		return nil, decoder.Err
	}
	return saved, nil
}

func (r *Runtime) RestoreState(saved *SavedState) error {
	if saved == nil {
		return fmt.Errorf("restore EADS runtime: state is missing")
	}
	for _, memory := range []struct {
		address uint32
		data    []byte
		label   string
	}{
		{ImageHeapBase, saved.imageHeapMemory, "image heap"},
	} {
		if err := r.cpu.WriteMemory(memory.address, memory.data); err != nil {
			return fmt.Errorf("restore EADS %s: %w", memory.label, err)
		}
	}
	if err := guest.RestoreHeapMetadata(
		&r.imageHeap,
		ImageHeapBase,
		ImageHeapSize,
		saved.imageHeapAllocations,
	); err != nil {
		return err
	}

	r.images = make(map[uint32]eadsDecodedImage, len(saved.images))
	for _, decoded := range saved.images {
		if _, ok := r.imageHeap.Allocations[decoded.pixels]; !ok {
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
	r.Stats = saved.stats
	r.Stats.Events = append([]EADSEventResult(nil), saved.stats.Events...)
	r.SyncFrame()
	return nil
}
