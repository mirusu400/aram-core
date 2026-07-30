package application

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mirusu400/aram-core/wipi"
)

const (
	maxSavedWIPIFramebuffers = 1024
	maxSavedWIPIEntries      = 4096
	maxSavedWIPILogs         = 4096
)

type wipiSavedState struct {
	tickMS        uint64
	exitRequested bool
	exitCode      int32
	strtokNext    uint32
	screenHandle  uint32
	screenPixels  uint32
	stats         WIPIFrameStats

	heapAllocations  []heapBlock
	framebuffers     []wipiFramebuffer
	properties       map[string][]byte
	shared           map[string]uint32
	sharedSizes      map[uint32]uint32
	timers           map[uint32]wipiTimer
	resources        map[string]*wipiResource
	nextResource     int32
	programs         map[int32]*wipiProgram
	nextProgram      int32
	currentProgram   int32
	appManager       int32
	lastExecuteName  string
	lastExecuteArgs  []string
	lastExecuted     int32
	graphicsEvents   []wipiGraphicsEvent
	files            map[string][]byte
	directories      map[string]bool
	fileTimes        map[string]uint32
	fileHandles      map[int32]wipiFileHandle
	nextFile         int32
	databases        map[string]*wipiDatabase
	databaseHandles  map[int32]string
	nextDatabase     int32
	uicContexts      map[uint32]bool
	uicClasses       map[string]uint32
	uicComponents    map[uint32]*wipiComponent
	uicRepaints      []wipiUICRepaint
	mediaClips       map[uint32]*wipiMediaClip
	mediaVolume      int32
	mediaMute        map[int32]bool
	vibratorLevel    int32
	vibratorTimeout  int32
	backlight        [4]int32
	ledState         int32
	phoneRequests    [][]byte
	serialPorts      map[int32]*wipiSerialPort
	nextSerial       int32
	networkConnected bool
	networkCallback  uint32
	networkParameter uint32
	sockets          map[int32]*wipiSocket
	nextSocket       int32
	http             map[int32]*wipiHTTP
	nextHTTP         int32
	pendingCallbacks []wipiGuestCallback
	observed         map[string]uint64
	unimplemented    map[string]uint64
	logs             []string

	systemMemory []byte
	heapMemory   []byte
}

