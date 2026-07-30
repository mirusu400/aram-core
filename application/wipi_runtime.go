package application

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"sort"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/wipi"
)

const (
	maxWIPIString = uint32(1 << 20)
	maxWIPICopy   = uint32(64 << 20)
	wipiEpochUnix = int64(946684800)

	wipiMaxPrograms         = 64
	wipiMaxProgramName      = 64
	wipiMaxExecuteArguments = 16
	wipiMaxGraphicsEvents   = 32
	wipiMaxUICRepaints      = 64

	wipiProgramTypeCApp       = int32(2)
	wipiProgramTypeCDLL       = int32(3)
	wipiProgramTypeJavaDLL    = int32(4)
	wipiProgramTypeJavaSysDLL = int32(5)
	wipiDefaultAccessLevel    = int32(0xff)
)

// WIPIFrameStats reports standard public WIPI-C activity independently of any
// carrier, vendor, device, or title-specific service profile.
type WIPIFrameStats struct {
	PresentCount       uint32
	APICalls           uint64
	ImplementedCalls   uint64
	UnimplementedCalls uint64
	LastAPI            string
	LastUnimplemented  string
}

// WIPIAPICoverage is the static and observed public API coverage summary.
type WIPIAPICoverage struct {
	Cataloged             int
	DispatchWired         int
	SemanticallyModeled   int
	Observed              int
	ObservedUnimplemented int
}

type wipiFramebuffer struct {
	handle       uint32
	pixels       uint32
	width        int
	height       int
	bitsPerPixel int
	owns         bool
}

type wipiTimer struct {
	callback  uint32
	parameter uint32
	deadline  uint64
}

type wipiResource struct {
	id   int32
	name string
	data []byte
}

type wipiProgram struct {
	id          int32
	parentID    int32
	programType int32
	accessLevel int32
	running     bool
	execName    string
	programName string
	version     string
	vendor      string
}

type wipiGraphicsEvent struct {
	id     int32
	kind   int32
	param1 int32
	param2 int32
}

type wipiUICRepaint struct {
	component uint32
	x         int32
	y         int32
	width     int32
	height    int32
}

type wipiFileHandle struct {
	path     string
	offset   int
	readable bool
	writable bool
}

type wipiDatabase struct {
	name       string
	recordSize uint32
	mode       int32
	nextRecord int32
	records    map[int32][]byte
}

type wipiUICallback struct {
	procedure uint32
	client    uint32
}

type wipiUIItem struct {
	label []byte
	image uint32
}

type wipiComponent struct {
	handle       uint32
	className    string
	x            int32
	y            int32
	width        int32
	height       int32
	enabled      bool
	callbacks    map[int32]wipiUICallback
	eventHandler uint32
	font         int32
	foreground   uint32
	background   uint32
	label        []byte
	alignment    int32
	timeMask     int32
	timeData     [36]byte
	menuItems    []wipiUIItem
	listItems    []wipiUIItem
	activeMenu   int32
	activeList   int32
	text         []byte
	maxText      int32
}

type wipiMediaClip struct {
	handle    uint32
	mediaType []byte
	capacity  int32
	callback  uint32
	data      []byte
	position  int32
	volume    int32
	state     uint8
	repeat    bool
}

type wipiSerialPort struct {
	descriptor     int32
	port           int32
	data           []byte
	readCallback   uint32
	readParameter  uint32
	writeCallback  uint32
	writeParameter uint32
}

type wipiSocket struct {
	descriptor     int32
	domain         int32
	socketType     int32
	address        uint32
	port           uint16
	connected      bool
	readData       []byte
	writeData      []byte
	readCallback   uint32
	readParameter  uint32
	writeCallback  uint32
	writeParameter uint32
}

type wipiHTTP struct {
	descriptor int32
	url        []byte
	method     []byte
	request    []byte
	properties map[string][]byte
	proxyHost  uint32
	proxyPort  uint16
	connected  bool
	response   []byte
	code       int32
}

