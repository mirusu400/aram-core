package wipi

import (
	"fmt"
	"github.com/mirusu400/aram-core/cpu"
	"sort"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
	wipicatalog "github.com/mirusu400/aram-core/wipi"
)

const (
	MaxSavedFramebuffers = 1024
	MaxSavedEntries      = 4096
	MaxSavedLogs         = 4096
	// Bundled package resources are trusted content read straight from the
	// title's JAR and can number in the thousands (아니마 ships 5498 entries),
	// so they use a higher cap than the general per-collection sanity bound.
	// Total resource bytes stay limited by maxWIPICopy, so this only relaxes
	// the entry count, not the memory footprint.
	MaxResourceEntries = 1 << 16
)

type SavedState struct {
	TickMS        uint64
	ExitRequested bool
	exitCode      int32
	strtokNext    uint32
	ScreenHandle  uint32
	screenPixels  uint32
	Stats         WIPIFrameStats

	heapAllocations  []guest.Block
	Framebuffers     []Framebuffer
	properties       map[string][]byte
	shared           map[string]uint32
	sharedSizes      map[uint32]uint32
	Timers           map[uint32]wipiTimer
	Resources        map[string]*Resource
	nextResource     int32
	Programs         map[int32]*Program
	nextProgram      int32
	CurrentProgram   int32
	appManager       int32
	LastExecuteName  string
	LastExecuteArgs  []string
	LastExecuted     int32
	GraphicsEvents   []GraphicsEvent
	Files            map[string][]byte
	Directories      map[string]bool
	FileTimes        map[string]uint32
	fileHandles      map[int32]wipiFileHandle
	nextFile         int32
	Databases        map[string]*Database
	DatabaseHandles  map[int32]string
	NextDatabase     int32
	UicContexts      map[uint32]bool
	UicClasses       map[string]uint32
	UicComponents    map[uint32]*Component
	UicRepaints      []UICRepaint
	MediaClips       map[uint32]*wipiMediaClip
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
	PendingCallbacks []GuestCallback
	Observed         map[string]uint64
	Unimplemented    map[string]uint64
	Logs             []string

	systemMemory []byte
	heapMemory   []byte

	ServiceOwner      shared.OwnerID
	serviceName       string
	Services          []byte
	surfaceServices   map[uint32]shared.ServiceID
	assetServices     map[uint32]shared.ServiceID
	TimerServices     map[uint32]shared.ServiceID
	fileServices      map[int32]shared.ServiceID
	DatabaseServices  map[string]shared.ServiceID
	MediaServices     map[uint32]shared.ServiceID
	serialServices    map[int32]shared.ServiceID
	socketServices    map[int32]shared.ServiceID
	httpServices      map[int32]shared.ServiceID
	validatedServices *shared.Services
}