func (m *Machine) writeWIPIState(writer *stateWriter) error {
	runtime := m.wipi
	if runtime == nil {
		writer.u8(0)
		writer.write([]byte{0, 0, 0})
		return nil
	}
	if len(runtime.heap.allocations) > maxSavedHeapAllocations ||
		len(runtime.framebuffers) > maxSavedWIPIFramebuffers ||
		len(runtime.properties) > maxSavedWIPIEntries ||
		len(runtime.shared) > maxSavedWIPIEntries ||
		len(runtime.sharedSizes) > maxSavedWIPIEntries ||
		len(runtime.timers) > maxSavedWIPIEntries ||
		len(runtime.resources) > maxSavedWIPIEntries ||
		len(runtime.programs) > wipiMaxPrograms ||
		len(runtime.lastExecuteArgs) > wipiMaxExecuteArguments ||
		len(runtime.graphicsEvents) > wipiMaxGraphicsEvents ||
		len(runtime.files) > maxSavedWIPIEntries ||
		len(runtime.directories) > maxSavedWIPIEntries ||
		len(runtime.fileTimes) > maxSavedWIPIEntries ||
		len(runtime.fileHandles) > maxSavedWIPIEntries ||
		len(runtime.databases) > maxSavedWIPIEntries ||
		len(runtime.databaseHandles) > maxSavedWIPIEntries ||
		len(runtime.uicContexts) > maxSavedWIPIEntries ||
		len(runtime.uicClasses) > maxSavedWIPIEntries ||
		len(runtime.uicComponents) > maxSavedWIPIEntries ||
		len(runtime.uicRepaints) > wipiMaxUICRepaints ||
		len(runtime.mediaClips) > maxSavedWIPIEntries ||
		len(runtime.mediaMute) > maxSavedWIPIEntries ||
		len(runtime.phoneRequests) > maxSavedWIPIEntries ||
		len(runtime.serialPorts) > maxSavedWIPIEntries ||
		len(runtime.sockets) > maxSavedWIPIEntries ||
		len(runtime.http) > maxSavedWIPIEntries ||
		len(runtime.pendingCallbacks) > maxSavedWIPIEntries ||
		len(runtime.observed) > maxSavedWIPIEntries ||
		len(runtime.unimplemented) > maxSavedWIPIEntries ||
		len(runtime.logs) > maxSavedWIPILogs {
		return fmt.Errorf("save public WIPI runtime: metadata exceeds format limits")
	}

	writer.u8(1)
	writer.write([]byte{0, 0, 0})
	writer.u64(runtime.tickMS)
	if runtime.exitRequested {
		writer.u8(1)
	} else {
		writer.u8(0)
	}
	writer.write([]byte{0, 0, 0})
	writer.u32(uint32(runtime.exitCode))
	writer.u32(runtime.strtokNext)
	writer.u32(runtime.screenHandle)
	writer.u32(runtime.screenPixels)
	writer.u32(runtime.stats.PresentCount)
	writer.u64(runtime.stats.APICalls)
	writer.u64(runtime.stats.ImplementedCalls)
	writer.u64(runtime.stats.UnimplementedCalls)
	writeWIPIBytes(writer, []byte(runtime.stats.LastAPI))
	writeWIPIBytes(writer, []byte(runtime.stats.LastUnimplemented))

	writeHeapAllocations(writer, runtime.heap.allocations)
	handles := sortedUint32Keys(runtime.framebuffers)
	writer.u32(uint32(len(handles)))
	for _, handle := range handles {
		framebuffer := runtime.framebuffers[handle]
		writer.u32(framebuffer.handle)
		writer.u32(framebuffer.pixels)
		writer.u32(uint32(framebuffer.width))
		writer.u32(uint32(framebuffer.height))
		if framebuffer.owns {
			writer.u8(1)
		} else {
			writer.u8(0)
		}
		writer.write([]byte{0, 0, 0})
	}
	writeWIPIByteMap(writer, runtime.properties)
	writeWIPIStringUint32Map(writer, runtime.shared)
	writeWIPIUint32Map(writer, runtime.sharedSizes)

	timerAddresses := sortedUint32Keys(runtime.timers)
	writer.u32(uint32(len(timerAddresses)))
	for _, address := range timerAddresses {
		timer := runtime.timers[address]
		writer.u32(address)
		writer.u32(timer.callback)
		writer.u32(timer.parameter)
		writer.u64(timer.deadline)
	}
	resourceNames := sortedStringKeys(runtime.resources)
	if len(runtime.resourceIDs) != len(runtime.resources) {
		return fmt.Errorf("save public WIPI resources: identifier index is inconsistent")
	}
	writer.u32(uint32(len(resourceNames)))
	var totalResourceBytes uint64
	var highestResourceID int32
	for _, name := range resourceNames {
		resource := runtime.resources[name]
		if resource == nil || resource.id < 1 || resource.name != name ||
			runtime.resourceIDs[resource.id] != name ||
			len(resource.data) > int(maxWIPICopy) {
			return fmt.Errorf("save public WIPI resource %q: inconsistent metadata", name)
		}
		totalResourceBytes += uint64(len(resource.data))
		highestResourceID = max(highestResourceID, resource.id)
		writer.u32(uint32(resource.id))
		writeWIPIBytes(writer, []byte(resource.name))
		writeWIPIBytes(writer, resource.data)
	}
	if totalResourceBytes > uint64(maxWIPICopy) {
		return fmt.Errorf("save public WIPI resources: data exceeds format limit")
	}
	if runtime.nextResource < 1 || runtime.nextResource <= highestResourceID {
		return fmt.Errorf("save public WIPI resources: next identifier is inconsistent")
	}
	writer.u32(uint32(runtime.nextResource))
	programIDs := sortedWIPIProgramIDs(runtime.programs)
	if err := validateWIPIProgramState(
		runtime.programs,
		runtime.nextProgram,
		runtime.currentProgram,
		runtime.appManager,
		runtime.lastExecuteName,
		runtime.lastExecuteArgs,
		runtime.lastExecuted,
	); err != nil {
		return fmt.Errorf("save public WIPI programs: %w", err)
	}
	writer.u32(uint32(len(programIDs)))
	for _, id := range programIDs {
		program := runtime.programs[id]
		writer.u32(uint32(program.id))
		writer.u32(uint32(program.parentID))
		writer.u32(uint32(program.programType))
		writer.u32(uint32(program.accessLevel))
		if program.running {
			writer.u8(1)
		} else {
			writer.u8(0)
		}
		writer.write([]byte{0, 0, 0})
		writeWIPIBytes(writer, []byte(program.execName))
		writeWIPIBytes(writer, []byte(program.programName))
		writeWIPIBytes(writer, []byte(program.version))
		writeWIPIBytes(writer, []byte(program.vendor))
	}
	writer.u32(uint32(runtime.nextProgram))
	writer.u32(uint32(runtime.currentProgram))
	writer.u32(uint32(runtime.appManager))
	writeWIPIBytes(writer, []byte(runtime.lastExecuteName))
	writer.u32(uint32(len(runtime.lastExecuteArgs)))
	for _, argument := range runtime.lastExecuteArgs {
		writeWIPIBytes(writer, []byte(argument))
	}
	writer.u32(uint32(runtime.lastExecuted))
	writer.u32(uint32(len(runtime.graphicsEvents)))
	for _, event := range runtime.graphicsEvents {
		writer.u32(uint32(event.id))
		writer.u32(uint32(event.kind))
		writer.u32(uint32(event.param1))
		writer.u32(uint32(event.param2))
	}
	writeWIPIFiles(writer, runtime.files)
	directories := sortedStringKeys(runtime.directories)
	writer.u32(uint32(len(directories)))
	for _, directory := range directories {
		writeWIPIBytes(writer, []byte(directory))
	}
	writeWIPIStringUint32Map(writer, runtime.fileTimes)
	fileDescriptors := make([]int, 0, len(runtime.fileHandles))
	for descriptor := range runtime.fileHandles {
		fileDescriptors = append(fileDescriptors, int(descriptor))
	}
	sort.Ints(fileDescriptors)
	writer.u32(uint32(len(fileDescriptors)))
	for _, rawDescriptor := range fileDescriptors {
		descriptor := int32(rawDescriptor)
		handle := runtime.fileHandles[descriptor]
		writer.u32(uint32(descriptor))
		writeWIPIBytes(writer, []byte(handle.path))
		writer.u32(uint32(handle.offset))
		var flags uint8
		if handle.readable {
			flags |= 1
		}
		if handle.writable {
			flags |= 2
		}
		writer.u8(flags)
		writer.write([]byte{0, 0, 0})
	}
	writer.u32(uint32(runtime.nextFile))
	databaseKeys := sortedStringKeys(runtime.databases)
	writer.u32(uint32(len(databaseKeys)))
	for _, key := range databaseKeys {
		database := runtime.databases[key]
		writeWIPIBytes(writer, []byte(key))
		writeWIPIBytes(writer, []byte(database.name))
		writer.u32(database.recordSize)
		writer.u32(uint32(database.mode))
		writer.u32(uint32(database.nextRecord))
		recordIDs := make([]int, 0, len(database.records))
		for recordID := range database.records {
			recordIDs = append(recordIDs, int(recordID))
		}
		sort.Ints(recordIDs)
		writer.u32(uint32(len(recordIDs)))
		for _, rawRecordID := range recordIDs {
			recordID := int32(rawRecordID)
			writer.u32(uint32(recordID))
			writer.write(database.records[recordID])
		}
	}
	databaseHandles := make([]int, 0, len(runtime.databaseHandles))
	for handle := range runtime.databaseHandles {
		databaseHandles = append(databaseHandles, int(handle))
	}
	sort.Ints(databaseHandles)
	writer.u32(uint32(len(databaseHandles)))
	for _, rawHandle := range databaseHandles {
		handle := int32(rawHandle)
		writer.u32(uint32(handle))
		writeWIPIBytes(writer, []byte(runtime.databaseHandles[handle]))
	}
	writer.u32(uint32(runtime.nextDatabase))
	contextHandles := sortedUint32Keys(runtime.uicContexts)
	writer.u32(uint32(len(contextHandles)))
	for _, handle := range contextHandles {
		writer.u32(handle)
	}
	writeWIPIStringUint32Map(writer, runtime.uicClasses)
	componentHandles := sortedUint32Keys(runtime.uicComponents)
	writer.u32(uint32(len(componentHandles)))
	for _, handle := range componentHandles {
		component := runtime.uicComponents[handle]
		if len(component.callbacks) > maxSavedWIPIEntries ||
			len(component.menuItems) > maxSavedWIPIEntries ||
			len(component.listItems) > maxSavedWIPIEntries ||
			len(component.label) > int(maxWIPIString) ||
			len(component.text) > int(maxWIPIString) {
			return fmt.Errorf("save public WIPI component: metadata exceeds format limits")
		}
		writer.u32(component.handle)
		writeWIPIBytes(writer, []byte(component.className))
		for _, value := range []int32{
			component.x,
			component.y,
			component.width,
			component.height,
		} {
			writer.u32(uint32(value))
		}
		if component.enabled {
			writer.u8(1)
		} else {
			writer.u8(0)
		}
		writer.write([]byte{0, 0, 0})
		writer.u32(component.eventHandler)
		writer.u32(uint32(component.font))
		writer.u32(component.foreground)
		writer.u32(component.background)
		writeWIPIBytes(writer, component.label)
		writer.u32(uint32(component.alignment))
		writer.u32(uint32(component.timeMask))
		writer.write(component.timeData[:])
		writer.u32(uint32(component.activeMenu))
		writer.u32(uint32(component.activeList))
		writer.u32(uint32(component.maxText))
		writeWIPIBytes(writer, component.text)

		callbackIndices := make([]int, 0, len(component.callbacks))
		for index := range component.callbacks {
			callbackIndices = append(callbackIndices, int(index))
		}
		sort.Ints(callbackIndices)
		writer.u32(uint32(len(callbackIndices)))
		for _, rawIndex := range callbackIndices {
			index := int32(rawIndex)
			callback := component.callbacks[index]
			writer.u32(uint32(index))
			writer.u32(callback.procedure)
			writer.u32(callback.client)
		}
		writeWIPIItems(writer, component.menuItems)
		writeWIPIItems(writer, component.listItems)
	}
	writer.u32(uint32(len(runtime.uicRepaints)))
	for _, repaint := range runtime.uicRepaints {
		writer.u32(repaint.component)
		writer.u32(uint32(repaint.x))
		writer.u32(uint32(repaint.y))
		writer.u32(uint32(repaint.width))
		writer.u32(uint32(repaint.height))
	}
	writer.u32(uint32(runtime.mediaVolume))
	writer.u32(uint32(runtime.vibratorLevel))
	writer.u32(uint32(runtime.vibratorTimeout))
	for _, value := range runtime.backlight {
		writer.u32(uint32(value))
	}
	writer.u32(uint32(runtime.ledState))
	muteSources := make([]int, 0, len(runtime.mediaMute))
	for source := range runtime.mediaMute {
		muteSources = append(muteSources, int(source))
	}
	sort.Ints(muteSources)
	writer.u32(uint32(len(muteSources)))
	for _, rawSource := range muteSources {
		source := int32(rawSource)
		writer.u32(uint32(source))
		if runtime.mediaMute[source] {
			writer.u8(1)
		} else {
			writer.u8(0)
		}
		writer.write([]byte{0, 0, 0})
	}
	clipHandles := sortedUint32Keys(runtime.mediaClips)
	writer.u32(uint32(len(clipHandles)))
	for _, handle := range clipHandles {
		clip := runtime.mediaClips[handle]
		writer.u32(clip.handle)
		writeWIPIBytes(writer, clip.mediaType)
		writer.u32(uint32(clip.capacity))
		writer.u32(clip.callback)
		writeWIPIBytes(writer, clip.data)
		writer.u32(uint32(clip.position))
		writer.u32(uint32(clip.volume))
		writer.u8(clip.state)
		if clip.repeat {
			writer.u8(1)
		} else {
			writer.u8(0)
		}
		writer.write([]byte{0, 0})
	}
	writer.u32(uint32(len(runtime.phoneRequests)))
	for _, number := range runtime.phoneRequests {
		writeWIPIBytes(writer, number)
	}
	serialDescriptors := make([]int, 0, len(runtime.serialPorts))
	for descriptor := range runtime.serialPorts {
		serialDescriptors = append(serialDescriptors, int(descriptor))
	}
	sort.Ints(serialDescriptors)
	writer.u32(uint32(len(serialDescriptors)))
	for _, rawDescriptor := range serialDescriptors {
		descriptor := int32(rawDescriptor)
		port := runtime.serialPorts[descriptor]
		writer.u32(uint32(port.descriptor))
		writer.u32(uint32(port.port))
		writeWIPIBytes(writer, port.data)
		writer.u32(port.readCallback)
		writer.u32(port.readParameter)
		writer.u32(port.writeCallback)
		writer.u32(port.writeParameter)
	}
	writer.u32(uint32(runtime.nextSerial))
	if runtime.networkConnected {
		writer.u8(1)
	} else {
		writer.u8(0)
	}
	writer.write([]byte{0, 0, 0})
	writer.u32(runtime.networkCallback)
	writer.u32(runtime.networkParameter)
	socketDescriptors := make([]int, 0, len(runtime.sockets))
	for descriptor := range runtime.sockets {
		socketDescriptors = append(socketDescriptors, int(descriptor))
	}
	sort.Ints(socketDescriptors)
	writer.u32(uint32(len(socketDescriptors)))
	for _, rawDescriptor := range socketDescriptors {
		descriptor := int32(rawDescriptor)
		socket := runtime.sockets[descriptor]
		writer.u32(uint32(socket.descriptor))
		writer.u32(uint32(socket.domain))
		writer.u32(uint32(socket.socketType))
		writer.u32(socket.address)
		writer.u32(uint32(socket.port))
		if socket.connected {
			writer.u8(1)
		} else {
			writer.u8(0)
		}
		writer.write([]byte{0, 0, 0})
		writeWIPIBytes(writer, socket.readData)
		writeWIPIBytes(writer, socket.writeData)
		writer.u32(socket.readCallback)
		writer.u32(socket.readParameter)
		writer.u32(socket.writeCallback)
		writer.u32(socket.writeParameter)
	}
	writer.u32(uint32(runtime.nextSocket))
	httpDescriptors := make([]int, 0, len(runtime.http))
	for descriptor := range runtime.http {
		httpDescriptors = append(httpDescriptors, int(descriptor))
	}
	sort.Ints(httpDescriptors)
	writer.u32(uint32(len(httpDescriptors)))
	for _, rawDescriptor := range httpDescriptors {
		descriptor := int32(rawDescriptor)
		request := runtime.http[descriptor]
		writer.u32(uint32(request.descriptor))
		writeWIPIBytes(writer, request.url)
		writeWIPIBytes(writer, request.method)
		writeWIPIBytes(writer, request.request)
		writeWIPIByteMap(writer, request.properties)
		writer.u32(request.proxyHost)
		writer.u32(uint32(request.proxyPort))
		if request.connected {
			writer.u8(1)
		} else {
			writer.u8(0)
		}
		writer.write([]byte{0, 0, 0})
		writeWIPIBytes(writer, request.response)
		writer.u32(uint32(request.code))
	}
	writer.u32(uint32(runtime.nextHTTP))
	writer.u32(uint32(len(runtime.pendingCallbacks)))
	for _, callback := range runtime.pendingCallbacks {
		writer.u32(callback.procedure)
		for _, argument := range callback.args {
			writer.u32(argument)
		}
	}
	writeWIPIStringUint64Map(writer, runtime.observed)
	writeWIPIStringUint64Map(writer, runtime.unimplemented)
	writer.u32(uint32(len(runtime.logs)))
	for _, log := range runtime.logs {
		writeWIPIBytes(writer, []byte(log))
	}

	if err := m.writeMemoryState(writer, wipi.SystemBase, wipi.SystemSize); err != nil {
		return err
	}
	if err := m.writeMemoryState(writer, guestHeapBase, guestHeapSize); err != nil {
		return err
	}
	return nil
}