type wipiRuntime struct {
	cpu    cpu.Backend
	frame  *image.RGBA
	layout wipi.Layout
	heap   guestHeap

	framebuffers     map[uint32]wipiFramebuffer
	framebufferBits  int
	screenHandle     uint32
	screenPixels     uint32
	properties       map[string][]byte
	shared           map[string]uint32
	sharedSizes      map[uint32]uint32
	timers           map[uint32]wipiTimer
	resources        map[string]*wipiResource
	resourceIDs      map[int32]string
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
	uicClassNames    map[uint32]string
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
	invokeSync       func(context.Context, wipiGuestCallback) (uint32, error)
	activeContext    context.Context
	observed         map[string]uint64
	unimplemented    map[string]uint64
	logs             []string
	strtokNext       uint32
	tickMS           uint64
	exitRequested    bool
	exitCode         int32
	stats            WIPIFrameStats
}

type wipiReturn struct {
	low  uint32
	high uint32
}

func mapWIPIRuntimeMemory(backend cpu.Backend) error {
	for _, mapping := range []struct {
		address     uint32
		size        uint32
		permissions cpu.Permissions
		label       string
	}{
		{wipi.SystemBase, wipi.SystemSize, cpu.PermissionRead | cpu.PermissionWrite, "system"},
		{wipi.TrampolineBase, wipi.TrampolineSize, cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute, "trampolines"},
		{guestHeapBase, guestHeapSize, cpu.PermissionRead | cpu.PermissionWrite, "heap"},
	} {
		if err := backend.Map(mapping.address, mapping.size, mapping.permissions); err != nil {
			return fmt.Errorf("map WIPI %s: %w", mapping.label, err)
		}
	}
	return nil
}

func newWIPIRuntime(backend cpu.Backend, frame *image.RGBA) (*wipiRuntime, error) {
	layout, err := wipi.NewLayout()
	if err != nil {
		return nil, fmt.Errorf("build WIPI import layout: %w", err)
	}
	runtime := &wipiRuntime{
		cpu:             backend,
		frame:           frame,
		layout:          layout,
		heap:            newGuestHeap(backend, guestHeapBase, guestHeapSize),
		framebuffers:    make(map[uint32]wipiFramebuffer),
		framebufferBits: 32,
		properties:      make(map[string][]byte),
		shared:          make(map[string]uint32),
		sharedSizes:     make(map[uint32]uint32),
		timers:          make(map[uint32]wipiTimer),
		resources:       make(map[string]*wipiResource),
		resourceIDs:     make(map[int32]string),
		nextResource:    1,
		programs:        defaultWIPIPrograms(),
		nextProgram:     2,
		currentProgram:  1,
		appManager:      1,
		files:           make(map[string][]byte),
		directories: map[string]bool{
			"/private": true,
			"/shared":  true,
			"/system":  true,
		},
		fileTimes:        make(map[string]uint32),
		fileHandles:      make(map[int32]wipiFileHandle),
		nextFile:         3,
		databases:        make(map[string]*wipiDatabase),
		databaseHandles:  make(map[int32]string),
		nextDatabase:     1,
		uicContexts:      make(map[uint32]bool),
		uicClasses:       make(map[string]uint32),
		uicClassNames:    make(map[uint32]string),
		uicComponents:    make(map[uint32]*wipiComponent),
		mediaClips:       make(map[uint32]*wipiMediaClip),
		mediaVolume:      100,
		mediaMute:        make(map[int32]bool),
		serialPorts:      make(map[int32]*wipiSerialPort),
		nextSerial:       1,
		sockets:          make(map[int32]*wipiSocket),
		nextSocket:       1,
		http:             make(map[int32]*wipiHTTP),
		nextHTTP:         1,
		pendingCallbacks: make([]wipiGuestCallback, 0),
		observed:         make(map[string]uint64),
		unimplemented:    make(map[string]uint64),
	}
	if err := runtime.installLayout(); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *wipiRuntime) installLayout() error {
	if err := r.cpu.WriteMemory(wipi.SystemBase, r.layout.System); err != nil {
		return fmt.Errorf("install WIPI system imports: %w", err)
	}
	if err := r.cpu.WriteMemory(wipi.TrampolineBase, r.layout.Trampolines); err != nil {
		return fmt.Errorf("install WIPI API trampolines: %w", err)
	}
	return nil
}