func WriteState(r *Runtime, backend cpu.Backend, writer *guest.StateWriter) error {
	runtime := r
	if runtime == nil {
		writer.U8(0)
		writer.Write([]byte{0, 0, 0})
		return nil
	}
	serviceState, err := runtime.prepareServicesForSave()
	if err != nil {
		return fmt.Errorf("save public WIPI shared services: %w", err)
	}
	if len(runtime.Heap.Allocations) > guest.MaxSavedHeapAllocations ||
		len(runtime.Framebuffers) > MaxSavedFramebuffers ||
		len(runtime.properties) > MaxSavedEntries ||
		len(runtime.shared) > MaxSavedEntries ||
		len(runtime.sharedSizes) > MaxSavedEntries ||
		len(runtime.Timers) > MaxSavedEntries ||
		len(runtime.Resources) > MaxResourceEntries ||
		len(runtime.Programs) > wipiMaxPrograms ||
		len(runtime.LastExecuteArgs) > wipiMaxExecuteArguments ||
		len(runtime.GraphicsEvents) > wipiMaxGraphicsEvents ||
		len(runtime.Files) > MaxSavedEntries ||
		len(runtime.Directories) > MaxSavedEntries ||
		len(runtime.FileTimes) > MaxSavedEntries ||
		len(runtime.fileHandles) > MaxSavedEntries ||
		len(runtime.Databases) > MaxSavedEntries ||
		len(runtime.DatabaseHandles) > MaxSavedEntries ||
		len(runtime.UicContexts) > MaxSavedEntries ||
		len(runtime.UicClasses) > MaxSavedEntries ||
		len(runtime.UicComponents) > MaxSavedEntries ||
		len(runtime.UicRepaints) > wipiMaxUICRepaints ||
		len(runtime.MediaClips) > MaxSavedEntries ||
		len(runtime.mediaMute) > MaxSavedEntries ||
		len(runtime.phoneRequests) > MaxSavedEntries ||
		len(runtime.serialPorts) > MaxSavedEntries ||
		len(runtime.sockets) > MaxSavedEntries ||
		len(runtime.http) > MaxSavedEntries ||
		len(runtime.PendingCallbacks) > MaxSavedEntries ||
		len(runtime.Observed) > MaxSavedEntries ||
		len(runtime.Unimplemented) > MaxSavedEntries ||
		len(runtime.Logs) > MaxSavedLogs {
		return fmt.Errorf("save public WIPI runtime: metadata exceeds format limits")
	}

	writer.U8(1)
	writer.Write([]byte{0, 0, 0})
	writer.U64(runtime.TickMS)
	if runtime.ExitRequested {
		writer.U8(1)
	} else {
		writer.U8(0)
	}
	writer.Write([]byte{0, 0, 0})
	writer.U32(uint32(runtime.exitCode))
	writer.U32(runtime.strtokNext)
	writer.U32(runtime.ScreenHandle)
	writer.U32(runtime.screenPixels)
	writer.U32(runtime.Stats.PresentCount)
	writer.U64(runtime.Stats.APICalls)
	writer.U64(runtime.Stats.ImplementedCalls)
	writer.U64(runtime.Stats.UnimplementedCalls)
	writeWIPIBytes(writer, []byte(runtime.Stats.LastAPI))
	writeWIPIBytes(writer, []byte(runtime.Stats.LastUnimplemented))

	guest.WriteHeapAllocations(writer, runtime.Heap.Allocations)
	handles := guest.SortedUint32Keys(runtime.Framebuffers)
	writer.U32(uint32(len(handles)))
	for _, handle := range handles {
		framebuffer := runtime.Framebuffers[handle]
		writer.U32(framebuffer.Handle)
		writer.U32(framebuffer.Pixels)
		writer.U32(uint32(framebuffer.Width))
		writer.U32(uint32(framebuffer.Height))
		writer.U32(uint32(framebuffer.BitsPerPixel))
		if framebuffer.owns {
			writer.U8(1)
		} else {
			writer.U8(0)
		}
		writer.Write([]byte{0, 0, 0})
	}
	writeWIPIByteMap(writer, runtime.properties)
	writeWIPIStringUint32Map(writer, runtime.shared)
	writeWIPIUint32Map(writer, runtime.sharedSizes)

	timerAddresses := guest.SortedUint32Keys(runtime.Timers)
	writer.U32(uint32(len(timerAddresses)))
	for _, address := range timerAddresses {
		timer := runtime.Timers[address]
		writer.U32(address)
		writer.U32(timer.Callback)
		writer.U32(timer.Parameter)
		writer.U64(timer.Deadline)
	}
	resourceNames := guest.SortedStringKeys(runtime.Resources)
	if len(runtime.ResourceIDs) != len(runtime.Resources) {
		return fmt.Errorf("save public WIPI Resources: identifier index is inconsistent")
	}
	writer.U32(uint32(len(resourceNames)))
	var totalResourceBytes uint64
	var highestResourceID int32
	for _, name := range resourceNames {
		resource := runtime.Resources[name]
		if resource == nil || resource.Id < 1 || resource.name != name ||
			runtime.ResourceIDs[resource.Id] != name ||
			len(resource.Data) > int(maxWIPICopy) {
			return fmt.Errorf("save public WIPI resource %q: inconsistent metadata", name)
		}
		totalResourceBytes += uint64(len(resource.Data))
		highestResourceID = max(highestResourceID, resource.Id)
		writer.U32(uint32(resource.Id))
		writeWIPIBytes(writer, []byte(resource.name))
		writeWIPIBytes(writer, resource.Data)
	}
	if totalResourceBytes > uint64(maxWIPICopy) {
		return fmt.Errorf("save public WIPI resources: data exceeds format limit")
	}
	if runtime.nextResource < 1 || runtime.nextResource <= highestResourceID {
		return fmt.Errorf("save public WIPI Resources: next identifier is inconsistent")
	}
	writer.U32(uint32(runtime.nextResource))
	programIDs := sortedWIPIProgramIDs(runtime.Programs)
	if err := validateWIPIProgramState(
		runtime.Programs,
		runtime.nextProgram,
		runtime.CurrentProgram,
		runtime.appManager,
		runtime.LastExecuteName,
		runtime.LastExecuteArgs,
		runtime.LastExecuted,
	); err != nil {
		return fmt.Errorf("save public WIPI programs: %w", err)
	}
	writer.U32(uint32(len(programIDs)))
	for _, id := range programIDs {
		program := runtime.Programs[id]
		writer.U32(uint32(program.Id))
		writer.U32(uint32(program.ParentID))
		writer.U32(uint32(program.programType))
		writer.U32(uint32(program.accessLevel))
		if program.Running {
			writer.U8(1)
		} else {
			writer.U8(0)
		}
		writer.Write([]byte{0, 0, 0})
		writeWIPIBytes(writer, []byte(program.ExecName))
		writeWIPIBytes(writer, []byte(program.programName))
		writeWIPIBytes(writer, []byte(program.version))
		writeWIPIBytes(writer, []byte(program.vendor))
	}
	writer.U32(uint32(runtime.nextProgram))
	writer.U32(uint32(runtime.CurrentProgram))
	writer.U32(uint32(runtime.appManager))
	writeWIPIBytes(writer, []byte(runtime.LastExecuteName))
	writer.U32(uint32(len(runtime.LastExecuteArgs)))
	for _, argument := range runtime.LastExecuteArgs {
		writeWIPIBytes(writer, []byte(argument))
	}
	writer.U32(uint32(runtime.LastExecuted))
	writer.U32(uint32(len(runtime.GraphicsEvents)))
	for _, event := range runtime.GraphicsEvents {
		writer.U32(uint32(event.ID))
		writer.U32(uint32(event.Kind))
		writer.U32(uint32(event.Param1))
		writer.U32(uint32(event.Param2))
	}
	writeWIPIFiles(writer, runtime.Files)
	directories := guest.SortedStringKeys(runtime.Directories)
	writer.U32(uint32(len(directories)))
	for _, directory := range directories {
		writeWIPIBytes(writer, []byte(directory))
	}
	writeWIPIStringUint32Map(writer, runtime.FileTimes)
	fileDescriptors := make([]int, 0, len(runtime.fileHandles))
	for descriptor := range runtime.fileHandles {
		fileDescriptors = append(fileDescriptors, int(descriptor))
	}
	sort.Ints(fileDescriptors)
	writer.U32(uint32(len(fileDescriptors)))
	for _, rawDescriptor := range fileDescriptors {
		descriptor := int32(rawDescriptor)
		handle := runtime.fileHandles[descriptor]
		writer.U32(uint32(descriptor))
		writeWIPIBytes(writer, []byte(handle.path))
		writer.U32(uint32(handle.offset))
		var flags uint8
		if handle.readable {
			flags |= 1
		}
		if handle.writable {
			flags |= 2
		}
		writer.U8(flags)
		writer.Write([]byte{0, 0, 0})
	}
	writer.U32(uint32(runtime.nextFile))
	databaseKeys := guest.SortedStringKeys(runtime.Databases)
	writer.U32(uint32(len(databaseKeys)))
	for _, key := range databaseKeys {
		database := runtime.Databases[key]
		writeWIPIBytes(writer, []byte(key))
		writeWIPIBytes(writer, []byte(database.Name))
		writer.U32(database.RecordSize)
		writer.U32(uint32(database.Mode))
		writer.U32(uint32(database.NextRecord))
		recordIDs := make([]int, 0, len(database.Records))
		for recordID := range database.Records {
			recordIDs = append(recordIDs, int(recordID))
		}
		sort.Ints(recordIDs)
		writer.U32(uint32(len(recordIDs)))
		for _, rawRecordID := range recordIDs {
			recordID := int32(rawRecordID)
			writer.U32(uint32(recordID))
			writer.Write(database.Records[recordID])
		}
	}
	databaseHandles := make([]int, 0, len(runtime.DatabaseHandles))
	for handle := range runtime.DatabaseHandles {
		databaseHandles = append(databaseHandles, int(handle))
	}
	sort.Ints(databaseHandles)
	writer.U32(uint32(len(databaseHandles)))
	for _, rawHandle := range databaseHandles {
		handle := int32(rawHandle)
		writer.U32(uint32(handle))
		writeWIPIBytes(writer, []byte(runtime.DatabaseHandles[handle]))
	}
	writer.U32(uint32(runtime.NextDatabase))
	contextHandles := guest.SortedUint32Keys(runtime.UicContexts)
	writer.U32(uint32(len(contextHandles)))
	for _, handle := range contextHandles {
		writer.U32(handle)
	}
	writeWIPIStringUint32Map(writer, runtime.UicClasses)
	componentHandles := guest.SortedUint32Keys(runtime.UicComponents)
	writer.U32(uint32(len(componentHandles)))
	for _, handle := range componentHandles {
		component := runtime.UicComponents[handle]
		if len(component.Callbacks) > MaxSavedEntries ||
			len(component.menuItems) > MaxSavedEntries ||
			len(component.listItems) > MaxSavedEntries ||
			len(component.Label) > int(maxWIPIString) ||
			len(component.text) > int(maxWIPIString) {
			return fmt.Errorf("save public WIPI component: metadata exceeds format limits")
		}
		writer.U32(component.Handle)
		writeWIPIBytes(writer, []byte(component.ClassName))
		for _, value := range []int32{
			component.x,
			component.y,
			component.Width,
			component.Height,
		} {
			writer.U32(uint32(value))
		}
		if component.Enabled {
			writer.U8(1)
		} else {
			writer.U8(0)
		}
		writer.Write([]byte{0, 0, 0})
		writer.U32(component.eventHandler)
		writer.U32(uint32(component.font))
		writer.U32(component.foreground)
		writer.U32(component.background)
		writeWIPIBytes(writer, component.Label)
		writer.U32(uint32(component.alignment))
		writer.U32(uint32(component.timeMask))
		writer.Write(component.timeData[:])
		writer.U32(uint32(component.ActiveMenu))
		writer.U32(uint32(component.ActiveList))
		writer.U32(uint32(component.MaxText))
		writeWIPIBytes(writer, component.text)

		callbackIndices := make([]int, 0, len(component.Callbacks))
		for index := range component.Callbacks {
			callbackIndices = append(callbackIndices, int(index))
		}
		sort.Ints(callbackIndices)
		writer.U32(uint32(len(callbackIndices)))
		for _, rawIndex := range callbackIndices {
			index := int32(rawIndex)
			callback := component.Callbacks[index]
			writer.U32(uint32(index))
			writer.U32(callback.procedure)
			writer.U32(callback.client)
		}
		writeWIPIItems(writer, component.menuItems)
		writeWIPIItems(writer, component.listItems)
	}
	writer.U32(uint32(len(runtime.UicRepaints)))
	for _, repaint := range runtime.UicRepaints {
		writer.U32(repaint.Component)
		writer.U32(uint32(repaint.X))
		writer.U32(uint32(repaint.Y))
		writer.U32(uint32(repaint.Width))
		writer.U32(uint32(repaint.Height))
	}
	writer.U32(uint32(runtime.mediaVolume))
	writer.U32(uint32(runtime.vibratorLevel))
	writer.U32(uint32(runtime.vibratorTimeout))
	for _, value := range runtime.backlight {
		writer.U32(uint32(value))
	}
	writer.U32(uint32(runtime.ledState))
	muteSources := make([]int, 0, len(runtime.mediaMute))
	for source := range runtime.mediaMute {
		muteSources = append(muteSources, int(source))
	}
	sort.Ints(muteSources)
	writer.U32(uint32(len(muteSources)))
	for _, rawSource := range muteSources {
		source := int32(rawSource)
		writer.U32(uint32(source))
		if runtime.mediaMute[source] {
			writer.U8(1)
		} else {
			writer.U8(0)
		}
		writer.Write([]byte{0, 0, 0})
	}
	clipHandles := guest.SortedUint32Keys(runtime.MediaClips)
	writer.U32(uint32(len(clipHandles)))
	for _, handle := range clipHandles {
		clip := runtime.MediaClips[handle]
		writer.U32(clip.Handle)
		writeWIPIBytes(writer, clip.mediaType)
		writer.U32(uint32(clip.capacity))
		writer.U32(clip.Callback)
		writeWIPIBytes(writer, clip.Data)
		writer.U32(uint32(clip.position))
		writer.U32(uint32(clip.volume))
		writer.U8(clip.State)
		if clip.Repeat {
			writer.U8(1)
		} else {
			writer.U8(0)
		}
		writer.Write([]byte{0, 0})
	}
	writer.U32(uint32(len(runtime.phoneRequests)))
	for _, number := range runtime.phoneRequests {
		writeWIPIBytes(writer, number)
	}
	serialDescriptors := make([]int, 0, len(runtime.serialPorts))
	for descriptor := range runtime.serialPorts {
		serialDescriptors = append(serialDescriptors, int(descriptor))
	}
	sort.Ints(serialDescriptors)
	writer.U32(uint32(len(serialDescriptors)))
	for _, rawDescriptor := range serialDescriptors {
		descriptor := int32(rawDescriptor)
		port := runtime.serialPorts[descriptor]
		writer.U32(uint32(port.descriptor))
		writer.U32(uint32(port.port))
		writeWIPIBytes(writer, port.Data)
		writer.U32(port.readCallback)
		writer.U32(port.readParameter)
		writer.U32(port.writeCallback)
		writer.U32(port.writeParameter)
	}
	writer.U32(uint32(runtime.nextSerial))
	if runtime.networkConnected {
		writer.U8(1)
	} else {
		writer.U8(0)
	}
	writer.Write([]byte{0, 0, 0})
	writer.U32(runtime.networkCallback)
	writer.U32(runtime.networkParameter)
	socketDescriptors := make([]int, 0, len(runtime.sockets))
	for descriptor := range runtime.sockets {
		socketDescriptors = append(socketDescriptors, int(descriptor))
	}
	sort.Ints(socketDescriptors)
	writer.U32(uint32(len(socketDescriptors)))
	for _, rawDescriptor := range socketDescriptors {
		descriptor := int32(rawDescriptor)
		socket := runtime.sockets[descriptor]
		writer.U32(uint32(socket.descriptor))
		writer.U32(uint32(socket.domain))
		writer.U32(uint32(socket.socketType))
		writer.U32(socket.address)
		writer.U32(uint32(socket.port))
		if socket.connected {
			writer.U8(1)
		} else {
			writer.U8(0)
		}
		writer.Write([]byte{0, 0, 0})
		writeWIPIBytes(writer, socket.readData)
		writeWIPIBytes(writer, socket.writeData)
		writer.U32(socket.readCallback)
		writer.U32(socket.readParameter)
		writer.U32(socket.writeCallback)
		writer.U32(socket.writeParameter)
	}
	writer.U32(uint32(runtime.nextSocket))
	httpDescriptors := make([]int, 0, len(runtime.http))
	for descriptor := range runtime.http {
		httpDescriptors = append(httpDescriptors, int(descriptor))
	}
	sort.Ints(httpDescriptors)
	writer.U32(uint32(len(httpDescriptors)))
	for _, rawDescriptor := range httpDescriptors {
		descriptor := int32(rawDescriptor)
		request := runtime.http[descriptor]
		writer.U32(uint32(request.descriptor))
		writeWIPIBytes(writer, request.url)
		writeWIPIBytes(writer, request.method)
		writeWIPIBytes(writer, request.request)
		writeWIPIByteMap(writer, request.properties)
		writer.U32(request.proxyHost)
		writer.U32(uint32(request.proxyPort))
		if request.connected {
			writer.U8(1)
		} else {
			writer.U8(0)
		}
		writer.Write([]byte{0, 0, 0})
		writeWIPIBytes(writer, request.response)
		writer.U32(uint32(request.code))
	}
	writer.U32(uint32(runtime.nextHTTP))
	writer.U32(uint32(len(runtime.PendingCallbacks)))
	for _, callback := range runtime.PendingCallbacks {
		writer.U32(callback.Procedure)
		for _, argument := range callback.Args {
			writer.U32(argument)
		}
	}
	writeWIPIStringUint64Map(writer, runtime.Observed)
	writeWIPIStringUint64Map(writer, runtime.Unimplemented)
	writer.U32(uint32(len(runtime.Logs)))
	for _, log := range runtime.Logs {
		writeWIPIBytes(writer, []byte(log))
	}

	if err := guest.WriteMemoryState(writer, backend, wipicatalog.SystemBase, wipicatalog.SystemSize); err != nil {
		return err
	}
	if err := guest.WriteMemoryState(writer, backend, guest.HeapBase, guest.HeapSize); err != nil {
		return err
	}
	writer.U32(uint32(runtime.ServiceOwner))
	writeWIPIBytes(writer, []byte(runtime.serviceName))
	writer.U64(uint64(len(serviceState)))
	writer.Write(serviceState)
	writeWIPIUint32ServiceMap(writer, runtime.surfaceServices)
	writeWIPIUint32ServiceMap(writer, runtime.assetServices)
	writeWIPIUint32ServiceMap(writer, runtime.TimerServices)
	writeWIPIInt32ServiceMap(writer, runtime.fileServices)
	writeWIPIStringServiceMap(writer, runtime.DatabaseServices)
	writeWIPIUint32ServiceMap(writer, runtime.MediaServices)
	writeWIPIInt32ServiceMap(writer, runtime.serialServices)
	writeWIPIInt32ServiceMap(writer, runtime.socketServices)
	writeWIPIInt32ServiceMap(writer, runtime.httpServices)
	return nil
}