func (m *Machine) parseWIPIState(decoder *stateDecoder) (*wipiSavedState, error) {
	present := decoder.u8()
	decoder.reserved(3)
	if decoder.err != nil {
		return nil, decoder.err
	}
	if present > 1 {
		return nil, decoder.fail(fmt.Sprintf("invalid public WIPI runtime flag %d", present))
	}
	if (present == 1) != (m.wipi != nil) {
		return nil, decoder.fail("public WIPI runtime profile mismatch")
	}
	if present == 0 {
		return nil, nil
	}

	saved := &wipiSavedState{
		tickMS:        decoder.u64(),
		exitRequested: decoder.u8() != 0,
	}
	decoder.reserved(3)
	saved.exitCode = int32(decoder.u32())
	saved.strtokNext = decoder.u32()
	saved.screenHandle = decoder.u32()
	saved.screenPixels = decoder.u32()
	saved.stats.PresentCount = decoder.u32()
	saved.stats.APICalls = decoder.u64()
	saved.stats.ImplementedCalls = decoder.u64()
	saved.stats.UnimplementedCalls = decoder.u64()
	saved.stats.LastAPI = string(readWIPIBytes(decoder))
	saved.stats.LastUnimplemented = string(readWIPIBytes(decoder))
	if decoder.err != nil {
		return nil, decoder.err
	}
	if saved.stats.ImplementedCalls+saved.stats.UnimplementedCalls != saved.stats.APICalls {
		return nil, decoder.fail("public WIPI call counters are inconsistent")
	}

	var err error
	saved.heapAllocations, err = readHeapAllocations(
		decoder,
		guestHeapBase,
		guestHeapSize,
	)
	if err != nil {
		return nil, err
	}
	allocationSizes := make(map[uint32]uint32, len(saved.heapAllocations))
	for _, block := range saved.heapAllocations {
		allocationSizes[block.address] = block.size
	}

	framebufferCount := decoder.u32()
	if framebufferCount > maxSavedWIPIFramebuffers {
		return nil, decoder.fail(fmt.Sprintf(
			"public WIPI framebuffer count %d exceeds limit",
			framebufferCount,
		))
	}
	saved.framebuffers = make([]wipiFramebuffer, 0, framebufferCount)
	seenHandles := make(map[uint32]struct{}, framebufferCount)
	for index := uint32(0); index < framebufferCount; index++ {
		framebuffer := wipiFramebuffer{
			handle:       decoder.u32(),
			pixels:       decoder.u32(),
			width:        int(decoder.u32()),
			height:       int(decoder.u32()),
			bitsPerPixel: 32,
			owns:         decoder.u8() != 0,
		}
		decoder.reserved(3)
		pixelSize := uint64(framebuffer.width) * uint64(framebuffer.height) * 4
		if framebuffer.handle == 0 || framebuffer.pixels == 0 ||
			framebuffer.width <= 0 || framebuffer.height <= 0 ||
			framebuffer.width > 4096 || framebuffer.height > 4096 ||
			allocationSizes[framebuffer.handle] < 24 ||
			pixelSize > uint64(allocationSizes[framebuffer.pixels]) {
			return nil, decoder.fail(fmt.Sprintf("invalid public WIPI framebuffer %d", index))
		}
		if _, duplicate := seenHandles[framebuffer.handle]; duplicate {
			return nil, decoder.fail("duplicate public WIPI framebuffer handle")
		}
		seenHandles[framebuffer.handle] = struct{}{}
		saved.framebuffers = append(saved.framebuffers, framebuffer)
	}

	saved.properties = readWIPIByteMap(decoder)
	saved.shared = readWIPIStringUint32Map(decoder)
	saved.sharedSizes = readWIPIUint32Map(decoder)
	timerCount := decoder.u32()
	if timerCount > maxSavedWIPIEntries {
		return nil, decoder.fail(fmt.Sprintf("public WIPI timer count %d exceeds limit", timerCount))
	}
	saved.timers = make(map[uint32]wipiTimer, timerCount)
	for index := uint32(0); index < timerCount; index++ {
		address := decoder.u32()
		timer := wipiTimer{
			callback:  decoder.u32(),
			parameter: decoder.u32(),
			deadline:  decoder.u64(),
		}
		if _, duplicate := saved.timers[address]; duplicate {
			return nil, decoder.fail("duplicate public WIPI timer")
		}
		saved.timers[address] = timer
	}
	resourceCount := decoder.u32()
	if resourceCount > maxSavedWIPIEntries {
		return nil, decoder.fail(fmt.Sprintf("public WIPI resource count %d exceeds limit", resourceCount))
	}
	saved.resources = make(map[string]*wipiResource, resourceCount)
	resourceIDs := make(map[int32]string, resourceCount)
	totalResourceBytes := uint64(0)
	var highestResourceID int32
	for index := uint32(0); index < resourceCount; index++ {
		resource := &wipiResource{
			id:   int32(decoder.u32()),
			name: string(readWIPIBytes(decoder)),
			data: append([]byte(nil), readWIPIBytes(decoder)...),
		}
		totalResourceBytes += uint64(len(resource.data))
		if resource.id < 1 || resource.name == "" ||
			len(resource.data) > int(maxWIPICopy) {
			return nil, decoder.fail("invalid public WIPI resource")
		}
		if saved.resources[resource.name] != nil {
			return nil, decoder.fail("duplicate public WIPI resource name")
		}
		if _, duplicate := resourceIDs[resource.id]; duplicate {
			return nil, decoder.fail("duplicate public WIPI resource identifier")
		}
		saved.resources[resource.name] = resource
		resourceIDs[resource.id] = resource.name
		highestResourceID = max(highestResourceID, resource.id)
	}
	if totalResourceBytes > uint64(maxWIPICopy) {
		return nil, decoder.fail("public WIPI resources exceed state limit")
	}
	saved.nextResource = int32(decoder.u32())
	if saved.nextResource < 1 || saved.nextResource <= highestResourceID {
		return nil, decoder.fail("invalid public WIPI next resource identifier")
	}
	programCount := decoder.u32()
	if programCount == 0 || programCount > wipiMaxPrograms {
		return nil, decoder.fail(fmt.Sprintf("invalid public WIPI program count %d", programCount))
	}
	saved.programs = make(map[int32]*wipiProgram, programCount)
	for index := uint32(0); index < programCount; index++ {
		program := &wipiProgram{
			id:          int32(decoder.u32()),
			parentID:    int32(decoder.u32()),
			programType: int32(decoder.u32()),
			accessLevel: int32(decoder.u32()),
			running:     decoder.u8() != 0,
		}
		decoder.reserved(3)
		program.execName = string(readWIPIBytes(decoder))
		program.programName = string(readWIPIBytes(decoder))
		program.version = string(readWIPIBytes(decoder))
		program.vendor = string(readWIPIBytes(decoder))
		if _, duplicate := saved.programs[program.id]; duplicate {
			return nil, decoder.fail("duplicate public WIPI program identifier")
		}
		saved.programs[program.id] = program
	}
	saved.nextProgram = int32(decoder.u32())
	saved.currentProgram = int32(decoder.u32())
	saved.appManager = int32(decoder.u32())
	saved.lastExecuteName = string(readWIPIBytes(decoder))
	executeArgumentCount := decoder.u32()
	if executeArgumentCount > wipiMaxExecuteArguments {
		return nil, decoder.fail("public WIPI execute argument count exceeds limit")
	}
	saved.lastExecuteArgs = make([]string, 0, executeArgumentCount)
	for index := uint32(0); index < executeArgumentCount; index++ {
		saved.lastExecuteArgs = append(saved.lastExecuteArgs, string(readWIPIBytes(decoder)))
	}
	saved.lastExecuted = int32(decoder.u32())
	if err := validateWIPIProgramState(
		saved.programs,
		saved.nextProgram,
		saved.currentProgram,
		saved.appManager,
		saved.lastExecuteName,
		saved.lastExecuteArgs,
		saved.lastExecuted,
	); err != nil {
		return nil, decoder.fail(fmt.Sprintf("invalid public WIPI program state: %v", err))
	}
	eventCount := decoder.u32()
	if eventCount > wipiMaxGraphicsEvents {
		return nil, decoder.fail("public WIPI graphics event count exceeds limit")
	}
	saved.graphicsEvents = make([]wipiGraphicsEvent, 0, eventCount)
	for index := uint32(0); index < eventCount; index++ {
		saved.graphicsEvents = append(saved.graphicsEvents, wipiGraphicsEvent{
			id:     int32(decoder.u32()),
			kind:   int32(decoder.u32()),
			param1: int32(decoder.u32()),
			param2: int32(decoder.u32()),
		})
	}
	saved.files = readWIPIFiles(decoder)
	directoryCount := decoder.u32()
	if directoryCount > maxSavedWIPIEntries {
		return nil, decoder.fail(fmt.Sprintf("public WIPI directory count %d exceeds limit", directoryCount))
	}
	saved.directories = make(map[string]bool, directoryCount)
	for index := uint32(0); index < directoryCount; index++ {
		directory := string(readWIPIBytes(decoder))
		if directory == "" || saved.directories[directory] {
			return nil, decoder.fail("invalid or duplicate public WIPI directory")
		}
		saved.directories[directory] = true
	}
	saved.fileTimes = readWIPIStringUint32Map(decoder)
	fileHandleCount := decoder.u32()
	if fileHandleCount > maxSavedWIPIEntries {
		return nil, decoder.fail(fmt.Sprintf("public WIPI file-handle count %d exceeds limit", fileHandleCount))
	}
	saved.fileHandles = make(map[int32]wipiFileHandle, fileHandleCount)
	for index := uint32(0); index < fileHandleCount; index++ {
		descriptor := int32(decoder.u32())
		handle := wipiFileHandle{
			path:   string(readWIPIBytes(decoder)),
			offset: int(decoder.u32()),
		}
		flags := decoder.u8()
		decoder.reserved(3)
		handle.readable = flags&1 != 0
		handle.writable = flags&2 != 0
		if descriptor < 3 || flags&^uint8(3) != 0 ||
			(!handle.readable && !handle.writable) ||
			handle.offset < 0 || handle.offset > wipiFilesystemCapacity {
			return nil, decoder.fail("invalid public WIPI file handle")
		}
		if _, duplicate := saved.fileHandles[descriptor]; duplicate {
			return nil, decoder.fail("duplicate public WIPI file descriptor")
		}
		saved.fileHandles[descriptor] = handle
	}
	saved.nextFile = int32(decoder.u32())
	databaseCount := decoder.u32()
	if databaseCount > maxSavedWIPIEntries {
		return nil, decoder.fail(fmt.Sprintf("public WIPI database count %d exceeds limit", databaseCount))
	}
	saved.databases = make(map[string]*wipiDatabase, databaseCount)
	totalDatabaseBytes := uint64(0)
	for index := uint32(0); index < databaseCount; index++ {
		key := string(readWIPIBytes(decoder))
		database := &wipiDatabase{
			name:       string(readWIPIBytes(decoder)),
			recordSize: decoder.u32(),
			mode:       int32(decoder.u32()),
			nextRecord: int32(decoder.u32()),
		}
		recordCount := decoder.u32()
		if key == "" || database.name == "" ||
			database.recordSize == 0 || database.recordSize > maxWIPIString ||
			database.nextRecord < 0 || recordCount > maxSavedWIPIEntries {
			return nil, decoder.fail("invalid public WIPI database metadata")
		}
		database.records = make(map[int32][]byte, recordCount)
		var highestRecord int32 = -1
		for recordIndex := uint32(0); recordIndex < recordCount; recordIndex++ {
			recordID := int32(decoder.u32())
			if recordID < 0 {
				return nil, decoder.fail("invalid public WIPI record identifier")
			}
			if _, duplicate := database.records[recordID]; duplicate {
				return nil, decoder.fail("duplicate public WIPI record identifier")
			}
			record := append([]byte(nil), decoder.bytes(int(database.recordSize))...)
			database.records[recordID] = record
			totalDatabaseBytes += uint64(len(record))
			highestRecord = max(highestRecord, recordID)
		}
		if database.nextRecord <= highestRecord {
			return nil, decoder.fail("public WIPI next record identifier is inconsistent")
		}
		if _, duplicate := saved.databases[key]; duplicate {
			return nil, decoder.fail("duplicate public WIPI database")
		}
		saved.databases[key] = database
	}
	if totalDatabaseBytes > uint64(64<<20) {
		return nil, decoder.fail("public WIPI databases exceed state limit")
	}
	databaseHandleCount := decoder.u32()
	if databaseHandleCount > maxSavedWIPIEntries {
		return nil, decoder.fail(fmt.Sprintf(
			"public WIPI database-handle count %d exceeds limit",
			databaseHandleCount,
		))
	}
	saved.databaseHandles = make(map[int32]string, databaseHandleCount)
	for index := uint32(0); index < databaseHandleCount; index++ {
		handle := int32(decoder.u32())
		key := string(readWIPIBytes(decoder))
		if handle < 1 || saved.databases[key] == nil {
			return nil, decoder.fail("invalid public WIPI database handle")
		}
		if _, duplicate := saved.databaseHandles[handle]; duplicate {
			return nil, decoder.fail("duplicate public WIPI database handle")
		}
		saved.databaseHandles[handle] = key
	}
	saved.nextDatabase = int32(decoder.u32())
	contextCount := decoder.u32()
	if contextCount > maxSavedWIPIEntries {
		return nil, decoder.fail(fmt.Sprintf("public WIPI UI context count %d exceeds limit", contextCount))
	}
	saved.uicContexts = make(map[uint32]bool, contextCount)
	for index := uint32(0); index < contextCount; index++ {
		handle := decoder.u32()
		if handle == 0 || allocationSizes[handle] < 64 || saved.uicContexts[handle] {
			return nil, decoder.fail("invalid public WIPI UI context")
		}
		saved.uicContexts[handle] = true
	}
	saved.uicClasses = readWIPIStringUint32Map(decoder)
	classNames := make(map[uint32]string, len(saved.uicClasses))
	for name, handle := range saved.uicClasses {
		if name == "" || handle == 0 || allocationSizes[handle] < 16 {
			return nil, decoder.fail("invalid public WIPI UI class")
		}
		if _, duplicate := classNames[handle]; duplicate {
			return nil, decoder.fail("duplicate public WIPI UI class handle")
		}
		classNames[handle] = name
	}
	componentCount := decoder.u32()
	if componentCount > maxSavedWIPIEntries {
		return nil, decoder.fail(fmt.Sprintf("public WIPI component count %d exceeds limit", componentCount))
	}
	saved.uicComponents = make(map[uint32]*wipiComponent, componentCount)
	for index := uint32(0); index < componentCount; index++ {
		component := &wipiComponent{
			handle:    decoder.u32(),
			className: string(readWIPIBytes(decoder)),
			x:         int32(decoder.u32()),
			y:         int32(decoder.u32()),
			width:     int32(decoder.u32()),
			height:    int32(decoder.u32()),
			enabled:   decoder.u8() != 0,
		}
		decoder.reserved(3)
		component.eventHandler = decoder.u32()
		component.font = int32(decoder.u32())
		component.foreground = decoder.u32()
		component.background = decoder.u32()
		component.label = append([]byte(nil), readWIPIBytes(decoder)...)
		component.alignment = int32(decoder.u32())
		component.timeMask = int32(decoder.u32())
		copy(component.timeData[:], decoder.bytes(len(component.timeData)))
		component.activeMenu = int32(decoder.u32())
		component.activeList = int32(decoder.u32())
		component.maxText = int32(decoder.u32())
		component.text = append([]byte(nil), readWIPIBytes(decoder)...)
		if component.handle == 0 || allocationSizes[component.handle] < 128 ||
			component.className == "" ||
			saved.uicClasses[component.className] == 0 ||
			component.width < 0 || component.height < 0 ||
			component.maxText < 0 || component.maxText > int32(maxWIPIString) ||
			len(component.text) > int(component.maxText) {
			return nil, decoder.fail("invalid public WIPI component metadata")
		}
		callbackCount := decoder.u32()
		if callbackCount > maxSavedWIPIEntries {
			return nil, decoder.fail("public WIPI component callback count exceeds limit")
		}
		component.callbacks = make(map[int32]wipiUICallback, callbackCount)
		for callbackIndex := uint32(0); callbackIndex < callbackCount; callbackIndex++ {
			selector := int32(decoder.u32())
			callback := wipiUICallback{
				procedure: decoder.u32(),
				client:    decoder.u32(),
			}
			if _, duplicate := component.callbacks[selector]; duplicate {
				return nil, decoder.fail("duplicate public WIPI component callback")
			}
			component.callbacks[selector] = callback
		}
		component.menuItems = readWIPIItems(decoder)
		component.listItems = readWIPIItems(decoder)
		if decoder.err != nil {
			return nil, decoder.err
		}
		if component.activeMenu < -1 || component.activeMenu >= int32(len(component.menuItems)) ||
			component.activeList < -1 || component.activeList >= int32(len(component.listItems)) {
			return nil, decoder.fail("public WIPI component active item is invalid")
		}
		if _, duplicate := saved.uicComponents[component.handle]; duplicate {
			return nil, decoder.fail("duplicate public WIPI component handle")
		}
		saved.uicComponents[component.handle] = component
	}
	repaintCount := decoder.u32()
	if repaintCount > wipiMaxUICRepaints {
		return nil, decoder.fail("public WIPI repaint count exceeds limit")
	}
	saved.uicRepaints = make([]wipiUICRepaint, 0, repaintCount)
	for index := uint32(0); index < repaintCount; index++ {
		repaint := wipiUICRepaint{
			component: decoder.u32(),
			x:         int32(decoder.u32()),
			y:         int32(decoder.u32()),
			width:     int32(decoder.u32()),
			height:    int32(decoder.u32()),
		}
		if saved.uicComponents[repaint.component] == nil {
			return nil, decoder.fail("public WIPI repaint refers to an absent component")
		}
		saved.uicRepaints = append(saved.uicRepaints, repaint)
	}
	saved.mediaVolume = int32(decoder.u32())
	saved.vibratorLevel = int32(decoder.u32())
	saved.vibratorTimeout = int32(decoder.u32())
	for index := range saved.backlight {
		saved.backlight[index] = int32(decoder.u32())
	}
	saved.ledState = int32(decoder.u32())
	if saved.mediaVolume < 0 || saved.mediaVolume > 100 || saved.vibratorTimeout < 0 {
		return nil, decoder.fail("invalid public WIPI media global state")
	}
	muteCount := decoder.u32()
	if muteCount > maxSavedWIPIEntries {
		return nil, decoder.fail("public WIPI mute-state count exceeds limit")
	}
	saved.mediaMute = make(map[int32]bool, muteCount)
	for index := uint32(0); index < muteCount; index++ {
		source := int32(decoder.u32())
		muted := decoder.u8()
		decoder.reserved(3)
		if muted > 1 {
			return nil, decoder.fail("invalid public WIPI mute state")
		}
		if _, duplicate := saved.mediaMute[source]; duplicate {
			return nil, decoder.fail("duplicate public WIPI mute source")
		}
		saved.mediaMute[source] = muted == 1
	}
	clipCount := decoder.u32()
	if clipCount > maxSavedWIPIEntries {
		return nil, decoder.fail("public WIPI media-clip count exceeds limit")
	}
	saved.mediaClips = make(map[uint32]*wipiMediaClip, clipCount)
	for index := uint32(0); index < clipCount; index++ {
		clip := &wipiMediaClip{
			handle:    decoder.u32(),
			mediaType: append([]byte(nil), readWIPIBytes(decoder)...),
			capacity:  int32(decoder.u32()),
			callback:  decoder.u32(),
			data:      append([]byte(nil), readWIPIBytes(decoder)...),
			position:  int32(decoder.u32()),
			volume:    int32(decoder.u32()),
			state:     decoder.u8(),
			repeat:    decoder.u8() != 0,
		}
		decoder.reserved(2)
		if clip.handle == 0 || allocationSizes[clip.handle] < 64 ||
			clip.capacity < 0 || clip.capacity > int32(maxWIPIString) ||
			(clip.capacity > 0 && len(clip.data) > int(clip.capacity)) ||
			clip.volume < 0 || clip.volume > 100 || clip.state > 3 {
			return nil, decoder.fail("invalid public WIPI media clip")
		}
		if _, duplicate := saved.mediaClips[clip.handle]; duplicate {
			return nil, decoder.fail("duplicate public WIPI media clip")
		}
		saved.mediaClips[clip.handle] = clip
	}
	phoneCount := decoder.u32()
	if phoneCount > maxSavedWIPIEntries {
		return nil, decoder.fail("public WIPI phone-request count exceeds limit")
	}
	saved.phoneRequests = make([][]byte, 0, phoneCount)
	for index := uint32(0); index < phoneCount; index++ {
		saved.phoneRequests = append(saved.phoneRequests, append([]byte(nil), readWIPIBytes(decoder)...))
	}
	serialCount := decoder.u32()
	if serialCount > maxSavedWIPIEntries {
		return nil, decoder.fail("public WIPI serial-port count exceeds limit")
	}
	saved.serialPorts = make(map[int32]*wipiSerialPort, serialCount)
	for index := uint32(0); index < serialCount; index++ {
		port := &wipiSerialPort{
			descriptor:     int32(decoder.u32()),
			port:           int32(decoder.u32()),
			data:           append([]byte(nil), readWIPIBytes(decoder)...),
			readCallback:   decoder.u32(),
			readParameter:  decoder.u32(),
			writeCallback:  decoder.u32(),
			writeParameter: decoder.u32(),
		}
		if port.descriptor < 1 || port.port != 0 {
			return nil, decoder.fail("invalid public WIPI serial port")
		}
		if _, duplicate := saved.serialPorts[port.descriptor]; duplicate {
			return nil, decoder.fail("duplicate public WIPI serial descriptor")
		}
		saved.serialPorts[port.descriptor] = port
	}
	saved.nextSerial = int32(decoder.u32())
	if saved.nextSerial < 1 {
		return nil, decoder.fail("invalid public WIPI next serial descriptor")
	}
	saved.networkConnected = decoder.u8() != 0
	decoder.reserved(3)
	saved.networkCallback = decoder.u32()
	saved.networkParameter = decoder.u32()
	socketCount := decoder.u32()
	if socketCount > maxSavedWIPIEntries {
		return nil, decoder.fail("public WIPI socket count exceeds limit")
	}
	saved.sockets = make(map[int32]*wipiSocket, socketCount)
	for index := uint32(0); index < socketCount; index++ {
		socket := &wipiSocket{
			descriptor: int32(decoder.u32()),
			domain:     int32(decoder.u32()),
			socketType: int32(decoder.u32()),
			address:    decoder.u32(),
			port:       uint16(decoder.u32()),
			connected:  decoder.u8() != 0,
		}
		decoder.reserved(3)
		socket.readData = append([]byte(nil), readWIPIBytes(decoder)...)
		socket.writeData = append([]byte(nil), readWIPIBytes(decoder)...)
		socket.readCallback = decoder.u32()
		socket.readParameter = decoder.u32()
		socket.writeCallback = decoder.u32()
		socket.writeParameter = decoder.u32()
		if socket.descriptor < 1 {
			return nil, decoder.fail("invalid public WIPI socket")
		}
		if _, duplicate := saved.sockets[socket.descriptor]; duplicate {
			return nil, decoder.fail("duplicate public WIPI socket")
		}
		saved.sockets[socket.descriptor] = socket
	}
	saved.nextSocket = int32(decoder.u32())
	if saved.nextSocket < 1 {
		return nil, decoder.fail("invalid public WIPI next socket descriptor")
	}
	httpCount := decoder.u32()
	if httpCount > maxSavedWIPIEntries {
		return nil, decoder.fail("public WIPI HTTP request count exceeds limit")
	}
	saved.http = make(map[int32]*wipiHTTP, httpCount)
	for index := uint32(0); index < httpCount; index++ {
		request := &wipiHTTP{
			descriptor: int32(decoder.u32()),
			url:        append([]byte(nil), readWIPIBytes(decoder)...),
			method:     append([]byte(nil), readWIPIBytes(decoder)...),
			request:    append([]byte(nil), readWIPIBytes(decoder)...),
			properties: readWIPIByteMap(decoder),
			proxyHost:  decoder.u32(),
			proxyPort:  uint16(decoder.u32()),
			connected:  decoder.u8() != 0,
		}
		decoder.reserved(3)
		request.response = append([]byte(nil), readWIPIBytes(decoder)...)
		request.code = int32(decoder.u32())
		if request.descriptor < 1 || len(request.url) == 0 ||
			len(request.properties) > maxSavedWIPIEntries {
			return nil, decoder.fail("invalid public WIPI HTTP request")
		}
		if _, duplicate := saved.http[request.descriptor]; duplicate {
			return nil, decoder.fail("duplicate public WIPI HTTP descriptor")
		}
		saved.http[request.descriptor] = request
	}
	saved.nextHTTP = int32(decoder.u32())
	if saved.nextHTTP < 1 {
		return nil, decoder.fail("invalid public WIPI next HTTP descriptor")
	}
	callbackCount := decoder.u32()
	if callbackCount > maxSavedWIPIEntries {
		return nil, decoder.fail("public WIPI pending callback count exceeds limit")
	}
	saved.pendingCallbacks = make([]wipiGuestCallback, 0, callbackCount)
	for index := uint32(0); index < callbackCount; index++ {
		callback := wipiGuestCallback{procedure: decoder.u32()}
		for argument := range callback.args {
			callback.args[argument] = decoder.u32()
		}
		if callback.procedure == 0 {
			return nil, decoder.fail("public WIPI pending callback is null")
		}
		saved.pendingCallbacks = append(saved.pendingCallbacks, callback)
	}
	saved.observed = readWIPIStringUint64Map(decoder)
	saved.unimplemented = readWIPIStringUint64Map(decoder)
	logCount := decoder.u32()
	if logCount > maxSavedWIPILogs {
		return nil, decoder.fail(fmt.Sprintf("public WIPI log count %d exceeds limit", logCount))
	}
	saved.logs = make([]string, 0, logCount)
	for index := uint32(0); index < logCount; index++ {
		saved.logs = append(saved.logs, string(readWIPIBytes(decoder)))
	}
	if decoder.err != nil {
		return nil, decoder.err
	}
	if len(saved.observed) > maxSavedWIPIEntries ||
		len(saved.unimplemented) > maxSavedWIPIEntries ||
		len(saved.properties) > maxSavedWIPIEntries ||
		len(saved.shared) > maxSavedWIPIEntries ||
		len(saved.sharedSizes) > maxSavedWIPIEntries {
		return nil, decoder.fail("public WIPI map exceeds entry limit")
	}
	if (saved.screenHandle == 0) != (saved.screenPixels == 0) {
		return nil, decoder.fail("public WIPI screen handles are inconsistent")
	}
	if saved.screenHandle != 0 {
		framebuffer, ok := seenHandles[saved.screenHandle]
		_ = framebuffer
		if !ok {
			return nil, decoder.fail("public WIPI screen framebuffer is absent")
		}
		found := false
		for _, current := range saved.framebuffers {
			if current.handle == saved.screenHandle && current.pixels == saved.screenPixels {
				found = true
				break
			}
		}
		if !found {
			return nil, decoder.fail("public WIPI screen pixels do not match its framebuffer")
		}
	}
	for key, address := range saved.shared {
		size, ok := saved.sharedSizes[address]
		if key == "" || !ok || allocationSizes[address] < size {
			return nil, decoder.fail("public WIPI shared buffer metadata is inconsistent")
		}
	}
	if !saved.directories["/private"] ||
		!saved.directories["/shared"] ||
		!saved.directories["/system"] ||
		saved.nextFile < 3 {
		return nil, decoder.fail("public WIPI filesystem roots or next descriptor are invalid")
	}
	if saved.nextDatabase < 1 {
		return nil, decoder.fail("public WIPI next database handle is invalid")
	}
	totalFileBytes := 0
	for name, data := range saved.files {
		if !validSavedWIPIPath(name) {
			return nil, decoder.fail("invalid public WIPI file path")
		}
		totalFileBytes += len(data)
	}
	if totalFileBytes > wipiFilesystemCapacity {
		return nil, decoder.fail("public WIPI filesystem exceeds capacity")
	}
	for directory := range saved.directories {
		if !validSavedWIPIPath(directory) {
			return nil, decoder.fail("invalid public WIPI directory path")
		}
	}
	for _, handle := range saved.fileHandles {
		if _, ok := saved.files[handle.path]; !ok {
			return nil, decoder.fail("public WIPI file handle refers to an absent file")
		}
	}

	saved.systemMemory = append([]byte(nil), decoder.bytes(int(wipi.SystemSize))...)
	saved.heapMemory = append([]byte(nil), decoder.bytes(int(guestHeapSize))...)
	if decoder.err != nil {
		return nil, decoder.err
	}
	return saved, nil
}