func (r *wipiRuntime) reset() error {
	for _, region := range []struct {
		address uint32
		size    uint32
		label   string
	}{
		{wipi.SystemBase, wipi.SystemSize, "system"},
		{wipi.TrampolineBase, wipi.TrampolineSize, "trampolines"},
		{guestHeapBase, guestHeapSize, "heap"},
	} {
		if err := zeroGuestMemory(r.cpu, region.address, region.size); err != nil {
			return fmt.Errorf("reset WIPI %s: %w", region.label, err)
		}
	}
	r.heap = newGuestHeap(r.cpu, guestHeapBase, guestHeapSize)
	r.framebuffers = make(map[uint32]wipiFramebuffer)
	r.screenHandle = 0
	r.screenPixels = 0
	r.properties = make(map[string][]byte)
	r.shared = make(map[string]uint32)
	r.sharedSizes = make(map[uint32]uint32)
	r.timers = make(map[uint32]wipiTimer)
	r.resources = make(map[string]*wipiResource)
	r.resourceIDs = make(map[int32]string)
	r.nextResource = 1
	r.programs = defaultWIPIPrograms()
	r.nextProgram = 2
	r.currentProgram = 1
	r.appManager = 1
	r.lastExecuteName = ""
	r.lastExecuteArgs = nil
	r.lastExecuted = 0
	r.graphicsEvents = nil
	r.files = make(map[string][]byte)
	r.directories = map[string]bool{
		"/private": true,
		"/shared":  true,
		"/system":  true,
	}
	r.fileTimes = make(map[string]uint32)
	r.fileHandles = make(map[int32]wipiFileHandle)
	r.nextFile = 3
	r.databases = make(map[string]*wipiDatabase)
	r.databaseHandles = make(map[int32]string)
	r.nextDatabase = 1
	r.uicContexts = make(map[uint32]bool)
	r.uicClasses = make(map[string]uint32)
	r.uicClassNames = make(map[uint32]string)
	r.uicComponents = make(map[uint32]*wipiComponent)
	r.uicRepaints = nil
	r.mediaClips = make(map[uint32]*wipiMediaClip)
	r.mediaVolume = 100
	r.mediaMute = make(map[int32]bool)
	r.vibratorLevel = 0
	r.vibratorTimeout = 0
	r.backlight = [4]int32{}
	r.ledState = 0
	r.phoneRequests = nil
	r.serialPorts = make(map[int32]*wipiSerialPort)
	r.nextSerial = 1
	r.networkConnected = false
	r.networkCallback = 0
	r.networkParameter = 0
	r.sockets = make(map[int32]*wipiSocket)
	r.nextSocket = 1
	r.http = make(map[int32]*wipiHTTP)
	r.nextHTTP = 1
	r.pendingCallbacks = nil
	r.activeContext = nil
	r.observed = make(map[string]uint64)
	r.unimplemented = make(map[string]uint64)
	r.logs = nil
	r.strtokNext = 0
	r.tickMS = 0
	r.exitRequested = false
	r.exitCode = 0
	r.stats = WIPIFrameStats{}
	return r.installLayout()
}

func (r *wipiRuntime) enqueueCallback(procedure uint32, args ...uint32) {
	if procedure == 0 || len(r.pendingCallbacks) >= maxSavedWIPIEntries {
		return
	}
	callback := wipiGuestCallback{procedure: procedure}
	copy(callback.args[:], args)
	r.pendingCallbacks = append(r.pendingCallbacks, callback)
}