func (r *Runtime) RestoreState(saved *SavedState) error {
	if saved == nil {
		return fmt.Errorf("restore public WIPI runtime: state is missing")
	}
	restoredServices := saved.validatedServices
	if restoredServices == nil {
		var err error
		restoredServices, err = shared.NewServices(r.serviceConfig)
		if err != nil {
			return fmt.Errorf("restore public WIPI shared services: %w", err)
		}
		if err := restoredServices.UnmarshalBinary(saved.Services); err != nil {
			return fmt.Errorf("restore public WIPI shared services: %w", err)
		}
	}
	if err := r.CPU.WriteMemory(wipicatalog.SystemBase, saved.systemMemory); err != nil {
		return fmt.Errorf("restore public WIPI system memory: %w", err)
	}
	if err := r.CPU.WriteMemory(wipicatalog.TrampolineBase, r.Layout.Trampolines); err != nil {
		return fmt.Errorf("restore public WIPI trampolines: %w", err)
	}
	if err := r.CPU.WriteMemory(guest.HeapBase, saved.heapMemory); err != nil {
		return fmt.Errorf("restore public WIPI heap memory: %w", err)
	}
	if err := guest.RestoreHeapMetadata(
		&r.Heap,
		guest.HeapBase,
		guest.HeapSize,
		saved.heapAllocations,
	); err != nil {
		return err
	}
	r.Framebuffers = make(map[uint32]Framebuffer, len(saved.Framebuffers))
	for _, framebuffer := range saved.Framebuffers {
		r.Framebuffers[framebuffer.Handle] = framebuffer
	}
	r.ScreenHandle = saved.ScreenHandle
	r.screenPixels = saved.screenPixels
	r.properties = guest.CloneSliceMap(saved.properties)
	r.shared = guest.CloneMap(saved.shared)
	r.sharedSizes = guest.CloneMap(saved.sharedSizes)
	r.Timers = guest.CloneMap(saved.Timers)
	r.Resources = cloneResources(saved.Resources)
	r.ResourceIDs = make(map[int32]string, len(saved.Resources))
	for name, resource := range r.Resources {
		r.ResourceIDs[resource.Id] = name
	}
	r.nextResource = saved.nextResource
	r.Programs = ClonePrograms(saved.Programs)
	r.nextProgram = saved.nextProgram
	r.CurrentProgram = saved.CurrentProgram
	r.appManager = saved.appManager
	r.LastExecuteName = saved.LastExecuteName
	r.LastExecuteArgs = append([]string(nil), saved.LastExecuteArgs...)
	r.LastExecuted = saved.LastExecuted
	r.GraphicsEvents = append([]GraphicsEvent(nil), saved.GraphicsEvents...)
	r.Files = guest.CloneSliceMap(saved.Files)
	r.Directories = guest.CloneMap(saved.Directories)
	r.FileTimes = guest.CloneMap(saved.FileTimes)
	r.fileHandles = guest.CloneMap(saved.fileHandles)
	r.nextFile = saved.nextFile
	r.Databases = cloneDatabases(saved.Databases)
	r.DatabaseHandles = guest.CloneMap(saved.DatabaseHandles)
	r.NextDatabase = saved.NextDatabase
	r.UicContexts = guest.CloneMap(saved.UicContexts)
	r.UicClasses = guest.CloneMap(saved.UicClasses)
	r.UicClassNames = make(map[uint32]string, len(saved.UicClasses))
	for name, handle := range saved.UicClasses {
		r.UicClassNames[handle] = name
	}
	r.UicComponents = cloneComponents(saved.UicComponents)
	r.UicRepaints = append([]UICRepaint(nil), saved.UicRepaints...)
	r.MediaClips = cloneMediaClips(saved.MediaClips)
	r.mediaVolume = saved.mediaVolume
	r.mediaMute = guest.CloneMap(saved.mediaMute)
	r.vibratorLevel = saved.vibratorLevel
	r.vibratorTimeout = saved.vibratorTimeout
	r.backlight = saved.backlight
	r.ledState = saved.ledState
	r.phoneRequests = guest.CloneByteSlices(saved.phoneRequests)
	r.serialPorts = cloneSerialPorts(saved.serialPorts)
	r.nextSerial = saved.nextSerial
	r.networkConnected = saved.networkConnected
	r.networkCallback = saved.networkCallback
	r.networkParameter = saved.networkParameter
	r.sockets = cloneSockets(saved.sockets)
	r.nextSocket = saved.nextSocket
	r.http = cloneHTTP(saved.http)
	r.nextHTTP = saved.nextHTTP
	r.PendingCallbacks = append([]GuestCallback(nil), saved.PendingCallbacks...)
	r.Observed = guest.CloneMap(saved.Observed)
	r.Unimplemented = guest.CloneMap(saved.Unimplemented)
	r.Logs = append([]string(nil), saved.Logs...)
	r.strtokNext = saved.strtokNext
	r.TickMS = saved.TickMS
	r.ExitRequested = saved.ExitRequested
	r.exitCode = saved.exitCode
	r.Stats = saved.Stats
	r.Services = restoredServices
	r.ServiceOwner = saved.ServiceOwner
	r.serviceName = saved.serviceName
	r.surfaceServices = guest.CloneMap(saved.surfaceServices)
	r.assetServices = guest.CloneMap(saved.assetServices)
	r.TimerServices = guest.CloneMap(saved.TimerServices)
	r.fileServices = guest.CloneMap(saved.fileServices)
	r.DatabaseServices = guest.CloneMap(saved.DatabaseServices)
	r.MediaServices = guest.CloneMap(saved.MediaServices)
	r.serialServices = guest.CloneMap(saved.serialServices)
	r.socketServices = guest.CloneMap(saved.socketServices)
	r.httpServices = guest.CloneMap(saved.httpServices)
	return nil
}