func (r *wipiRuntime) restoreState(saved *wipiSavedState) error {
	if saved == nil {
		return fmt.Errorf("restore public WIPI runtime: state is missing")
	}
	if err := r.cpu.WriteMemory(wipi.SystemBase, saved.systemMemory); err != nil {
		return fmt.Errorf("restore public WIPI system memory: %w", err)
	}
	if err := r.cpu.WriteMemory(wipi.TrampolineBase, r.layout.Trampolines); err != nil {
		return fmt.Errorf("restore public WIPI trampolines: %w", err)
	}
	if err := r.cpu.WriteMemory(guestHeapBase, saved.heapMemory); err != nil {
		return fmt.Errorf("restore public WIPI heap memory: %w", err)
	}
	if err := restoreHeapMetadata(
		&r.heap,
		guestHeapBase,
		guestHeapSize,
		saved.heapAllocations,
	); err != nil {
		return err
	}
	r.framebuffers = make(map[uint32]wipiFramebuffer, len(saved.framebuffers))
	for _, framebuffer := range saved.framebuffers {
		r.framebuffers[framebuffer.handle] = framebuffer
	}
	r.screenHandle = saved.screenHandle
	r.screenPixels = saved.screenPixels
	r.properties = cloneByteMap(saved.properties)
	r.shared = cloneStringUint32Map(saved.shared)
	r.sharedSizes = cloneUint32Map(saved.sharedSizes)
	r.timers = cloneTimerMap(saved.timers)
	r.resources = cloneResources(saved.resources)
	r.resourceIDs = make(map[int32]string, len(saved.resources))
	for name, resource := range r.resources {
		r.resourceIDs[resource.id] = name
	}
	r.nextResource = saved.nextResource
	r.programs = clonePrograms(saved.programs)
	r.nextProgram = saved.nextProgram
	r.currentProgram = saved.currentProgram
	r.appManager = saved.appManager
	r.lastExecuteName = saved.lastExecuteName
	r.lastExecuteArgs = append([]string(nil), saved.lastExecuteArgs...)
	r.lastExecuted = saved.lastExecuted
	r.graphicsEvents = append([]wipiGraphicsEvent(nil), saved.graphicsEvents...)
	r.files = cloneByteMap(saved.files)
	r.directories = cloneStringBoolMap(saved.directories)
	r.fileTimes = cloneStringUint32Map(saved.fileTimes)
	r.fileHandles = cloneFileHandleMap(saved.fileHandles)
	r.nextFile = saved.nextFile
	r.databases = cloneDatabases(saved.databases)
	r.databaseHandles = cloneInt32StringMap(saved.databaseHandles)
	r.nextDatabase = saved.nextDatabase
	r.uicContexts = cloneUint32BoolMap(saved.uicContexts)
	r.uicClasses = cloneStringUint32Map(saved.uicClasses)
	r.uicClassNames = make(map[uint32]string, len(saved.uicClasses))
	for name, handle := range saved.uicClasses {
		r.uicClassNames[handle] = name
	}
	r.uicComponents = cloneComponents(saved.uicComponents)
	r.uicRepaints = append([]wipiUICRepaint(nil), saved.uicRepaints...)
	r.mediaClips = cloneMediaClips(saved.mediaClips)
	r.mediaVolume = saved.mediaVolume
	r.mediaMute = cloneInt32BoolMap(saved.mediaMute)
	r.vibratorLevel = saved.vibratorLevel
	r.vibratorTimeout = saved.vibratorTimeout
	r.backlight = saved.backlight
	r.ledState = saved.ledState
	r.phoneRequests = cloneByteSlices(saved.phoneRequests)
	r.serialPorts = cloneSerialPorts(saved.serialPorts)
	r.nextSerial = saved.nextSerial
	r.networkConnected = saved.networkConnected
	r.networkCallback = saved.networkCallback
	r.networkParameter = saved.networkParameter
	r.sockets = cloneSockets(saved.sockets)
	r.nextSocket = saved.nextSocket
	r.http = cloneHTTP(saved.http)
	r.nextHTTP = saved.nextHTTP
	r.pendingCallbacks = append([]wipiGuestCallback(nil), saved.pendingCallbacks...)
	r.observed = cloneStringUint64Map(saved.observed)
	r.unimplemented = cloneStringUint64Map(saved.unimplemented)
	r.logs = append([]string(nil), saved.logs...)
	r.strtokNext = saved.strtokNext
	r.tickMS = saved.tickMS
	r.exitRequested = saved.exitRequested
	r.exitCode = saved.exitCode
	r.stats = saved.stats
	return nil
}