func (r *wipiRuntime) callGuestFunction(procedure uint32, args ...uint32) (uint32, error) {
	if procedure == 0 {
		return 0, nil
	}
	if r.invokeSync == nil {
		return 0, fmt.Errorf("synchronous WIPI callback 0x%08x has no guest runner", procedure)
	}
	callback := wipiGuestCallback{procedure: procedure}
	copy(callback.args[:], args)
	ctx := r.activeContext
	if ctx == nil {
		ctx = context.Background()
	}
	return r.invokeSync(ctx, callback)
}

func (r *wipiRuntime) registerResource(name string, data []byte) int32 {
	if name == "" || len(name) > int(maxWIPIString) || len(data) > int(maxWIPICopy) {
		return wipiInvalid
	}
	totalBytes := uint64(len(data))
	for currentName, resource := range r.resources {
		if currentName != name && resource != nil {
			totalBytes += uint64(len(resource.data))
		}
	}
	if totalBytes > uint64(maxWIPICopy) {
		return wipiNoMemory
	}
	if resource := r.resources[name]; resource != nil {
		resource.data = append(resource.data[:0], data...)
		return resource.id
	}
	if len(r.resources) >= maxSavedWIPIEntries || r.nextResource < 1 {
		return wipiNoMemory
	}
	resource := &wipiResource{
		id:   r.nextResource,
		name: name,
		data: append([]byte(nil), data...),
	}
	r.nextResource++
	r.resources[name] = resource
	r.resourceIDs[resource.id] = name
	return resource.id
}

func (r *wipiRuntime) dispatchTrap(ctx context.Context, trap uint32) (bool, error) {
	api, ok := r.layout.APIByStub[trap]
	if !ok {
		return false, nil
	}
	return true, r.dispatchAPI(ctx, api)
}

func (r *wipiRuntime) dispatchAPI(ctx context.Context, api wipi.API) error {
	if ctx == nil {
		ctx = context.Background()
	}
	previousContext := r.activeContext
	r.activeContext = ctx
	defer func() {
		r.activeContext = previousContext
	}()
	r.stats.APICalls++
	r.stats.LastAPI = api.Name
	r.observed[api.Name]++

	result, handled, err := r.dispatch(api)
	if err != nil {
		return fmt.Errorf("%s.%s: %w", api.Family, api.Name, err)
	}
	if handled {
		r.stats.ImplementedCalls++
	} else {
		r.stats.UnimplementedCalls++
		r.stats.LastUnimplemented = api.Name
		r.unimplemented[api.Name]++
		result.low = defaultWIPIReturn(api.Family)
	}
	if err := r.returnFromTrap(result); err != nil {
		return err
	}
	return nil
}

func (r *wipiRuntime) dispatch(api wipi.API) (wipiReturn, bool, error) {
	switch api.Family {
	case "CSTDLIB":
		return r.dispatchCStdlib(api.Name)
	case "MC_KNL":
		return r.dispatchKernel(api.Name)
	case "MC_GRP":
		return r.dispatchGraphics(api.Name)
	case "MC_FS":
		return r.dispatchFilesystem(api.Name)
	case "MC_DB":
		return r.dispatchDatabase(api.Name)
	case "MC_UIC":
		return r.dispatchUIC(api.Name)
	case "MC_MDA":
		return r.dispatchMedia(api.Name)
	case "MC_MISC":
		return r.dispatchMisc(api.Name)
	case "MC_PHN":
		return r.dispatchPhone(api.Name)
	case "MC_SRL":
		return r.dispatchSerial(api.Name)
	case "MC_NET":
		return r.dispatchNetwork(api.Name)
	case "MC_HTTP":
		return r.dispatchHTTP(api.Name)
	case "MC_UTIL":
		return r.dispatchUtility(api.Name)
	default:
		return wipiReturn{}, false, nil
	}
}

func defaultWIPIReturn(family string) uint32 {
	switch family {
	case "MC_KNL", "MC_FS", "MC_NET", "MC_HTTP", "MC_DB", "MC_SRL", "MC_PHN":
		return math.MaxUint32
	default:
		return 0
	}
}