func writeWIPIBytes(writer *guest.StateWriter, value []byte) {
	if writer.Err != nil {
		return
	}
	if uint64(len(value)) > uint64(maxWIPIString) {
		writer.Err = fmt.Errorf("public WIPI state byte field exceeds %d bytes", maxWIPIString)
		return
	}
	writer.U32(uint32(len(value)))
	writer.Write(value)
}

func readWIPIBytes(decoder *guest.StateDecoder) []byte {
	size := decoder.U32()
	if size > maxWIPIString {
		decoder.Err = decoder.Fail(fmt.Sprintf(
			"public WIPI byte field size %d exceeds limit",
			size,
		))
		return nil
	}
	return decoder.Bytes(int(size))
}

func writeWIPIByteMap(writer *guest.StateWriter, values map[string][]byte) {
	keys := guest.SortedStringKeys(values)
	writer.U32(uint32(len(keys)))
	for _, key := range keys {
		writeWIPIBytes(writer, []byte(key))
		writeWIPIBytes(writer, values[key])
	}
}

func readWIPIByteMap(decoder *guest.StateDecoder) map[string][]byte {
	count := decoder.U32()
	if count > MaxSavedEntries {
		decoder.Err = decoder.Fail(fmt.Sprintf("public WIPI byte-map count %d exceeds limit", count))
		return nil
	}
	result := make(map[string][]byte, count)
	for index := uint32(0); index < count; index++ {
		key := string(readWIPIBytes(decoder))
		value := append([]byte(nil), readWIPIBytes(decoder)...)
		if _, duplicate := result[key]; duplicate {
			decoder.Err = decoder.Fail("duplicate public WIPI byte-map key")
			return result
		}
		result[key] = value
	}
	return result
}