func writeWIPIBytes(writer *stateWriter, value []byte) {
	if writer.err != nil {
		return
	}
	if uint64(len(value)) > uint64(maxWIPIString) {
		writer.err = fmt.Errorf("public WIPI state byte field exceeds %d bytes", maxWIPIString)
		return
	}
	writer.u32(uint32(len(value)))
	writer.write(value)
}

func readWIPIBytes(decoder *stateDecoder) []byte {
	size := decoder.u32()
	if size > maxWIPIString {
		decoder.err = decoder.fail(fmt.Sprintf(
			"public WIPI byte field size %d exceeds limit",
			size,
		))
		return nil
	}
	return decoder.bytes(int(size))
}

func writeWIPIByteMap(writer *stateWriter, values map[string][]byte) {
	keys := sortedStringKeys(values)
	writer.u32(uint32(len(keys)))
	for _, key := range keys {
		writeWIPIBytes(writer, []byte(key))
		writeWIPIBytes(writer, values[key])
	}
}

func readWIPIByteMap(decoder *stateDecoder) map[string][]byte {
	count := decoder.u32()
	if count > maxSavedWIPIEntries {
		decoder.err = decoder.fail(fmt.Sprintf("public WIPI byte-map count %d exceeds limit", count))
		return nil
	}
	result := make(map[string][]byte, count)
	for index := uint32(0); index < count; index++ {
		key := string(readWIPIBytes(decoder))
		value := append([]byte(nil), readWIPIBytes(decoder)...)
		if _, duplicate := result[key]; duplicate {
			decoder.err = decoder.fail("duplicate public WIPI byte-map key")
			return result
		}
		result[key] = value
	}
	return result
}

