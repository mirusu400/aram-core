package minigame

import (
	"fmt"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"sort"
	"time"
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
	stage            int
	rngState         uint32
	tickMS           uint32
	presentCount     uint32
	enabled          bool
	activeEvent      uint32
	activeKey        uint32
	inFlight         bool
	activeInput      bool
	pendingKeys      []keyEvent
	audioHandles     map[uint32]uint32
	timerPeriod      uint32
	nextTick         uint32
	clockMS          uint32
	clockNS          time.Duration
	timersConfigured bool
	eventTimers      [2]eventTimer

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
	runtime.inputMu.Lock()
	pendingKeys := append([]keyEvent(nil), runtime.pendingKeys...)
	runtime.inputMu.Unlock()
	if runtime.stage < 0 ||
		len(runtime.imageHeap.Allocations) > guest.MaxSavedHeapAllocations ||
		len(runtime.images) > maxSavedEADSImages ||
		len(runtime.Stats.Events) > maxSavedEADSEvents ||
		len(pendingKeys) > maxPendingInput ||
		len(runtime.audioHandles) > maxAudioHandles {
		return fmt.Errorf("save EADS runtime: metadata exceeds format limits")
	}
	version := uint8(4)
	if runtime.legacy {
		version = 5
	}
	writer.U8(version)
	writer.Write([]byte{0, 0, 0})
	var timerMask uint32
	if runtime.timersConfigured {
		timerMask = 1
	}
	for index, timer := range runtime.eventTimers {
		if timer.enabled {
			timerMask |= 2 << index
		}
	}
	writer.U32(timerMask)
	writer.U32(runtime.clockMS)
	writer.U64(uint64(runtime.clockNS))
	for _, timer := range runtime.eventTimers {
		writer.U32(timer.deadline)
	}
	if runtime.legacy {
		writer.U32(runtime.timerPeriod)
		writer.U32(runtime.nextTick)
	}
	writer.U32(runtime.activeEvent)
	writer.U32(runtime.activeKey)
	var activeFlags uint32
	if runtime.inFlight {
		activeFlags = 1
	}
	if runtime.activeInput {
		activeFlags |= 2
	}
	writer.U32(activeFlags)
	writer.U32(uint32(len(pendingKeys)))
	for _, event := range pendingKeys {
		writer.U32(event.event)
		writer.U32(event.key)
		writer.U64(uint64(event.at))
	}
	ids := guest.SortedUint32Keys(runtime.audioHandles)
	writer.U32(uint32(len(ids)))
	for _, id := range ids {
		writer.U32(id)
		writer.U32(runtime.audioHandles[id])
	}
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
	writer.U64(runtime.Stats.TotalInstructions)
	writer.U64(runtime.Stats.TotalAPICalls)
	writer.U64(runtime.Stats.InputDispatchCount)
	writer.U64(runtime.Stats.InputReleaseCount)
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
	if present > 5 {
		return nil, decoder.Fail(fmt.Sprintf("invalid EADS runtime flag %d", present))
	}
	if (present != 0) != (r != nil) {
		return nil, decoder.Fail("EADS runtime profile mismatch")
	}
	if present == 0 {
		return nil, nil
	}
	if (present == 3 || present == 5) != r.legacy {
		return nil, decoder.Fail("EADS event ABI mismatch")
	}

	saved := &SavedState{}
	if present >= 2 {
		timerMask := decoder.U32()
		if timerMask > 7 || timerMask&6 != 0 && timerMask&1 == 0 || r.legacy && timerMask != 0 {
			return nil, decoder.Fail("invalid EADS timer mask")
		}
		saved.timersConfigured = timerMask&1 != 0
		saved.clockMS = decoder.U32()
		saved.clockNS = time.Duration(saved.clockMS) * time.Millisecond
		if present >= 4 {
			saved.clockNS = time.Duration(decoder.U64())
			if saved.clockNS < 0 {
				return nil, decoder.Fail("negative EADS clock")
			}
		}
		for index := range saved.eventTimers {
			saved.eventTimers[index] = eventTimer{enabled: timerMask&(2<<index) != 0, deadline: decoder.U32()}
		}
	}
	if present == 3 || present == 5 {
		saved.timerPeriod, saved.nextTick = decoder.U32(), decoder.U32()
		if saved.timerPeriod > 0xffff {
			return nil, decoder.Fail("invalid EADS timer interval")
		}
	}
	if present >= 2 {
		saved.activeEvent = decoder.U32()
		saved.activeKey = decoder.U32()
		flags := decoder.U32()
		saved.inFlight, saved.activeInput = flags&1 != 0, flags&2 != 0
		if flags > 3 || saved.activeInput && !saved.inFlight ||
			!r.validSavedKeyEvent(saved.activeEvent, saved.activeKey, !saved.activeInput) ||
			!saved.inFlight && (saved.activeEvent != 0 || saved.activeKey != 0) {
			return nil, decoder.Fail("invalid active EADS input")
		}
		count := decoder.U32()
		if count > maxPendingInput {
			return nil, decoder.Fail("EADS input count exceeds limit")
		}
		for range count {
			event := keyEvent{event: decoder.U32(), key: decoder.U32()}
			if present >= 4 {
				event.at = time.Duration(decoder.U64())
			}
			if event.at < 0 || len(saved.pendingKeys) > 0 && event.at < saved.pendingKeys[len(saved.pendingKeys)-1].at ||
				!r.validSavedKeyEvent(event.event, event.key, false) {
				return nil, decoder.Fail("invalid pending EADS input")
			}
			saved.pendingKeys = append(saved.pendingKeys, event)
		}
		audioCount := decoder.U32()
		if audioCount > maxAudioHandles {
			return nil, decoder.Fail("EADS audio count exceeds limit")
		}
		saved.audioHandles = make(map[uint32]uint32, audioCount)
		for range audioCount {
			id, handle := decoder.U32(), decoder.U32()
			if id > 0xffff || handle == 0 || saved.audioHandles[id] != 0 {
				return nil, decoder.Fail("invalid EADS audio handle")
			}
			saved.audioHandles[id] = handle
		}
	}
	saved.stage = int(decoder.U32())
	saved.rngState = decoder.U32()
	saved.tickMS = decoder.U32()
	saved.presentCount = decoder.U32()
	saved.enabled = decoder.U8() != 0
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
	if present >= 2 {
		saved.stats.TotalInstructions = decoder.U64()
		saved.stats.TotalAPICalls = decoder.U64()
		saved.stats.InputDispatchCount = decoder.U64()
		saved.stats.InputReleaseCount = decoder.U64()
	}
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
	if present == 1 {
		for _, event := range saved.stats.Events {
			saved.stats.TotalInstructions += event.Instructions
			saved.stats.TotalAPICalls += event.APICalls
		}
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
	r.activeEvent = saved.activeEvent
	r.activeKey = saved.activeKey
	r.inFlight, r.activeInput = saved.inFlight, saved.activeInput
	r.inputMu.Lock()
	r.pendingKeys = append([]keyEvent(nil), saved.pendingKeys...)
	r.inputMu.Unlock()
	r.audioHandles = make(map[uint32]uint32, len(saved.audioHandles))
	for id, handle := range saved.audioHandles {
		r.audioHandles[id] = handle
	}
	r.timerPeriod, r.nextTick = saved.timerPeriod, saved.nextTick
	r.clockMS, r.timersConfigured, r.eventTimers = saved.clockMS, saved.timersConfigured, saved.eventTimers
	r.clockNS = saved.clockNS
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

func (r *Runtime) validSavedKeyEvent(event, key uint32, timer bool) bool {
	if timer {
		if r.legacy {
			return (event == 0 || event == 5) && key == 0
		}
		return (event == 0 || event == StartEvent || event == FrameEvent) && key == 0
	}
	if r.legacy {
		if event == 4 || event == 6 {
			return key <= 0x15
		}
		return false
	}
	if event&0xffff == pointerEvent {
		return r.pointerEnabled && (event>>16 == 1 || event>>16 == 2) &&
			int(uint16(key)) < r.width && int(key>>16) < r.height
	}
	return event <= 0x15 && key == 0
}