func writeWIPIFiles(writer *guest.StateWriter, values map[string][]byte) {
	keys := guest.SortedStringKeys(values)
	writer.U32(uint32(len(keys)))
	for _, key := range keys {
		writeWIPIBytes(writer, []byte(key))
		value := values[key]
		if len(value) > wipiFilesystemCapacity {
			writer.Err = fmt.Errorf("public WIPI file exceeds filesystem capacity")
			return
		}
		writer.U32(uint32(len(value)))
		writer.Write(value)
	}
}

func readWIPIFiles(decoder *guest.StateDecoder) map[string][]byte {
	count := decoder.U32()
	if count > MaxSavedEntries {
		decoder.Err = decoder.Fail(fmt.Sprintf("public WIPI file count %d exceeds limit", count))
		return nil
	}
	result := make(map[string][]byte, count)
	for index := uint32(0); index < count; index++ {
		name := string(readWIPIBytes(decoder))
		size := decoder.U32()
		if size > wipiFilesystemCapacity {
			decoder.Err = decoder.Fail(fmt.Sprintf("public WIPI file size %d exceeds limit", size))
			return result
		}
		if _, duplicate := result[name]; duplicate {
			decoder.Err = decoder.Fail("duplicate public WIPI file")
			return result
		}
		result[name] = append([]byte(nil), decoder.Bytes(int(size))...)
	}
	return result
}