func writeWIPIFiles(writer *stateWriter, values map[string][]byte) {
	keys := sortedStringKeys(values)
	writer.u32(uint32(len(keys)))
	for _, key := range keys {
		writeWIPIBytes(writer, []byte(key))
		value := values[key]
		if len(value) > wipiFilesystemCapacity {
			writer.err = fmt.Errorf("public WIPI file exceeds filesystem capacity")
			return
		}
		writer.u32(uint32(len(value)))
		writer.write(value)
	}
}

func readWIPIFiles(decoder *stateDecoder) map[string][]byte {
	count := decoder.u32()
	if count > maxSavedWIPIEntries {
		decoder.err = decoder.fail(fmt.Sprintf("public WIPI file count %d exceeds limit", count))
		return nil
	}
	result := make(map[string][]byte, count)
	for index := uint32(0); index < count; index++ {
		name := string(readWIPIBytes(decoder))
		size := decoder.u32()
		if size > wipiFilesystemCapacity {
			decoder.err = decoder.fail(fmt.Sprintf("public WIPI file size %d exceeds limit", size))
			return result
		}
		if _, duplicate := result[name]; duplicate {
			decoder.err = decoder.fail("duplicate public WIPI file")
			return result
		}
		result[name] = append([]byte(nil), decoder.bytes(int(size))...)
	}
	return result
}

