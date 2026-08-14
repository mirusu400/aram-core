package wipi

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"sort"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
	wipicatalog "github.com/mirusu400/aram-core/wipi"
)

const CallbackInstructionLimit = uint64(2_000_000)

type GuestCallback struct {
	Procedure uint32
	Args      [4]uint32
}

const (
	maxWIPIString = uint32(1 << 20)
	maxWIPICopy   = uint32(64 << 20)
	EpochUnix     = int64(946684800)

	wipiMaxPrograms         = 64
	wipiMaxProgramName      = 64
	wipiMaxExecuteArguments = 16
	wipiMaxGraphicsEvents   = 32
	wipiMaxUICRepaints      = 64

	ProgramTypeCApp           = int32(2)
	wipiProgramTypeCDLL       = int32(3)
	wipiProgramTypeJavaDLL    = int32(4)
	wipiProgramTypeJavaSysDLL = int32(5)
	DefaultAccessLevel        = int32(0xff)
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

type Framebuffer struct {
	Handle       uint32
	Pixels       uint32
	Width        int
	Height       int
	BitsPerPixel int
	owns         bool
}

type wipiTimer struct {
	Callback  uint32
	Parameter uint32
	Deadline  uint64
}

type Resource struct {
	Id   int32
	name string
	Data []byte
}

type Program struct {
	Id          int32
	ParentID    int32
	programType int32
	accessLevel int32
	Running     bool
	ExecName    string
	programName string
	version     string
	vendor      string
}

type GraphicsEvent struct {
	ID     int32
	Kind   int32
	Param1 int32
	Param2 int32
}

type UICRepaint struct {
	Component uint32
	X         int32
	Y         int32
	Width     int32
	Height    int32
}

type wipiFileHandle struct {
	path     string
	offset   int
	readable bool
	writable bool
}

type Database struct {
	Name       string
	RecordSize uint32
	Mode       int32
	NextRecord int32
	Records    map[int32][]byte
}

type UICallback struct {
	procedure uint32
	client    uint32
}

type wipiUIItem struct {
	Label []byte
	image uint32
}

type Component struct {
	Handle       uint32
	ClassName    string
	x            int32
	y            int32
	Width        int32
	Height       int32
	Enabled      bool
	Callbacks    map[int32]UICallback
	eventHandler uint32
	font         int32
	foreground   uint32
	background   uint32
	Label        []byte
	alignment    int32
	timeMask     int32
	timeData     [36]byte
	menuItems    []wipiUIItem
	listItems    []wipiUIItem
	ActiveMenu   int32
	ActiveList   int32
	text         []byte
	MaxText      int32
}

type wipiMediaClip struct {
	Handle    uint32
	mediaType []byte
	capacity  int32
	Callback  uint32
	Data      []byte
	position  int32
	volume    int32
	State     uint8
	Repeat    bool
}

type wipiSerialPort struct {
	descriptor     int32
	port           int32
	Data           []byte
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

type Runtime struct {
	CPU    cpu.Backend
	Frame  *image.RGBA
	Layout wipicatalog.Layout
	Heap   guest.Heap

	Services         *shared.Services
	serviceConfig    shared.Config
	ServiceOwner     shared.OwnerID
	serviceName      string
	surfaceServices  map[uint32]shared.ServiceID
	assetServices    map[uint32]shared.ServiceID
	TimerServices    map[uint32]shared.ServiceID
	fileServices     map[int32]shared.ServiceID
	DatabaseServices map[string]shared.ServiceID
	MediaServices    map[uint32]shared.ServiceID
	serialServices   map[int32]shared.ServiceID
	socketServices   map[int32]shared.ServiceID
	httpServices     map[int32]shared.ServiceID

	Framebuffers     map[uint32]Framebuffer
	framebufferBits  int
	ScreenHandle     uint32
	screenPixels     uint32
	properties       map[string][]byte
	shared           map[string]uint32
	sharedSizes      map[uint32]uint32
	Timers           map[uint32]wipiTimer
	Resources        map[string]*Resource
	ResourceIDs      map[int32]string
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
	UicClassNames    map[uint32]string
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
	InvokeSync       func(context.Context, GuestCallback) (uint32, error)
	activeContext    context.Context
	Observed         map[string]uint64
	Unimplemented    map[string]uint64
	Logs             []string
	strtokNext       uint32
	TickMS           uint64
	ExitRequested    bool
	exitCode         int32
	Stats            WIPIFrameStats
}

func MapRuntimeMemory(backend cpu.Backend) error {
	for _, mapping := range []struct {
		address     uint32
		size        uint32
		permissions cpu.Permissions
		label       string
	}{
		{wipicatalog.SystemBase, wipicatalog.SystemSize, cpu.PermissionRead | cpu.PermissionWrite, "system"},
		{wipicatalog.TrampolineBase, wipicatalog.TrampolineSize, cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute, "trampolines"},
		{guest.HeapBase, guest.HeapSize, cpu.PermissionRead | cpu.PermissionWrite, "heap"},
	} {
		if err := backend.Map(mapping.address, mapping.size, mapping.permissions); err != nil {
			return fmt.Errorf("map WIPI %s: %w", mapping.label, err)
		}
	}
	return nil
}

func NewRuntime(backend cpu.Backend, frame *image.RGBA) (*Runtime, error) {
	return NewRuntimeForProfile(
		backend,
		frame,
		guest.DefaultProfileID,
		"unknown",
		32,
		"wipi-c",
	)
}

func NewRuntimeForProfile(
	backend cpu.Backend,
	frame *image.RGBA,
	profileID string,
	carrier string,
	framebufferBits int,
	serviceName string,
) (*Runtime, error) {
	layout, err := wipicatalog.NewLayout()
	if err != nil {
		return nil, fmt.Errorf("build WIPI import layout: %w", err)
	}
	serviceConfig := shared.DefaultConfig()
	serviceConfig.Device.ProfileID = profileID
	serviceConfig.Device.Carrier = carrier
	serviceConfig.Device.ScreenWidth = int32(frame.Bounds().Dx())
	serviceConfig.Device.ScreenHeight = int32(frame.Bounds().Dy())
	serviceConfig.Device.ScreenFormat = shared.PixelBGRX8888
	if framebufferBits == 16 {
		serviceConfig.Device.ScreenFormat = shared.PixelRGB565
	}
	serviceConfig.Device.Capabilities = []shared.DeviceCapability{
		{Name: "audio", Enabled: true},
		{Name: "backlight", Enabled: true},
		{Name: "graphics", Enabled: true},
		{Name: "http", Enabled: true},
		{Name: "images", Enabled: true},
		{Name: "led", Enabled: true},
		{Name: "network", Enabled: true},
		{Name: "phone", Enabled: true},
		{Name: "serial", Enabled: true},
		{Name: "text", Enabled: true},
		{Name: "vibration", Enabled: true},
	}
	services, err := shared.NewServices(serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("initialize public WIPI shared Services: %w", err)
	}
	owner, err := services.Coordinator.Register(
		serviceName,
		serviceConfig.Limits.Coordinator.MaxRunBudget,
	)
	if err != nil {
		return nil, fmt.Errorf("register public WIPI adapter: %w", err)
	}
	if err := services.Coordinator.Transition(
		owner,
		shared.LifecycleReady,
		services.Clock.Monotonic(),
		services.Events,
	); err != nil {
		return nil, fmt.Errorf("ready public WIPI adapter: %w", err)
	}
	runtime := &Runtime{
		CPU:              backend,
		Frame:            frame,
		Layout:           layout,
		Heap:             guest.NewHeap(backend, guest.HeapBase, guest.HeapSize),
		Services:         services,
		serviceConfig:    services.Config,
		ServiceOwner:     owner,
		serviceName:      serviceName,
		surfaceServices:  make(map[uint32]shared.ServiceID),
		assetServices:    make(map[uint32]shared.ServiceID),
		TimerServices:    make(map[uint32]shared.ServiceID),
		fileServices:     make(map[int32]shared.ServiceID),
		DatabaseServices: make(map[string]shared.ServiceID),
		MediaServices:    make(map[uint32]shared.ServiceID),
		serialServices:   make(map[int32]shared.ServiceID),
		socketServices:   make(map[int32]shared.ServiceID),
		httpServices:     make(map[int32]shared.ServiceID),
		Framebuffers:     make(map[uint32]Framebuffer),
		framebufferBits:  framebufferBits,
		properties:       make(map[string][]byte),
		shared:           make(map[string]uint32),
		sharedSizes:      make(map[uint32]uint32),
		Timers:           make(map[uint32]wipiTimer),
		Resources:        make(map[string]*Resource),
		ResourceIDs:      make(map[int32]string),
		nextResource:     1,
		Programs:         DefaultPrograms(),
		nextProgram:      2,
		CurrentProgram:   1,
		appManager:       1,
		Files:            make(map[string][]byte),
		Directories: map[string]bool{
			"/private": true,
			"/shared":  true,
			"/system":  true,
		},
		FileTimes:        make(map[string]uint32),
		fileHandles:      make(map[int32]wipiFileHandle),
		nextFile:         3,
		Databases:        make(map[string]*Database),
		DatabaseHandles:  make(map[int32]string),
		NextDatabase:     1,
		UicContexts:      make(map[uint32]bool),
		UicClasses:       make(map[string]uint32),
		UicClassNames:    make(map[uint32]string),
		UicComponents:    make(map[uint32]*Component),
		MediaClips:       make(map[uint32]*wipiMediaClip),
		mediaVolume:      100,
		mediaMute:        make(map[int32]bool),
		serialPorts:      make(map[int32]*wipiSerialPort),
		nextSerial:       1,
		sockets:          make(map[int32]*wipiSocket),
		nextSocket:       1,
		http:             make(map[int32]*wipiHTTP),
		nextHTTP:         1,
		PendingCallbacks: make([]GuestCallback, 0),
		Observed:         make(map[string]uint64),
		Unimplemented:    make(map[string]uint64),
	}
	if err := runtime.installLayout(); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *Runtime) installLayout() error {
	if err := r.CPU.WriteMemory(wipicatalog.SystemBase, r.Layout.System); err != nil {
		return fmt.Errorf("install WIPI system imports: %w", err)
	}
	if err := r.CPU.WriteMemory(wipicatalog.TrampolineBase, r.Layout.Trampolines); err != nil {
		return fmt.Errorf("install WIPI API trampolines: %w", err)
	}
	return nil
}

func (r *Runtime) Reset() error {
	for _, region := range []struct {
		address uint32
		size    uint32
		label   string
	}{
		{wipicatalog.SystemBase, wipicatalog.SystemSize, "system"},
		{wipicatalog.TrampolineBase, wipicatalog.TrampolineSize, "trampolines"},
		{guest.HeapBase, guest.HeapSize, "heap"},
	} {
		if err := guest.ZeroMemory(r.CPU, region.address, region.size); err != nil {
			return fmt.Errorf("reset WIPI %s: %w", region.label, err)
		}
	}
	r.Heap = guest.NewHeap(r.CPU, guest.HeapBase, guest.HeapSize)
	services, err := shared.NewServices(r.serviceConfig)
	if err != nil {
		return fmt.Errorf("reset public WIPI shared services: %w", err)
	}
	owner, err := services.Coordinator.Register(
		r.serviceName,
		r.serviceConfig.Limits.Coordinator.MaxRunBudget,
	)
	if err != nil {
		return fmt.Errorf("register reset public WIPI adapter: %w", err)
	}
	if err := services.Coordinator.Transition(
		owner,
		shared.LifecycleReady,
		services.Clock.Monotonic(),
		services.Events,
	); err != nil {
		return fmt.Errorf("ready reset public WIPI adapter: %w", err)
	}
	r.Services = services
	r.ServiceOwner = owner
	r.surfaceServices = make(map[uint32]shared.ServiceID)
	r.assetServices = make(map[uint32]shared.ServiceID)
	r.TimerServices = make(map[uint32]shared.ServiceID)
	r.fileServices = make(map[int32]shared.ServiceID)
	r.DatabaseServices = make(map[string]shared.ServiceID)
	r.MediaServices = make(map[uint32]shared.ServiceID)
	r.serialServices = make(map[int32]shared.ServiceID)
	r.socketServices = make(map[int32]shared.ServiceID)
	r.httpServices = make(map[int32]shared.ServiceID)
	r.Framebuffers = make(map[uint32]Framebuffer)
	r.ScreenHandle = 0
	r.screenPixels = 0
	r.properties = make(map[string][]byte)
	r.shared = make(map[string]uint32)
	r.sharedSizes = make(map[uint32]uint32)
	r.Timers = make(map[uint32]wipiTimer)
	r.Resources = make(map[string]*Resource)
	r.ResourceIDs = make(map[int32]string)
	r.nextResource = 1
	r.Programs = DefaultPrograms()
	r.nextProgram = 2
	r.CurrentProgram = 1
	r.appManager = 1
	r.LastExecuteName = ""
	r.LastExecuteArgs = nil
	r.LastExecuted = 0
	r.GraphicsEvents = nil
	r.Files = make(map[string][]byte)
	r.Directories = map[string]bool{
		"/private": true,
		"/shared":  true,
		"/system":  true,
	}
	r.FileTimes = make(map[string]uint32)
	r.fileHandles = make(map[int32]wipiFileHandle)
	r.nextFile = 3
	r.Databases = make(map[string]*Database)
	r.DatabaseHandles = make(map[int32]string)
	r.NextDatabase = 1
	r.UicContexts = make(map[uint32]bool)
	r.UicClasses = make(map[string]uint32)
	r.UicClassNames = make(map[uint32]string)
	r.UicComponents = make(map[uint32]*Component)
	r.UicRepaints = nil
	r.MediaClips = make(map[uint32]*wipiMediaClip)
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
	r.PendingCallbacks = nil
	r.activeContext = nil
	r.Observed = make(map[string]uint64)
	r.Unimplemented = make(map[string]uint64)
	r.Logs = nil
	r.strtokNext = 0
	r.TickMS = 0
	r.ExitRequested = false
	r.exitCode = 0
	r.Stats = WIPIFrameStats{}
	return r.installLayout()
}

func (r *Runtime) EnqueueCallback(procedure uint32, args ...uint32) {
	if procedure == 0 || len(r.PendingCallbacks) >= MaxSavedEntries {
		return
	}
	callback := GuestCallback{Procedure: procedure}
	copy(callback.Args[:], args)
	r.PendingCallbacks = append(r.PendingCallbacks, callback)
}

func (r *Runtime) CallGuestFunction(procedure uint32, args ...uint32) (uint32, error) {
	if procedure == 0 {
		return 0, nil
	}
	if r.InvokeSync == nil {
		return 0, fmt.Errorf("synchronous WIPI callback 0x%08x has no guest runner", procedure)
	}
	callback := GuestCallback{Procedure: procedure}
	copy(callback.Args[:], args)
	ctx := r.activeContext
	if ctx == nil {
		ctx = context.Background()
	}
	return r.InvokeSync(ctx, callback)
}

func (r *Runtime) RegisterResource(name string, data []byte) int32 {
	if name == "" || len(name) > int(maxWIPIString) || len(data) > int(maxWIPICopy) {
		return guest.WIPIInvalid
	}
	totalBytes := uint64(len(data))
	for currentName, resource := range r.Resources {
		if currentName != name && resource != nil {
			totalBytes += uint64(len(resource.Data))
		}
	}
	if totalBytes > uint64(maxWIPICopy) {
		return guest.WIPINoMemory
	}
	packageFiles := make(map[string][]byte, len(r.Resources)+1)
	for currentName, resource := range r.Resources {
		if resource != nil {
			packageFiles[currentName] = resource.Data
		}
	}
	packageFiles[name] = data
	if err := r.Services.Storage.ReplacePackage(packageFiles); err != nil {
		return guest.WIPINoMemory
	}
	if resource := r.Resources[name]; resource != nil {
		resource.Data = append(resource.Data[:0], data...)
		return resource.Id
	}
	if len(r.Resources) >= MaxSavedEntries || r.nextResource < 1 {
		return guest.WIPINoMemory
	}
	resource := &Resource{
		Id:   r.nextResource,
		name: name,
		Data: append([]byte(nil), data...),
	}
	r.nextResource++
	r.Resources[name] = resource
	r.ResourceIDs[resource.Id] = name
	return resource.Id
}

// DispatchTrap exposes trap dispatch to the internal minigame overlay.
func (r *Runtime) DispatchTrap(ctx context.Context, trap uint32) (bool, error) {
	return r.dispatchTrap(ctx, trap)
}

// SharedHeap exposes the shared guest heap to the internal minigame overlay.
func (r *Runtime) SharedHeap() *guest.Heap {
	return &r.Heap
}

func (r *Runtime) dispatchTrap(ctx context.Context, trap uint32) (bool, error) {
	api, ok := r.Layout.APIByStub[trap]
	if !ok {
		return false, nil
	}
	return true, r.DispatchAPI(ctx, api)
}

func (r *Runtime) DispatchAPI(ctx context.Context, api wipicatalog.API) error {
	if ctx == nil {
		ctx = context.Background()
	}
	previousContext := r.activeContext
	r.activeContext = ctx
	defer func() {
		r.activeContext = previousContext
	}()
	r.Stats.APICalls++
	r.Stats.LastAPI = api.Name
	r.Observed[api.Name]++

	result, handled, err := r.dispatch(api)
	if err != nil {
		return fmt.Errorf("%s.%s: %w", api.Family, api.Name, err)
	}
	if handled {
		r.Stats.ImplementedCalls++
	} else {
		r.Stats.UnimplementedCalls++
		r.Stats.LastUnimplemented = api.Name
		r.Unimplemented[api.Name]++
		result.Low = defaultWIPIReturn(api.Family)
	}
	if err := r.ReturnFromTrap(result); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) dispatch(api wipicatalog.API) (guest.WIPIReturn, bool, error) {
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
		return guest.WIPIReturn{}, false, nil
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

func (r *Runtime) ReturnFromTrap(result guest.WIPIReturn) error {
	if err := r.CPU.WriteRegister(cpu.RegisterR0, result.Low); err != nil {
		return err
	}
	if err := r.CPU.WriteRegister(cpu.RegisterR1, result.High); err != nil {
		return err
	}
	lr, err := r.CPU.ReadRegister(cpu.RegisterLR)
	if err != nil {
		return err
	}
	if err := r.CPU.WriteRegister(cpu.RegisterPC, lr&^1); err != nil {
		return err
	}
	cpsr, err := r.CPU.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		return err
	}
	if lr&1 != 0 {
		cpsr |= cpu.StatusThumb
	} else {
		cpsr &^= cpu.StatusThumb
	}
	return r.CPU.WriteRegister(cpu.RegisterCPSR, cpsr)
}

func (r *Runtime) arg(index int) (uint32, error) {
	if index < 0 {
		return 0, fmt.Errorf("negative argument index")
	}
	if index < 4 {
		return r.CPU.ReadRegister(uint32(index))
	}
	sp, err := r.CPU.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return 0, err
	}
	return r.ReadU32(sp + uint32(index-4)*4)
}

func (r *Runtime) args(count int) ([]uint32, error) {
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

func (r *Runtime) ReadU32(address uint32) (uint32, error) {
	return guest.ReadU32(r.CPU, address)
}

func (r *Runtime) WriteU32(address, value uint32) error {
	return guest.WriteU32(r.CPU, address, value)
}

func (r *Runtime) writeU64(address uint32, value uint64) error {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	return r.CPU.WriteMemory(address, encoded[:])
}

func (r *Runtime) ReadCString(address uint32) ([]byte, error) {
	if address == 0 {
		return nil, nil
	}
	result := make([]byte, 0, 64)
	var current [1]byte
	for uint32(len(result)) < maxWIPIString {
		if err := r.CPU.ReadMemory(address+uint32(len(result)), current[:]); err != nil {
			return nil, err
		}
		if current[0] == 0 {
			return result, nil
		}
		result = append(result, current[0])
	}
	return nil, fmt.Errorf("string at 0x%08x exceeds %d bytes", address, maxWIPIString)
}

func (r *Runtime) writeCString(address uint32, value []byte, limit int32) (uint32, error) {
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
	if err := r.CPU.WriteMemory(address, output); err != nil {
		return 0, err
	}
	return uint32(count), nil
}

func (r *Runtime) Coverage() WIPIAPICoverage {
	return WIPIAPICoverage{
		Cataloged:             len(wipicatalog.APIs()),
		DispatchWired:         len(r.Layout.APIByStub),
		SemanticallyModeled:   modeledWIPIAPICount(),
		Observed:              len(r.Observed),
		ObservedUnimplemented: len(r.Unimplemented),
	}
}

func (r *Runtime) UnimplementedNames() []string {
	result := make([]string, 0, len(r.Unimplemented))
	for name := range r.Unimplemented {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func signed32(value uint32) int32 {
	return int32(value)
}

func wipiU64(value uint64) guest.WIPIReturn {
	return guest.WIPIReturn{Low: uint32(value), High: uint32(value >> 32)}
}

func checkedWIPISize(value uint32) (int, error) {
	if value > maxWIPICopy {
		return 0, fmt.Errorf("guest transfer size %d exceeds %d", value, maxWIPICopy)
	}
	return int(value), nil
}
