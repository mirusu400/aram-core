package wipi

import (
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
	wipicatalog "github.com/mirusu400/aram-core/wipi"
)

func ParseState(r *Runtime, decoder *guest.StateDecoder) (*SavedState, error) {
	present := decoder.U8()
	decoder.Reserved(3)
	if decoder.Err != nil {
		return nil, decoder.Err
	}
	if present > 1 {
		return nil, decoder.Fail(fmt.Sprintf("invalid public WIPI runtime flag %d", present))
	}
	if (present == 1) != (r != nil) {
		return nil, decoder.Fail("public WIPI runtime profile mismatch")
	}
	if present == 0 {
		return nil, nil
	}

	saved := &SavedState{
		TickMS:        decoder.U64(),
		ExitRequested: decoder.U8() != 0,
	}
	decoder.Reserved(3)
	saved.exitCode = int32(decoder.U32())
	saved.strtokNext = decoder.U32()
	saved.ScreenHandle = decoder.U32()
	saved.screenPixels = decoder.U32()
	saved.Stats.PresentCount = decoder.U32()
	saved.Stats.APICalls = decoder.U64()
	saved.Stats.ImplementedCalls = decoder.U64()
	saved.Stats.UnimplementedCalls = decoder.U64()
	saved.Stats.LastAPI = string(readWIPIBytes(decoder))
	saved.Stats.LastUnimplemented = string(readWIPIBytes(decoder))
	if decoder.Err != nil {
		return nil, decoder.Err
	}
	if saved.Stats.ImplementedCalls+saved.Stats.UnimplementedCalls != saved.Stats.APICalls {
		return nil, decoder.Fail("public WIPI call counters are inconsistent")
	}

	var err error
	saved.heapAllocations, err = guest.ReadHeapAllocations(
		decoder,
		guest.HeapBase,
		guest.HeapSize,
	)
	if err != nil {
		return nil, err
	}
	allocationSizes := make(map[uint32]uint32, len(saved.heapAllocations))
	for _, block := range saved.heapAllocations {
		allocationSizes[block.Address] = block.Size
	}

	framebufferCount := decoder.U32()
	if framebufferCount > MaxSavedFramebuffers {
		return nil, decoder.Fail(fmt.Sprintf(
			"public WIPI framebuffer count %d exceeds limit",
			framebufferCount,
		))
	}
	saved.Framebuffers = make([]Framebuffer, 0, framebufferCount)
	seenHandles := make(map[uint32]struct{}, framebufferCount)
	for index := uint32(0); index < framebufferCount; index++ {
		framebuffer := Framebuffer{
			Handle:       decoder.U32(),
			Pixels:       decoder.U32(),
			Width:        int(decoder.U32()),
			Height:       int(decoder.U32()),
			BitsPerPixel: int(decoder.U32()),
			owns:         decoder.U8() != 0,
		}
		decoder.Reserved(3)
		bytesPerPixel := uint64(4)
		if framebuffer.BitsPerPixel == 16 {
			bytesPerPixel = 2
		}
		pixelSize := uint64(framebuffer.Width) * uint64(framebuffer.Height) *
			bytesPerPixel
		if framebuffer.Handle == 0 || framebuffer.Pixels == 0 ||
			framebuffer.Width <= 0 || framebuffer.Height <= 0 ||
			framebuffer.Width > 4096 || framebuffer.Height > 4096 ||
			(framebuffer.BitsPerPixel != 16 && framebuffer.BitsPerPixel != 32) ||
			allocationSizes[framebuffer.Handle] < 24 ||
			pixelSize > uint64(allocationSizes[framebuffer.Pixels]) {
			return nil, decoder.Fail(fmt.Sprintf("invalid public WIPI framebuffer %d", index))
		}
		if _, duplicate := seenHandles[framebuffer.Handle]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI framebuffer handle")
		}
		seenHandles[framebuffer.Handle] = struct{}{}
		saved.Framebuffers = append(saved.Framebuffers, framebuffer)
	}

	saved.properties = readWIPIByteMap(decoder)
	saved.shared = readWIPIStringUint32Map(decoder)
	saved.sharedSizes = readWIPIUint32Map(decoder)
	timerCount := decoder.U32()
	if timerCount > MaxSavedEntries {
		return nil, decoder.Fail(fmt.Sprintf("public WIPI timer count %d exceeds limit", timerCount))
	}
	saved.Timers = make(map[uint32]wipiTimer, timerCount)
	for index := uint32(0); index < timerCount; index++ {
		address := decoder.U32()
		timer := wipiTimer{
			Callback:  decoder.U32(),
			Parameter: decoder.U32(),
			Deadline:  decoder.U64(),
		}
		if _, duplicate := saved.Timers[address]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI timer")
		}
		saved.Timers[address] = timer
	}
	resourceCount := decoder.U32()
	if resourceCount > MaxSavedEntries {
		return nil, decoder.Fail(fmt.Sprintf("public WIPI resource count %d exceeds limit", resourceCount))
	}
	saved.Resources = make(map[string]*Resource, resourceCount)
	resourceIDs := make(map[int32]string, resourceCount)
	totalResourceBytes := uint64(0)
	var highestResourceID int32
	for index := uint32(0); index < resourceCount; index++ {
		resource := &Resource{
			Id:   int32(decoder.U32()),
			name: string(readWIPIBytes(decoder)),
			Data: append([]byte(nil), readWIPIBytes(decoder)...),
		}
		totalResourceBytes += uint64(len(resource.Data))
		if resource.Id < 1 || resource.name == "" ||
			len(resource.Data) > int(maxWIPICopy) {
			return nil, decoder.Fail("invalid public WIPI resource")
		}
		if saved.Resources[resource.name] != nil {
			return nil, decoder.Fail("duplicate public WIPI resource name")
		}
		if _, duplicate := resourceIDs[resource.Id]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI resource identifier")
		}
		saved.Resources[resource.name] = resource
		resourceIDs[resource.Id] = resource.name
		highestResourceID = max(highestResourceID, resource.Id)
	}
	if totalResourceBytes > uint64(maxWIPICopy) {
		return nil, decoder.Fail("public WIPI resources exceed state limit")
	}
	saved.nextResource = int32(decoder.U32())
	if saved.nextResource < 1 || saved.nextResource <= highestResourceID {
		return nil, decoder.Fail("invalid public WIPI next resource identifier")
	}
	programCount := decoder.U32()
	if programCount == 0 || programCount > wipiMaxPrograms {
		return nil, decoder.Fail(fmt.Sprintf("invalid public WIPI program count %d", programCount))
	}
	saved.Programs = make(map[int32]*Program, programCount)
	for index := uint32(0); index < programCount; index++ {
		program := &Program{
			Id:          int32(decoder.U32()),
			ParentID:    int32(decoder.U32()),
			programType: int32(decoder.U32()),
			accessLevel: int32(decoder.U32()),
			Running:     decoder.U8() != 0,
		}
		decoder.Reserved(3)
		program.ExecName = string(readWIPIBytes(decoder))
		program.programName = string(readWIPIBytes(decoder))
		program.version = string(readWIPIBytes(decoder))
		program.vendor = string(readWIPIBytes(decoder))
		if _, duplicate := saved.Programs[program.Id]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI program identifier")
		}
		saved.Programs[program.Id] = program
	}
	saved.nextProgram = int32(decoder.U32())
	saved.CurrentProgram = int32(decoder.U32())
	saved.appManager = int32(decoder.U32())
	saved.LastExecuteName = string(readWIPIBytes(decoder))
	executeArgumentCount := decoder.U32()
	if executeArgumentCount > wipiMaxExecuteArguments {
		return nil, decoder.Fail("public WIPI execute argument count exceeds limit")
	}
	saved.LastExecuteArgs = make([]string, 0, executeArgumentCount)
	for index := uint32(0); index < executeArgumentCount; index++ {
		saved.LastExecuteArgs = append(saved.LastExecuteArgs, string(readWIPIBytes(decoder)))
	}
	saved.LastExecuted = int32(decoder.U32())
	if err := validateWIPIProgramState(
		saved.Programs,
		saved.nextProgram,
		saved.CurrentProgram,
		saved.appManager,
		saved.LastExecuteName,
		saved.LastExecuteArgs,
		saved.LastExecuted,
	); err != nil {
		return nil, decoder.Fail(fmt.Sprintf("invalid public WIPI program State: %v", err))
	}
	eventCount := decoder.U32()
	if eventCount > wipiMaxGraphicsEvents {
		return nil, decoder.Fail("public WIPI graphics event count exceeds limit")
	}
	saved.GraphicsEvents = make([]GraphicsEvent, 0, eventCount)
	for index := uint32(0); index < eventCount; index++ {
		saved.GraphicsEvents = append(saved.GraphicsEvents, GraphicsEvent{
			ID:     int32(decoder.U32()),
			Kind:   int32(decoder.U32()),
			Param1: int32(decoder.U32()),
			Param2: int32(decoder.U32()),
		})
	}
	saved.Files = readWIPIFiles(decoder)
	directoryCount := decoder.U32()
	if directoryCount > MaxSavedEntries {
		return nil, decoder.Fail(fmt.Sprintf("public WIPI directory count %d exceeds limit", directoryCount))
	}
	saved.Directories = make(map[string]bool, directoryCount)
	for index := uint32(0); index < directoryCount; index++ {
		directory := string(readWIPIBytes(decoder))
		if directory == "" || saved.Directories[directory] {
			return nil, decoder.Fail("invalid or duplicate public WIPI directory")
		}
		saved.Directories[directory] = true
	}
	saved.FileTimes = readWIPIStringUint32Map(decoder)
	fileHandleCount := decoder.U32()
	if fileHandleCount > MaxSavedEntries {
		return nil, decoder.Fail(fmt.Sprintf("public WIPI file-handle count %d exceeds limit", fileHandleCount))
	}
	saved.fileHandles = make(map[int32]wipiFileHandle, fileHandleCount)
	for index := uint32(0); index < fileHandleCount; index++ {
		descriptor := int32(decoder.U32())
		handle := wipiFileHandle{
			path:   string(readWIPIBytes(decoder)),
			offset: int(decoder.U32()),
		}
		flags := decoder.U8()
		decoder.Reserved(3)
		handle.readable = flags&1 != 0
		handle.writable = flags&2 != 0
		if descriptor < 3 || flags&^uint8(3) != 0 ||
			(!handle.readable && !handle.writable) ||
			handle.offset < 0 || handle.offset > wipiFilesystemCapacity {
			return nil, decoder.Fail("invalid public WIPI file handle")
		}
		if _, duplicate := saved.fileHandles[descriptor]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI file descriptor")
		}
		saved.fileHandles[descriptor] = handle
	}
	saved.nextFile = int32(decoder.U32())
	databaseCount := decoder.U32()
	if databaseCount > MaxSavedEntries {
		return nil, decoder.Fail(fmt.Sprintf("public WIPI database count %d exceeds limit", databaseCount))
	}
	saved.Databases = make(map[string]*Database, databaseCount)
	totalDatabaseBytes := uint64(0)
	for index := uint32(0); index < databaseCount; index++ {
		key := string(readWIPIBytes(decoder))
		database := &Database{
			Name:       string(readWIPIBytes(decoder)),
			RecordSize: decoder.U32(),
			Mode:       int32(decoder.U32()),
			NextRecord: int32(decoder.U32()),
		}
		recordCount := decoder.U32()
		if key == "" || database.Name == "" ||
			database.RecordSize == 0 || database.RecordSize > maxWIPIString ||
			database.NextRecord < 0 || recordCount > MaxSavedEntries {
			return nil, decoder.Fail("invalid public WIPI database metadata")
		}
		database.Records = make(map[int32][]byte, recordCount)
		var highestRecord int32 = -1
		for recordIndex := uint32(0); recordIndex < recordCount; recordIndex++ {
			recordID := int32(decoder.U32())
			if recordID < 0 {
				return nil, decoder.Fail("invalid public WIPI record identifier")
			}
			if _, duplicate := database.Records[recordID]; duplicate {
				return nil, decoder.Fail("duplicate public WIPI record identifier")
			}
			record := append([]byte(nil), decoder.Bytes(int(database.RecordSize))...)
			database.Records[recordID] = record
			totalDatabaseBytes += uint64(len(record))
			highestRecord = max(highestRecord, recordID)
		}
		if database.NextRecord <= highestRecord {
			return nil, decoder.Fail("public WIPI next record identifier is inconsistent")
		}
		if _, duplicate := saved.Databases[key]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI database")
		}
		saved.Databases[key] = database
	}
	if totalDatabaseBytes > uint64(64<<20) {
		return nil, decoder.Fail("public WIPI databases exceed state limit")
	}
	databaseHandleCount := decoder.U32()
	if databaseHandleCount > MaxSavedEntries {
		return nil, decoder.Fail(fmt.Sprintf(
			"public WIPI database-handle count %d exceeds limit",
			databaseHandleCount,
		))
	}
	saved.DatabaseHandles = make(map[int32]string, databaseHandleCount)
	for index := uint32(0); index < databaseHandleCount; index++ {
		handle := int32(decoder.U32())
		key := string(readWIPIBytes(decoder))
		if handle < 1 || saved.Databases[key] == nil {
			return nil, decoder.Fail("invalid public WIPI database handle")
		}
		if _, duplicate := saved.DatabaseHandles[handle]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI database handle")
		}
		saved.DatabaseHandles[handle] = key
	}
	saved.NextDatabase = int32(decoder.U32())
	contextCount := decoder.U32()
	if contextCount > MaxSavedEntries {
		return nil, decoder.Fail(fmt.Sprintf("public WIPI UI context count %d exceeds limit", contextCount))
	}
	saved.UicContexts = make(map[uint32]bool, contextCount)
	for index := uint32(0); index < contextCount; index++ {
		handle := decoder.U32()
		if handle == 0 || allocationSizes[handle] < 64 || saved.UicContexts[handle] {
			return nil, decoder.Fail("invalid public WIPI UI context")
		}
		saved.UicContexts[handle] = true
	}
	saved.UicClasses = readWIPIStringUint32Map(decoder)
	classNames := make(map[uint32]string, len(saved.UicClasses))
	for name, handle := range saved.UicClasses {
		if name == "" || handle == 0 || allocationSizes[handle] < 16 {
			return nil, decoder.Fail("invalid public WIPI UI class")
		}
		if _, duplicate := classNames[handle]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI UI class handle")
		}
		classNames[handle] = name
	}
	componentCount := decoder.U32()
	if componentCount > MaxSavedEntries {
		return nil, decoder.Fail(fmt.Sprintf("public WIPI component count %d exceeds limit", componentCount))
	}
	saved.UicComponents = make(map[uint32]*Component, componentCount)
	for index := uint32(0); index < componentCount; index++ {
		component := &Component{
			Handle:    decoder.U32(),
			ClassName: string(readWIPIBytes(decoder)),
			x:         int32(decoder.U32()),
			y:         int32(decoder.U32()),
			Width:     int32(decoder.U32()),
			Height:    int32(decoder.U32()),
			Enabled:   decoder.U8() != 0,
		}
		decoder.Reserved(3)
		component.eventHandler = decoder.U32()
		component.font = int32(decoder.U32())
		component.foreground = decoder.U32()
		component.background = decoder.U32()
		component.Label = append([]byte(nil), readWIPIBytes(decoder)...)
		component.alignment = int32(decoder.U32())
		component.timeMask = int32(decoder.U32())
		copy(component.timeData[:], decoder.Bytes(len(component.timeData)))
		component.ActiveMenu = int32(decoder.U32())
		component.ActiveList = int32(decoder.U32())
		component.MaxText = int32(decoder.U32())
		component.text = append([]byte(nil), readWIPIBytes(decoder)...)
		if component.Handle == 0 || allocationSizes[component.Handle] < 128 ||
			component.ClassName == "" ||
			saved.UicClasses[component.ClassName] == 0 ||
			component.Width < 0 || component.Height < 0 ||
			component.MaxText < 0 || component.MaxText > int32(maxWIPIString) ||
			len(component.text) > int(component.MaxText) {
			return nil, decoder.Fail("invalid public WIPI component metadata")
		}
		callbackCount := decoder.U32()
		if callbackCount > MaxSavedEntries {
			return nil, decoder.Fail("public WIPI component callback count exceeds limit")
		}
		component.Callbacks = make(map[int32]UICallback, callbackCount)
		for callbackIndex := uint32(0); callbackIndex < callbackCount; callbackIndex++ {
			selector := int32(decoder.U32())
			callback := UICallback{
				procedure: decoder.U32(),
				client:    decoder.U32(),
			}
			if _, duplicate := component.Callbacks[selector]; duplicate {
				return nil, decoder.Fail("duplicate public WIPI component callback")
			}
			component.Callbacks[selector] = callback
		}
		component.menuItems = readWIPIItems(decoder)
		component.listItems = readWIPIItems(decoder)
		if decoder.Err != nil {
			return nil, decoder.Err
		}
		if component.ActiveMenu < -1 || component.ActiveMenu >= int32(len(component.menuItems)) ||
			component.ActiveList < -1 || component.ActiveList >= int32(len(component.listItems)) {
			return nil, decoder.Fail("public WIPI component active item is invalid")
		}
		if _, duplicate := saved.UicComponents[component.Handle]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI component handle")
		}
		saved.UicComponents[component.Handle] = component
	}
	repaintCount := decoder.U32()
	if repaintCount > wipiMaxUICRepaints {
		return nil, decoder.Fail("public WIPI repaint count exceeds limit")
	}
	saved.UicRepaints = make([]UICRepaint, 0, repaintCount)
	for index := uint32(0); index < repaintCount; index++ {
		repaint := UICRepaint{
			Component: decoder.U32(),
			X:         int32(decoder.U32()),
			Y:         int32(decoder.U32()),
			Width:     int32(decoder.U32()),
			Height:    int32(decoder.U32()),
		}
		if saved.UicComponents[repaint.Component] == nil {
			return nil, decoder.Fail("public WIPI repaint refers to an absent component")
		}
		saved.UicRepaints = append(saved.UicRepaints, repaint)
	}
	saved.mediaVolume = int32(decoder.U32())
	saved.vibratorLevel = int32(decoder.U32())
	saved.vibratorTimeout = int32(decoder.U32())
	for index := range saved.backlight {
		saved.backlight[index] = int32(decoder.U32())
	}
	saved.ledState = int32(decoder.U32())
	if saved.mediaVolume < 0 || saved.mediaVolume > 100 || saved.vibratorTimeout < 0 {
		return nil, decoder.Fail("invalid public WIPI media global state")
	}
	muteCount := decoder.U32()
	if muteCount > MaxSavedEntries {
		return nil, decoder.Fail("public WIPI mute-state count exceeds limit")
	}
	saved.mediaMute = make(map[int32]bool, muteCount)
	for index := uint32(0); index < muteCount; index++ {
		source := int32(decoder.U32())
		muted := decoder.U8()
		decoder.Reserved(3)
		if muted > 1 {
			return nil, decoder.Fail("invalid public WIPI mute state")
		}
		if _, duplicate := saved.mediaMute[source]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI mute source")
		}
		saved.mediaMute[source] = muted == 1
	}
	clipCount := decoder.U32()
	if clipCount > MaxSavedEntries {
		return nil, decoder.Fail("public WIPI media-clip count exceeds limit")
	}
	saved.MediaClips = make(map[uint32]*wipiMediaClip, clipCount)
	for index := uint32(0); index < clipCount; index++ {
		clip := &wipiMediaClip{
			Handle:    decoder.U32(),
			mediaType: append([]byte(nil), readWIPIBytes(decoder)...),
			capacity:  int32(decoder.U32()),
			Callback:  decoder.U32(),
			Data:      append([]byte(nil), readWIPIBytes(decoder)...),
			position:  int32(decoder.U32()),
			volume:    int32(decoder.U32()),
			State:     decoder.U8(),
			Repeat:    decoder.U8() != 0,
		}
		decoder.Reserved(2)
		if clip.Handle == 0 || allocationSizes[clip.Handle] < 64 ||
			clip.capacity < 0 || clip.capacity > int32(maxWIPIString) ||
			(clip.capacity > 0 && len(clip.Data) > int(clip.capacity)) ||
			clip.volume < 0 || clip.volume > 100 || clip.State > 3 {
			return nil, decoder.Fail("invalid public WIPI media clip")
		}
		if _, duplicate := saved.MediaClips[clip.Handle]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI media clip")
		}
		saved.MediaClips[clip.Handle] = clip
	}
	phoneCount := decoder.U32()
	if phoneCount > MaxSavedEntries {
		return nil, decoder.Fail("public WIPI phone-request count exceeds limit")
	}
	saved.phoneRequests = make([][]byte, 0, phoneCount)
	for index := uint32(0); index < phoneCount; index++ {
		saved.phoneRequests = append(saved.phoneRequests, append([]byte(nil), readWIPIBytes(decoder)...))
	}
	serialCount := decoder.U32()
	if serialCount > MaxSavedEntries {
		return nil, decoder.Fail("public WIPI serial-port count exceeds limit")
	}
	saved.serialPorts = make(map[int32]*wipiSerialPort, serialCount)
	for index := uint32(0); index < serialCount; index++ {
		port := &wipiSerialPort{
			descriptor:     int32(decoder.U32()),
			port:           int32(decoder.U32()),
			Data:           append([]byte(nil), readWIPIBytes(decoder)...),
			readCallback:   decoder.U32(),
			readParameter:  decoder.U32(),
			writeCallback:  decoder.U32(),
			writeParameter: decoder.U32(),
		}
		if port.descriptor < 1 || port.port != 0 {
			return nil, decoder.Fail("invalid public WIPI serial port")
		}
		if _, duplicate := saved.serialPorts[port.descriptor]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI serial descriptor")
		}
		saved.serialPorts[port.descriptor] = port
	}
	saved.nextSerial = int32(decoder.U32())
	if saved.nextSerial < 1 {
		return nil, decoder.Fail("invalid public WIPI next serial descriptor")
	}
	saved.networkConnected = decoder.U8() != 0
	decoder.Reserved(3)
	saved.networkCallback = decoder.U32()
	saved.networkParameter = decoder.U32()
	socketCount := decoder.U32()
	if socketCount > MaxSavedEntries {
		return nil, decoder.Fail("public WIPI socket count exceeds limit")
	}
	saved.sockets = make(map[int32]*wipiSocket, socketCount)
	for index := uint32(0); index < socketCount; index++ {
		socket := &wipiSocket{
			descriptor: int32(decoder.U32()),
			domain:     int32(decoder.U32()),
			socketType: int32(decoder.U32()),
			address:    decoder.U32(),
			port:       uint16(decoder.U32()),
			connected:  decoder.U8() != 0,
		}
		decoder.Reserved(3)
		socket.readData = append([]byte(nil), readWIPIBytes(decoder)...)
		socket.writeData = append([]byte(nil), readWIPIBytes(decoder)...)
		socket.readCallback = decoder.U32()
		socket.readParameter = decoder.U32()
		socket.writeCallback = decoder.U32()
		socket.writeParameter = decoder.U32()
		if socket.descriptor < 1 {
			return nil, decoder.Fail("invalid public WIPI socket")
		}
		if _, duplicate := saved.sockets[socket.descriptor]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI socket")
		}
		saved.sockets[socket.descriptor] = socket
	}
	saved.nextSocket = int32(decoder.U32())
	if saved.nextSocket < 1 {
		return nil, decoder.Fail("invalid public WIPI next socket descriptor")
	}
	httpCount := decoder.U32()
	if httpCount > MaxSavedEntries {
		return nil, decoder.Fail("public WIPI HTTP request count exceeds limit")
	}
	saved.http = make(map[int32]*wipiHTTP, httpCount)
	for index := uint32(0); index < httpCount; index++ {
		request := &wipiHTTP{
			descriptor: int32(decoder.U32()),
			url:        append([]byte(nil), readWIPIBytes(decoder)...),
			method:     append([]byte(nil), readWIPIBytes(decoder)...),
			request:    append([]byte(nil), readWIPIBytes(decoder)...),
			properties: readWIPIByteMap(decoder),
			proxyHost:  decoder.U32(),
			proxyPort:  uint16(decoder.U32()),
			connected:  decoder.U8() != 0,
		}
		decoder.Reserved(3)
		request.response = append([]byte(nil), readWIPIBytes(decoder)...)
		request.code = int32(decoder.U32())
		if request.descriptor < 1 || len(request.url) == 0 ||
			len(request.properties) > MaxSavedEntries {
			return nil, decoder.Fail("invalid public WIPI HTTP request")
		}
		if _, duplicate := saved.http[request.descriptor]; duplicate {
			return nil, decoder.Fail("duplicate public WIPI HTTP descriptor")
		}
		saved.http[request.descriptor] = request
	}
	saved.nextHTTP = int32(decoder.U32())
	if saved.nextHTTP < 1 {
		return nil, decoder.Fail("invalid public WIPI next HTTP descriptor")
	}
	callbackCount := decoder.U32()
	if callbackCount > MaxSavedEntries {
		return nil, decoder.Fail("public WIPI pending callback count exceeds limit")
	}
	saved.PendingCallbacks = make([]GuestCallback, 0, callbackCount)
	for index := uint32(0); index < callbackCount; index++ {
		callback := GuestCallback{Procedure: decoder.U32()}
		for argument := range callback.Args {
			callback.Args[argument] = decoder.U32()
		}
		if callback.Procedure == 0 {
			return nil, decoder.Fail("public WIPI pending callback is null")
		}
		saved.PendingCallbacks = append(saved.PendingCallbacks, callback)
	}
	saved.Observed = readWIPIStringUint64Map(decoder)
	saved.Unimplemented = readWIPIStringUint64Map(decoder)
	logCount := decoder.U32()
	if logCount > MaxSavedLogs {
		return nil, decoder.Fail(fmt.Sprintf("public WIPI log count %d exceeds limit", logCount))
	}
	saved.Logs = make([]string, 0, logCount)
	for index := uint32(0); index < logCount; index++ {
		saved.Logs = append(saved.Logs, string(readWIPIBytes(decoder)))
	}
	if decoder.Err != nil {
		return nil, decoder.Err
	}
	if len(saved.Observed) > MaxSavedEntries ||
		len(saved.Unimplemented) > MaxSavedEntries ||
		len(saved.properties) > MaxSavedEntries ||
		len(saved.shared) > MaxSavedEntries ||
		len(saved.sharedSizes) > MaxSavedEntries {
		return nil, decoder.Fail("public WIPI map exceeds entry limit")
	}
	if (saved.ScreenHandle == 0) != (saved.screenPixels == 0) {
		return nil, decoder.Fail("public WIPI screen handles are inconsistent")
	}
	if saved.ScreenHandle != 0 {
		framebuffer, ok := seenHandles[saved.ScreenHandle]
		_ = framebuffer
		if !ok {
			return nil, decoder.Fail("public WIPI screen framebuffer is absent")
		}
		found := false
		for _, current := range saved.Framebuffers {
			if current.Handle == saved.ScreenHandle && current.Pixels == saved.screenPixels {
				found = true
				break
			}
		}
		if !found {
			return nil, decoder.Fail("public WIPI screen pixels do not match its framebuffer")
		}
	}
	for key, address := range saved.shared {
		size, ok := saved.sharedSizes[address]
		if key == "" || !ok || allocationSizes[address] < size {
			return nil, decoder.Fail("public WIPI shared buffer metadata is inconsistent")
		}
	}
	if !saved.Directories["/private"] ||
		!saved.Directories["/shared"] ||
		!saved.Directories["/system"] ||
		saved.nextFile < 3 {
		return nil, decoder.Fail("public WIPI filesystem roots or next descriptor are invalid")
	}
	if saved.NextDatabase < 1 {
		return nil, decoder.Fail("public WIPI next database handle is invalid")
	}
	totalFileBytes := 0
	for name, data := range saved.Files {
		if !validSavedWIPIPath(name) {
			return nil, decoder.Fail("invalid public WIPI file path")
		}
		totalFileBytes += len(data)
	}
	if totalFileBytes > wipiFilesystemCapacity {
		return nil, decoder.Fail("public WIPI filesystem exceeds capacity")
	}
	for directory := range saved.Directories {
		if !validSavedWIPIPath(directory) {
			return nil, decoder.Fail("invalid public WIPI directory path")
		}
	}
	for _, handle := range saved.fileHandles {
		if _, ok := saved.Files[handle.path]; !ok {
			return nil, decoder.Fail("public WIPI file handle refers to an absent file")
		}
	}

	saved.systemMemory = append([]byte(nil), decoder.Bytes(int(wipicatalog.SystemSize))...)
	saved.heapMemory = append([]byte(nil), decoder.Bytes(int(guest.HeapSize))...)
	saved.ServiceOwner = shared.OwnerID(decoder.U32())
	saved.serviceName = string(readWIPIBytes(decoder))
	serviceSize := decoder.U64()
	if serviceSize > shared.MaxServicesStateBytes ||
		serviceSize > uint64(decoder.Reader.Len()) ||
		serviceSize > uint64(^uint(0)>>1) {
		return nil, decoder.Fail("invalid public WIPI shared-service state size")
	}
	saved.Services = append([]byte(nil), decoder.Bytes(int(serviceSize))...)
	saved.surfaceServices = readWIPIUint32ServiceMap(decoder)
	saved.assetServices = readWIPIUint32ServiceMap(decoder)
	saved.TimerServices = readWIPIUint32ServiceMap(decoder)
	saved.fileServices = readWIPIInt32ServiceMap(decoder)
	saved.DatabaseServices = readWIPIStringServiceMap(decoder)
	saved.MediaServices = readWIPIUint32ServiceMap(decoder)
	saved.serialServices = readWIPIInt32ServiceMap(decoder)
	saved.socketServices = readWIPIInt32ServiceMap(decoder)
	saved.httpServices = readWIPIInt32ServiceMap(decoder)
	if decoder.Err != nil {
		return nil, decoder.Err
	}
	if err := r.validateSavedServices(saved); err != nil {
		return nil, decoder.Fail(fmt.Sprintf("invalid public WIPI shared Services: %v", err))
	}
	return saved, nil
}