func writeWIPIItems(writer *stateWriter, items []wipiUIItem) {
	writer.u32(uint32(len(items)))
	for _, item := range items {
		writeWIPIBytes(writer, item.label)
		writer.u32(item.image)
	}
}

func readWIPIItems(decoder *stateDecoder) []wipiUIItem {
	count := decoder.u32()
	if count > maxSavedWIPIEntries {
		decoder.err = decoder.fail(fmt.Sprintf("public WIPI UI item count %d exceeds limit", count))
		return nil
	}
	result := make([]wipiUIItem, 0, count)
	for index := uint32(0); index < count; index++ {
		result = append(result, wipiUIItem{
			label: append([]byte(nil), readWIPIBytes(decoder)...),
			image: decoder.u32(),
		})
	}
	return result
}

func writeWIPIStringUint32Map(writer *stateWriter, values map[string]uint32) {
	keys := sortedStringKeys(values)
	writer.u32(uint32(len(keys)))
	for _, key := range keys {
		writeWIPIBytes(writer, []byte(key))
		writer.u32(values[key])
	}
}

func readWIPIStringUint32Map(decoder *stateDecoder) map[string]uint32 {
	count := decoder.u32()
	if count > maxSavedWIPIEntries {
		decoder.err = decoder.fail(fmt.Sprintf("public WIPI string map count %d exceeds limit", count))
		return nil
	}
	result := make(map[string]uint32, count)
	for index := uint32(0); index < count; index++ {
		key := string(readWIPIBytes(decoder))
		value := decoder.u32()
		if _, duplicate := result[key]; duplicate {
			decoder.err = decoder.fail("duplicate public WIPI string-map key")
			return result
		}
		result[key] = value
	}
	return result
}