func writeWIPIItems(writer *guest.StateWriter, items []wipiUIItem) {
	writer.U32(uint32(len(items)))
	for _, item := range items {
		writeWIPIBytes(writer, item.Label)
		writer.U32(item.image)
	}
}

func readWIPIItems(decoder *guest.StateDecoder) []wipiUIItem {
	count := decoder.U32()
	if count > MaxSavedEntries {
		decoder.Err = decoder.Fail(fmt.Sprintf("public WIPI UI item count %d exceeds limit", count))
		return nil
	}
	result := make([]wipiUIItem, 0, count)
	for index := uint32(0); index < count; index++ {
		result = append(result, wipiUIItem{
			Label: append([]byte(nil), readWIPIBytes(decoder)...),
			image: decoder.U32(),
		})
	}
	return result
}

func writeWIPIStringUint32Map(writer *guest.StateWriter, values map[string]uint32) {
	keys := guest.SortedStringKeys(values)
	writer.U32(uint32(len(keys)))
	for _, key := range keys {
		writeWIPIBytes(writer, []byte(key))
		writer.U32(values[key])
	}
}

func readWIPIStringUint32Map(decoder *guest.StateDecoder) map[string]uint32 {
	count := decoder.U32()
	if count > MaxSavedEntries {
		decoder.Err = decoder.Fail(fmt.Sprintf("public WIPI string map count %d exceeds limit", count))
		return nil
	}
	result := make(map[string]uint32, count)
	for index := uint32(0); index < count; index++ {
		key := string(readWIPIBytes(decoder))
		value := decoder.U32()
		if _, duplicate := result[key]; duplicate {
			decoder.Err = decoder.Fail("duplicate public WIPI string-map key")
			return result
		}
		result[key] = value
	}
	return result
}