func (r *wipiRuntime) returnFromTrap(result wipiReturn) error {
	if err := r.cpu.WriteRegister(cpu.RegisterR0, result.low); err != nil {
		return err
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR1, result.high); err != nil {
		return err
	}
	lr, err := r.cpu.ReadRegister(cpu.RegisterLR)
	if err != nil {
		return err
	}
	if err := r.cpu.WriteRegister(cpu.RegisterPC, lr&^1); err != nil {
		return err
	}
	cpsr, err := r.cpu.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		return err
	}
	if lr&1 != 0 {
		cpsr |= cpu.StatusThumb
	} else {
		cpsr &^= cpu.StatusThumb
	}
	return r.cpu.WriteRegister(cpu.RegisterCPSR, cpsr)
}

func (r *wipiRuntime) arg(index int) (uint32, error) {
	if index < 0 {
		return 0, fmt.Errorf("negative argument index")
	}
	if index < 4 {
		return r.cpu.ReadRegister(uint32(index))
	}
	sp, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return 0, err
	}
	return r.readU32(sp + uint32(index-4)*4)
}

func (r *wipiRuntime) args(count int) ([]uint32, error) {
	result := make([]uint32, count)
	for index := range result {
		value, err := r.arg(index)
		if err != nil {
			return nil, err
		}
		result[index] = value
	}
	return result, nil
}

func (r *wipiRuntime) readU32(address uint32) (uint32, error) {
	var encoded [4]byte
	if err := r.cpu.ReadMemory(address, encoded[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(encoded[:]), nil
}

func (r *wipiRuntime) writeU32(address, value uint32) error {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	return r.cpu.WriteMemory(address, encoded[:])
}

func (r *wipiRuntime) writeU64(address uint32, value uint64) error {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	return r.cpu.WriteMemory(address, encoded[:])
}

func (r *wipiRuntime) readCString(address uint32) ([]byte, error) {
	if address == 0 {
		return nil, nil
	}
	result := make([]byte, 0, 64)
	var current [1]byte
	for uint32(len(result)) < maxWIPIString {
		if err := r.cpu.ReadMemory(address+uint32(len(result)), current[:]); err != nil {
			return nil, err
		}
		if current[0] == 0 {
			return result, nil
		}
		result = append(result, current[0])
	}
	return nil, fmt.Errorf("string at 0x%08x exceeds %d bytes", address, maxWIPIString)
}

func (r *wipiRuntime) writeCString(address uint32, value []byte, limit int32) (uint32, error) {
	if address == 0 || limit == 0 {
		return 0, nil
	}
	count := len(value)
	if limit > 0 && count >= int(limit) {
		count = int(limit) - 1
	}
	if count < 0 {
		count = 0
	}
	output := make([]byte, count+1)
	copy(output, value[:count])
	if err := r.cpu.WriteMemory(address, output); err != nil {
		return 0, err
	}
	return uint32(count), nil
}

func (r *wipiRuntime) coverage() WIPIAPICoverage {
	return WIPIAPICoverage{
		Cataloged:             len(wipi.APIs()),
		DispatchWired:         len(r.layout.APIByStub),
		SemanticallyModeled:   modeledWIPIAPICount(),
		Observed:              len(r.observed),
		ObservedUnimplemented: len(r.unimplemented),
	}
}

func (r *wipiRuntime) unimplementedNames() []string {
	result := make([]string, 0, len(r.unimplemented))
	for name := range r.unimplemented {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func signed32(value uint32) int32 {
	return int32(value)
}

func wipiU64(value uint64) wipiReturn {
	return wipiReturn{low: uint32(value), high: uint32(value >> 32)}
}

func checkedWIPISize(value uint32) (int, error) {
	if value > maxWIPICopy {
		return 0, fmt.Errorf("guest transfer size %d exceeds %d", value, maxWIPICopy)
	}
	return int(value), nil
}