func writeWIPIUint32Map(writer *stateWriter, values map[uint32]uint32) {
	keys := sortedUint32Keys(values)
	writer.u32(uint32(len(keys)))
	for _, key := range keys {
		writer.u32(key)
		writer.u32(values[key])
	}
}

func readWIPIUint32Map(decoder *stateDecoder) map[uint32]uint32 {
	count := decoder.u32()
	if count > maxSavedWIPIEntries {
		decoder.err = decoder.fail(fmt.Sprintf("public WIPI integer-map count %d exceeds limit", count))
		return nil
	}
	result := make(map[uint32]uint32, count)
	for index := uint32(0); index < count; index++ {
		key := decoder.u32()
		value := decoder.u32()
		if _, duplicate := result[key]; duplicate {
			decoder.err = decoder.fail("duplicate public WIPI integer-map key")
			return result
		}
		result[key] = value
	}
	return result
}

func writeWIPIStringUint64Map(writer *stateWriter, values map[string]uint64) {
	keys := sortedStringKeys(values)
	writer.u32(uint32(len(keys)))
	for _, key := range keys {
		writeWIPIBytes(writer, []byte(key))
		writer.u64(values[key])
	}
}

func readWIPIStringUint64Map(decoder *stateDecoder) map[string]uint64 {
	count := decoder.u32()
	if count > maxSavedWIPIEntries {
		decoder.err = decoder.fail(fmt.Sprintf("public WIPI counter-map count %d exceeds limit", count))
		return nil
	}
	result := make(map[string]uint64, count)
	for index := uint32(0); index < count; index++ {
		key := string(readWIPIBytes(decoder))
		value := decoder.u64()
		if _, duplicate := result[key]; duplicate {
			decoder.err = decoder.fail("duplicate public WIPI counter-map key")
			return result
		}
		result[key] = value
	}
	return result
}

func sortedStringKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedUint32Keys[V any](values map[uint32]V) []uint32 {
	keys := make([]uint32, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func cloneByteMap(source map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(source))
	for key, value := range source {
		result[key] = append([]byte(nil), value...)
	}
	return result
}

func cloneResources(source map[string]*wipiResource) map[string]*wipiResource {
	result := make(map[string]*wipiResource, len(source))
	for name, resource := range source {
		if resource == nil {
			continue
		}
		result[name] = &wipiResource{
			id:   resource.id,
			name: resource.name,
			data: append([]byte(nil), resource.data...),
		}
	}
	return result
}

func cloneStringUint32Map(source map[string]uint32) map[string]uint32 {
	result := make(map[string]uint32, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneStringBoolMap(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneFileHandleMap(source map[int32]wipiFileHandle) map[int32]wipiFileHandle {
	result := make(map[int32]wipiFileHandle, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneUint32BoolMap(source map[uint32]bool) map[uint32]bool {
	result := make(map[uint32]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneDatabases(source map[string]*wipiDatabase) map[string]*wipiDatabase {
	result := make(map[string]*wipiDatabase, len(source))
	for key, database := range source {
		clone := &wipiDatabase{
			name:       database.name,
			recordSize: database.recordSize,
			mode:       database.mode,
			nextRecord: database.nextRecord,
			records:    make(map[int32][]byte, len(database.records)),
		}
		for recordID, record := range database.records {
			clone.records[recordID] = append([]byte(nil), record...)
		}
		result[key] = clone
	}
	return result
}

func cloneInt32StringMap(source map[int32]string) map[int32]string {
	result := make(map[int32]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneComponents(source map[uint32]*wipiComponent) map[uint32]*wipiComponent {
	result := make(map[uint32]*wipiComponent, len(source))
	for handle, component := range source {
		clone := *component
		clone.label = append([]byte(nil), component.label...)
		clone.text = append([]byte(nil), component.text...)
		clone.callbacks = make(map[int32]wipiUICallback, len(component.callbacks))
		for index, callback := range component.callbacks {
			clone.callbacks[index] = callback
		}
		clone.menuItems = cloneUIItems(component.menuItems)
		clone.listItems = cloneUIItems(component.listItems)
		result[handle] = &clone
	}
	return result
}

func cloneMediaClips(source map[uint32]*wipiMediaClip) map[uint32]*wipiMediaClip {
	result := make(map[uint32]*wipiMediaClip, len(source))
	for handle, clip := range source {
		clone := *clip
		clone.mediaType = append([]byte(nil), clip.mediaType...)
		clone.data = append([]byte(nil), clip.data...)
		result[handle] = &clone
	}
	return result
}

func cloneInt32BoolMap(source map[int32]bool) map[int32]bool {
	result := make(map[int32]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneByteSlices(source [][]byte) [][]byte {
	result := make([][]byte, len(source))
	for index, value := range source {
		result[index] = append([]byte(nil), value...)
	}
	return result
}

func cloneSerialPorts(source map[int32]*wipiSerialPort) map[int32]*wipiSerialPort {
	result := make(map[int32]*wipiSerialPort, len(source))
	for descriptor, port := range source {
		clone := *port
		clone.data = append([]byte(nil), port.data...)
		result[descriptor] = &clone
	}
	return result
}

func cloneSockets(source map[int32]*wipiSocket) map[int32]*wipiSocket {
	result := make(map[int32]*wipiSocket, len(source))
	for descriptor, socket := range source {
		clone := *socket
		clone.readData = append([]byte(nil), socket.readData...)
		clone.writeData = append([]byte(nil), socket.writeData...)
		result[descriptor] = &clone
	}
	return result
}

func cloneHTTP(source map[int32]*wipiHTTP) map[int32]*wipiHTTP {
	result := make(map[int32]*wipiHTTP, len(source))
	for descriptor, request := range source {
		clone := *request
		clone.url = append([]byte(nil), request.url...)
		clone.method = append([]byte(nil), request.method...)
		clone.request = append([]byte(nil), request.request...)
		clone.response = append([]byte(nil), request.response...)
		clone.properties = cloneByteMap(request.properties)
		result[descriptor] = &clone
	}
	return result
}

func cloneUIItems(source []wipiUIItem) []wipiUIItem {
	result := make([]wipiUIItem, len(source))
	for index, item := range source {
		result[index] = wipiUIItem{
			label: append([]byte(nil), item.label...),
			image: item.image,
		}
	}
	return result
}

func cloneUint32Map(source map[uint32]uint32) map[uint32]uint32 {
	result := make(map[uint32]uint32, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneTimerMap(source map[uint32]wipiTimer) map[uint32]wipiTimer {
	result := make(map[uint32]wipiTimer, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneStringUint64Map(source map[string]uint64) map[string]uint64 {
	result := make(map[string]uint64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func validSavedWIPIPath(name string) bool {
	return name == "/private" || name == "/shared" || name == "/system" ||
		strings.HasPrefix(name, "/private/") ||
		strings.HasPrefix(name, "/shared/") ||
		strings.HasPrefix(name, "/system/")
}