func writeWIPIUint32Map(writer *guest.StateWriter, values map[uint32]uint32) {
	keys := guest.SortedUint32Keys(values)
	writer.U32(uint32(len(keys)))
	for _, key := range keys {
		writer.U32(key)
		writer.U32(values[key])
	}
}

func readWIPIUint32Map(decoder *guest.StateDecoder) map[uint32]uint32 {
	count := decoder.U32()
	if count > MaxSavedEntries {
		decoder.Err = decoder.Fail(fmt.Sprintf("public WIPI integer-map count %d exceeds limit", count))
		return nil
	}
	result := make(map[uint32]uint32, count)
	for index := uint32(0); index < count; index++ {
		key := decoder.U32()
		value := decoder.U32()
		if _, duplicate := result[key]; duplicate {
			decoder.Err = decoder.Fail("duplicate public WIPI integer-map key")
			return result
		}
		result[key] = value
	}
	return result
}

func writeWIPIUint32ServiceMap(
	writer *guest.StateWriter,
	values map[uint32]shared.ServiceID,
) {
	keys := guest.SortedUint32Keys(values)
	writer.U32(uint32(len(keys)))
	for _, key := range keys {
		writer.U32(key)
		writer.U64(uint64(values[key]))
	}
}

func readWIPIUint32ServiceMap(
	decoder *guest.StateDecoder,
) map[uint32]shared.ServiceID {
	count := decoder.U32()
	if count > MaxSavedEntries {
		decoder.Err = decoder.Fail("public WIPI service-map count exceeds limit")
		return nil
	}
	result := make(map[uint32]shared.ServiceID, count)
	for index := uint32(0); index < count; index++ {
		key := decoder.U32()
		value := shared.ServiceID(decoder.U64())
		if !value.Valid() {
			decoder.Err = decoder.Fail("invalid public WIPI service identifier")
			return result
		}
		if _, duplicate := result[key]; duplicate {
			decoder.Err = decoder.Fail("duplicate public WIPI service-map key")
			return result
		}
		result[key] = value
	}
	return result
}

func writeWIPIInt32ServiceMap(
	writer *guest.StateWriter,
	values map[int32]shared.ServiceID,
) {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, int(key))
	}
	sort.Ints(keys)
	writer.U32(uint32(len(keys)))
	for _, key := range keys {
		writer.U32(uint32(int32(key)))
		writer.U64(uint64(values[int32(key)]))
	}
}

func readWIPIInt32ServiceMap(
	decoder *guest.StateDecoder,
) map[int32]shared.ServiceID {
	count := decoder.U32()
	if count > MaxSavedEntries {
		decoder.Err = decoder.Fail("public WIPI signed service-map count exceeds limit")
		return nil
	}
	result := make(map[int32]shared.ServiceID, count)
	for index := uint32(0); index < count; index++ {
		key := int32(decoder.U32())
		value := shared.ServiceID(decoder.U64())
		if !value.Valid() {
			decoder.Err = decoder.Fail("invalid public WIPI service identifier")
			return result
		}
		if _, duplicate := result[key]; duplicate {
			decoder.Err = decoder.Fail("duplicate public WIPI signed service-map key")
			return result
		}
		result[key] = value
	}
	return result
}

func writeWIPIStringServiceMap(
	writer *guest.StateWriter,
	values map[string]shared.ServiceID,
) {
	keys := guest.SortedStringKeys(values)
	writer.U32(uint32(len(keys)))
	for _, key := range keys {
		writeWIPIBytes(writer, []byte(key))
		writer.U64(uint64(values[key]))
	}
}

func readWIPIStringServiceMap(
	decoder *guest.StateDecoder,
) map[string]shared.ServiceID {
	count := decoder.U32()
	if count > MaxSavedEntries {
		decoder.Err = decoder.Fail("public WIPI named service-map count exceeds limit")
		return nil
	}
	result := make(map[string]shared.ServiceID, count)
	for index := uint32(0); index < count; index++ {
		key := string(readWIPIBytes(decoder))
		value := shared.ServiceID(decoder.U64())
		if key == "" || !value.Valid() {
			decoder.Err = decoder.Fail("invalid public WIPI named service mapping")
			return result
		}
		if _, duplicate := result[key]; duplicate {
			decoder.Err = decoder.Fail("duplicate public WIPI named service-map key")
			return result
		}
		result[key] = value
	}
	return result
}

func writeWIPIStringUint64Map(writer *guest.StateWriter, values map[string]uint64) {
	keys := guest.SortedStringKeys(values)
	writer.U32(uint32(len(keys)))
	for _, key := range keys {
		writeWIPIBytes(writer, []byte(key))
		writer.U64(values[key])
	}
}

func readWIPIStringUint64Map(decoder *guest.StateDecoder) map[string]uint64 {
	count := decoder.U32()
	if count > MaxSavedEntries {
		decoder.Err = decoder.Fail(fmt.Sprintf("public WIPI counter-map count %d exceeds limit", count))
		return nil
	}
	result := make(map[string]uint64, count)
	for index := uint32(0); index < count; index++ {
		key := string(readWIPIBytes(decoder))
		value := decoder.U64()
		if _, duplicate := result[key]; duplicate {
			decoder.Err = decoder.Fail("duplicate public WIPI counter-map key")
			return result
		}
		result[key] = value
	}
	return result
}
