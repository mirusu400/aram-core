package brewrt

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

const (
	moduleBase            = uint32(0x01000000)
	helperBase            = uint32(0x02000000)
	helperTableBase       = helperBase + 0x4000
	allocTrap             = helperBase + 0x800
	returnTrap            = helperBase + 0x802
	shellTrap             = helperBase + 0x804
	freeTrap              = helperBase + 0x806
	addRefTrap            = helperBase + 0x808
	releaseTrap           = helperBase + 0x80a
	displayTrapBase       = helperBase + 0x900
	shellMethodTrapBase   = helperBase + 0xa00
	heapTrapBase          = helperBase + 0xb00
	helperMethodTrapBase  = helperTableBase + 0x400
	displayMeasureAnchor  = helperBase + 0xd00
	deviceModelTrap       = helperBase + 0xd40
	soundTrapBase         = helperBase + 0xe00
	shellObject           = helperBase + 0x100
	shellVTable           = helperBase + 0x200
	displayObject         = helperBase + 0x300
	displayVTable         = helperBase + 0x400
	heapObject            = helperBase + 0x500
	heapVTable            = helperBase + 0x600
	deviceModelObject     = helperBase + 0x700
	deviceModelVTable     = helperBase + 0x720
	ktfServiceObject      = helperBase + 0x7a0
	ktfServiceVTable      = helperBase + 0x7b0
	ktfServiceTrapBase    = helperBase + 0xd60
	ktfServiceMethodCount = uint32(4)
	soundObject           = helperBase + 0x740
	soundVTable           = helperBase + 0x760
	displayMethodCount    = uint32(26)
	shellMethodCount      = uint32(52)
	heapMethodCount       = uint32(9)
	soundMethodCount      = uint32(15)
	// BREW 4.x exposes well over 64 AEEStdLib entries. Keep the complete table
	// in its own page so later entries cannot alias the shell object and other
	// runtime state that historically starts at helperBase+0x100.
	helperMethodCount       = uint32(160)
	helperMemmoveSlot       = uint32(0)
	helperMemsetSlot        = uint32(1)
	helperStrcpySlot        = uint32(2)
	helperStrcatSlot        = uint32(3)
	helperStrcmpSlot        = uint32(4)
	helperStrlenSlot        = uint32(5)
	helperStrchrSlot        = uint32(6)
	helperStrrchrSlot       = uint32(7)
	helperSprintfSlot       = uint32(8)
	helperWStrcpySlot       = uint32(9)
	helperWStrcmpSlot       = uint32(11)
	helperWStrlenSlot       = uint32(12)
	helperWSprintfSlot      = uint32(15)
	helperStrToWStrSlot     = uint32(16)
	helperWStrToStrSlot     = uint32(17)
	helperSetupImageSlot    = uint32(25)
	helperWStrSizeSlot      = uint32(31)
	helperWStrNCopyNSlot    = uint32(32)
	helperAtoiSlot          = uint32(36)
	helperGetAEEVersionSlot = uint32(35)
	helperDbgPrintfSlot     = uint32(39)
	helperWStrCompressSlot  = uint32(40)
	helperGetRandSlot       = uint32(42)
	helperGetTimeMSSlot     = uint32(43)
	helperGetUpTimeMSSlot   = uint32(44)
	helperGetSecondsSlot    = uint32(45)
	helperGetJulianDateSlot = uint32(46)
	helperSysFreeSlot       = uint32(47)
	helperCurrentAppletSlot = uint32(0x30)
	helperStrncpySlot       = uint32(50)
	helperStrncmpSlot       = uint32(51)
	helperStricmpSlot       = uint32(52)
	helperStrstrSlot        = uint32(54)
	helperMemcmpSlot        = uint32(55)
	helperStrExpandSlot     = uint32(57)
	helperStristrSlot       = uint32(58)
	// The exact title branches explicitly for BREW 1.0, 1.2 and 2.1. Its KTF
	// handset path is the BREW 2.1 branch.
	aeeVersion          = uint32(0x02010000)
	heapBase            = uint32(0x03000000)
	heapSize            = uint32(0x00800000)
	stackBase           = uint32(0x04000000)
	stackSize           = uint32(0x00010000)
	outputAddr          = stackBase + 0x100
	framebufferBase     = uint32(0x05000000)
	framebufferWidth    = uint32(120)
	framebufferHeight   = uint32(160)
	framebufferBytes    = framebufferWidth * framebufferHeight * 2
	framebufferMapSize  = uint32(0x0000a000)
	serviceBase         = helperBase + 0x1000
	fileMgrObject       = serviceBase + 0x100
	fileMgrVTable       = serviceBase + 0x200
	fileMgrTrapBase     = serviceBase + 0x400
	fileMgrMethodCount  = uint32(21)
	fileObject          = serviceBase + 0x500
	fileVTable          = serviceBase + 0x600
	fileTrapBase        = serviceBase + 0x700
	fileMethodCount     = uint32(12)
	bitmapVTable        = serviceBase + 0x800
	bitmapTrapBase      = serviceBase + 0x900
	bitmapMethodCount   = uint32(16)
	graphicsObject      = serviceBase + 0xa00
	graphicsVTable      = serviceBase + 0xa20
	graphicsTrapBase    = serviceBase + 0xb00
	graphicsMethodCount = uint32(44)
	tapiObject          = serviceBase + 0xc00
	tapiVTable          = serviceBase + 0xc20
	tapiTrapBase        = serviceBase + 0xd00
	tapiMethodCount     = uint32(12)
	soundPlayerObject   = serviceBase + 0xe00
	soundPlayerVTable   = serviceBase + 0xe20
	soundPlayerTrapBase = serviceBase + 0xf00
	soundPlayerMethods  = uint32(19)
	networkServiceBase  = serviceBase + 0x1000
	netObject           = networkServiceBase + 0x100
	netVTable           = networkServiceBase + 0x200
	netTrapBase         = networkServiceBase + 0x300
	netMethodCount      = uint32(12)
	imageVTable         = networkServiceBase + 0x400
	imageTrapBase       = networkServiceBase + 0x500
	imageMethodCount    = uint32(11)
	controlServiceBase  = networkServiceBase + 0x1000
	textCtlObject       = controlServiceBase + 0x100
	textCtlVTable       = controlServiceBase + 0x200
	textCtlTrapBase     = controlServiceBase + 0x300
	textCtlMethodCount  = uint32(28)
	deviceBitmapObject  = controlServiceBase + 0x500
	menuCtlObject       = controlServiceBase + 0x600
	menuCtlVTable       = controlServiceBase + 0x700
	menuCtlTrapBase     = controlServiceBase + 0x800
	menuCtlMethodCount  = uint32(38)

	guestInstructionBudget = uint64(16_000_000)
	hostCallBudget         = 131_072
)

// Runtime executes structurally validated module and applet ARM code with the
// portable interpreter. It exposes only implemented service methods; every
// unknown class or vtable slot remains a typed boundary.
type Runtime struct {
	cpu           cpu.Backend
	moduleObject  uint32
	appletObject  uint32
	activeApplet  uint32
	heapNext      uint32
	heapAllocated map[uint32]uint32
	heapFree      []brewHeapBlock
	updates       uint64
	guestFrame    bool
	presented     []byte
	displayColors [16]uint32
	eventCounts   map[uint32]uint64
	files         map[string][]byte
	currentFile   []byte
	currentPath   string
	fileOffset    uint32
	timers        []brewCallback
	boundary      *ExecutionBoundaryError
	classIDs      []uint32
	clock         time.Duration
	randomState   uint32
	graphics      graphicsState
	soundInfo     [5]byte
	soundVolume   uint16
	preferences   map[brewPreferenceKey][]byte
	textControl   brewTextControl
	menuControl   brewMenuControl
}

type brewPreferenceKey struct {
	classID uint32
	version uint16
}

type brewTextControl struct {
	active     bool
	rect       [8]byte
	properties uint32
	text       []byte
	textPtr    uint32
	maxSize    uint32
	cursor     uint32
	inputMode  uint32
}

type brewMenuItem struct {
	id   uint16
	data uint32
}

type brewMenuControl struct {
	active     bool
	rect       [8]byte
	properties uint32
	selection  uint16
	items      []brewMenuItem
	enumIndex  int
}

type brewCallback struct {
	function  uint32
	context   uint32
	remaining time.Duration
}

type brewHeapBlock struct {
	address uint32
	size    uint32
}

// ExecutionBoundaryError identifies the first unimplemented proprietary ABI
// request without pretending that a package splash is a guest-rendered frame.
type ExecutionBoundaryError struct {
	ClassID       uint32
	Interface     string
	MethodSlot    uint32
	GuestReturnPC uint32
	Arguments     [4]uint32
}

func (e *ExecutionBoundaryError) Error() string {
	if e.Interface != "" {
		return fmt.Sprintf(
			"BREW execution boundary: %s vtable slot %d is not implemented (guest return PC 0x%08x, args %08x/%08x/%08x/%08x)",
			e.Interface,
			e.MethodSlot,
			e.GuestReturnPC,
			e.Arguments[0], e.Arguments[1], e.Arguments[2], e.Arguments[3],
		)
	}
	return fmt.Sprintf(
		"BREW execution boundary: shell CreateInstance class 0x%08x is not implemented (guest return PC 0x%08x)",
		e.ClassID,
		e.GuestReturnPC,
	)
}

func New(pkg Package) (*Runtime, error) {
	if len(pkg.Module) == 0 {
		return nil, fmt.Errorf("BREW module is empty")
	}
	backend := interpreter.New()
	classIDs := append([]uint32(nil), pkg.ClassIDs...)
	if len(classIDs) == 0 {
		classIDs = []uint32{ClassID}
	}
	r := &Runtime{
		cpu: backend, heapNext: heapBase, files: pkg.Files,
		heapAllocated: make(map[uint32]uint32),
		eventCounts:   make(map[uint32]uint64), classIDs: classIDs,
		randomState: 1,
		preferences: make(map[brewPreferenceKey][]byte),
	}
	if err := r.mapImage(pkg.Module); err != nil {
		_ = backend.Close()
		return nil, err
	}
	return r, nil
}

func (r *Runtime) mapImage(module []byte) error {
	imageBase := moduleBase - 8
	imageData := make([]byte, len(module)+8)
	binary.LittleEndian.PutUint32(imageData[0:4], 0x00010000)
	binary.LittleEndian.PutUint32(imageData[4:8], helperTableBase)
	copy(imageData[8:], module)
	if err := r.cpu.Map(imageBase, uint32(len(imageData)), cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return fmt.Errorf("map BREW module: %w", err)
	}
	if err := r.cpu.WriteMemory(imageBase, imageData); err != nil {
		return fmt.Errorf("write BREW module: %w", err)
	}
	if err := r.cpu.Map(helperBase, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return fmt.Errorf("map BREW loader helper: %w", err)
	}
	if err := r.cpu.Map(helperTableBase, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return fmt.Errorf("map BREW stdlib helper table: %w", err)
	}
	var helper [0x1000]byte
	var helperTable [0x1000]byte
	// The second loader-prefix word points at this SDK-sized helper table. Unknown
	// helpers remain distinct typed traps rather than corrupting adjacent objects.
	for slot := uint32(0); slot < helperMethodCount; slot++ {
		trap := helperMethodTrapBase + slot*2
		binary.LittleEndian.PutUint16(helperTable[trap-helperTableBase:], 0xbe08)
		binary.LittleEndian.PutUint32(helperTable[slot*4:], trap|1)
	}
	// AEEStdLib slots 26 and 27 are malloc and free. They use the dedicated
	// allocator traps because allocation participates in applet construction.
	binary.LittleEndian.PutUint32(helperTable[0x68:], allocTrap|1)
	binary.LittleEndian.PutUint32(helperTable[0x6c:], freeTrap|1)
	binary.LittleEndian.PutUint16(helper[0x800:], 0xbe00)
	binary.LittleEndian.PutUint16(helper[0x802:], 0xbe01)
	binary.LittleEndian.PutUint16(helper[0x804:], 0xbe02)
	binary.LittleEndian.PutUint16(helper[0x806:], 0xbe03)
	binary.LittleEndian.PutUint16(helper[0x808:], 0xbe04)
	binary.LittleEndian.PutUint16(helper[0x80a:], 0xbe05)
	// Exact-title IShell. Unknown methods terminate at a typed slot boundary;
	// AddRef, Release and CreateInstance are the only implemented prefix.
	binary.LittleEndian.PutUint32(helper[0x100:], shellVTable)
	for slot := uint32(0); slot < shellMethodCount; slot++ {
		trap := shellMethodTrapBase + slot*2
		binary.LittleEndian.PutUint16(helper[trap-helperBase:], 0xbe07)
		binary.LittleEndian.PutUint32(helper[0x200+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(helper[0x200:], addRefTrap|1)
	binary.LittleEndian.PutUint32(helper[0x204:], releaseTrap|1)
	binary.LittleEndian.PutUint32(helper[0x208:], shellTrap|1)
	// The BREW SDK IDisplay layout has 26 slots. Unknown methods terminate at a
	// typed slot boundary instead of returning fabricated success.
	binary.LittleEndian.PutUint32(helper[0x300:], displayVTable)
	for slot := uint32(0); slot < displayMethodCount; slot++ {
		trap := displayTrapBase + slot*2
		binary.LittleEndian.PutUint16(helper[trap-helperBase:], 0xbe06)
		binary.LittleEndian.PutUint32(helper[0x400+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(helper[0x400:], addRefTrap|1)
	binary.LittleEndian.PutUint32(helper[0x404:], releaseTrap|1)
	// SDK-derived IHeap layout: AddRef, Release, Malloc, Realloc, Free,
	// StrDup, CheckAvail, GetMemStats and GetModuleMemStats.
	binary.LittleEndian.PutUint32(helper[0x500:], heapVTable)
	for slot := uint32(0); slot < heapMethodCount; slot++ {
		trap := heapTrapBase + slot*2
		binary.LittleEndian.PutUint16(helper[trap-helperBase:], 0xbe09)
		binary.LittleEndian.PutUint32(helper[0x600+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(helper[0x600:], addRefTrap|1)
	binary.LittleEndian.PutUint32(helper[0x604:], releaseTrap|1)
	// The exact module's authenticated 25-entry handset table contains one KTF
	// model name: KTF-X2000. Its slot-2 device service writes that ten-byte name.
	binary.LittleEndian.PutUint32(helper[0x700:], deviceModelVTable)
	binary.LittleEndian.PutUint32(helper[0x720:], addRefTrap|1)
	binary.LittleEndian.PutUint32(helper[0x724:], releaseTrap|1)
	binary.LittleEndian.PutUint32(helper[0x728:], deviceModelTrap|1)
	binary.LittleEndian.PutUint16(helper[deviceModelTrap-helperBase:], 0xbe0a)
	// KTF handsets expose an OEM service under class 0x018000fc. The observed
	// interface has the standard AddRef/Release prefix followed by two setup
	// operations. Both setup calls are synchronous and report AEE_SUCCESS.
	binary.LittleEndian.PutUint32(helper[ktfServiceObject-helperBase:], ktfServiceVTable)
	for slot := uint32(0); slot < ktfServiceMethodCount; slot++ {
		trap := ktfServiceTrapBase + slot*2
		binary.LittleEndian.PutUint16(helper[trap-helperBase:], 0xbe12)
		binary.LittleEndian.PutUint32(helper[ktfServiceVTable-helperBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(helper[ktfServiceVTable-helperBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(helper[ktfServiceVTable-helperBase+4:], releaseTrap|1)
	// SDK ISound has 15 slots. The exact title first registers its notification
	// callback through slot 2; every later method remains a typed boundary.
	binary.LittleEndian.PutUint32(helper[0x740:], soundVTable)
	for slot := uint32(0); slot < soundMethodCount; slot++ {
		trap := soundTrapBase + slot*2
		binary.LittleEndian.PutUint16(helper[trap-helperBase:], 0xbe0b)
		binary.LittleEndian.PutUint32(helper[0x760+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(helper[0x760:], addRefTrap|1)
	binary.LittleEndian.PutUint32(helper[0x764:], releaseTrap|1)
	// The KTF-X2000 metadata uses field 3 of the concrete display object as an
	// OEM anchor, then reads the native surface pointer sixteen bytes into it.
	// This is separate from IDisplay vtable slot 3, which stays a typed boundary.
	binary.LittleEndian.PutUint32(helper[0x300+3*4:], displayMeasureAnchor)
	binary.LittleEndian.PutUint32(helper[displayMeasureAnchor-helperBase+16:], framebufferBase)
	if err := r.cpu.WriteMemory(helperBase, helper[:]); err != nil {
		return fmt.Errorf("write BREW loader helper: %w", err)
	}
	if err := r.cpu.WriteMemory(helperTableBase, helperTable[:]); err != nil {
		return fmt.Errorf("write BREW stdlib helper table: %w", err)
	}
	if err := r.cpu.Map(framebufferBase, framebufferMapSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		return fmt.Errorf("map BREW guest framebuffer: %w", err)
	}
	if err := r.cpu.Map(serviceBase, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return fmt.Errorf("map BREW exact-title services: %w", err)
	}
	var services [0x1000]byte
	binary.LittleEndian.PutUint32(services[fileMgrObject-serviceBase:], fileMgrVTable)
	for slot := uint32(0); slot < fileMgrMethodCount; slot++ {
		trap := fileMgrTrapBase + slot*2
		binary.LittleEndian.PutUint16(services[trap-serviceBase:], 0xbe0c)
		binary.LittleEndian.PutUint32(services[fileMgrVTable-serviceBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(services[fileMgrVTable-serviceBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(services[fileMgrVTable-serviceBase+4:], releaseTrap|1)
	binary.LittleEndian.PutUint32(services[fileObject-serviceBase:], fileVTable)
	for slot := uint32(0); slot < fileMethodCount; slot++ {
		trap := fileTrapBase + slot*2
		binary.LittleEndian.PutUint16(services[trap-serviceBase:], 0xbe0d)
		binary.LittleEndian.PutUint32(services[fileVTable-serviceBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(services[fileVTable-serviceBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(services[fileVTable-serviceBase+4:], releaseTrap|1)
	for slot := uint32(0); slot < bitmapMethodCount; slot++ {
		trap := bitmapTrapBase + slot*2
		binary.LittleEndian.PutUint16(services[trap-serviceBase:], 0xbe0e)
		binary.LittleEndian.PutUint32(services[bitmapVTable-serviceBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(services[bitmapVTable-serviceBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(services[bitmapVTable-serviceBase+4:], releaseTrap|1)
	binary.LittleEndian.PutUint32(services[graphicsObject-serviceBase:], graphicsVTable)
	for slot := uint32(0); slot < graphicsMethodCount; slot++ {
		trap := graphicsTrapBase + slot*2
		binary.LittleEndian.PutUint16(services[trap-serviceBase:], 0xbe0f)
		binary.LittleEndian.PutUint32(services[graphicsVTable-serviceBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(services[graphicsVTable-serviceBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(services[graphicsVTable-serviceBase+4:], releaseTrap|1)
	binary.LittleEndian.PutUint32(services[tapiObject-serviceBase:], tapiVTable)
	for slot := uint32(0); slot < tapiMethodCount; slot++ {
		trap := tapiTrapBase + slot*2
		binary.LittleEndian.PutUint16(services[trap-serviceBase:], 0xbe10)
		binary.LittleEndian.PutUint32(services[tapiVTable-serviceBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(services[tapiVTable-serviceBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(services[tapiVTable-serviceBase+4:], releaseTrap|1)
	binary.LittleEndian.PutUint32(services[soundPlayerObject-serviceBase:], soundPlayerVTable)
	for slot := uint32(0); slot < soundPlayerMethods; slot++ {
		trap := soundPlayerTrapBase + slot*2
		binary.LittleEndian.PutUint16(services[trap-serviceBase:], 0xbe11)
		binary.LittleEndian.PutUint32(services[soundPlayerVTable-serviceBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(services[soundPlayerVTable-serviceBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(services[soundPlayerVTable-serviceBase+4:], releaseTrap|1)
	if err := r.cpu.WriteMemory(serviceBase, services[:]); err != nil {
		return fmt.Errorf("write BREW exact-title services: %w", err)
	}
	if err := r.cpu.Map(networkServiceBase, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return fmt.Errorf("map BREW network services: %w", err)
	}
	var network [0x1000]byte
	binary.LittleEndian.PutUint32(network[netObject-networkServiceBase:], netVTable)
	for slot := uint32(0); slot < netMethodCount; slot++ {
		trap := netTrapBase + slot*2
		binary.LittleEndian.PutUint16(network[trap-networkServiceBase:], 0xbe12)
		binary.LittleEndian.PutUint32(network[netVTable-networkServiceBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(network[netVTable-networkServiceBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(network[netVTable-networkServiceBase+4:], releaseTrap|1)
	for slot := uint32(0); slot < imageMethodCount; slot++ {
		trap := imageTrapBase + slot*2
		binary.LittleEndian.PutUint16(network[trap-networkServiceBase:], 0xbe13)
		binary.LittleEndian.PutUint32(network[imageVTable-networkServiceBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(network[imageVTable-networkServiceBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(network[imageVTable-networkServiceBase+4:], releaseTrap|1)
	if err := r.cpu.WriteMemory(networkServiceBase, network[:]); err != nil {
		return fmt.Errorf("write BREW network services: %w", err)
	}
	if err := r.cpu.Map(controlServiceBase, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return fmt.Errorf("map BREW control services: %w", err)
	}
	var controls [0x1000]byte
	binary.LittleEndian.PutUint32(controls[textCtlObject-controlServiceBase:], textCtlVTable)
	for slot := uint32(0); slot < textCtlMethodCount; slot++ {
		trap := textCtlTrapBase + slot*2
		binary.LittleEndian.PutUint16(controls[trap-controlServiceBase:], 0xbe14)
		binary.LittleEndian.PutUint32(controls[textCtlVTable-controlServiceBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(controls[textCtlVTable-controlServiceBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(controls[textCtlVTable-controlServiceBase+4:], releaseTrap|1)
	binary.LittleEndian.PutUint32(controls[menuCtlObject-controlServiceBase:], menuCtlVTable)
	for slot := uint32(0); slot < menuCtlMethodCount; slot++ {
		trap := menuCtlTrapBase + slot*2
		binary.LittleEndian.PutUint16(controls[trap-controlServiceBase:], 0xbe15)
		binary.LittleEndian.PutUint32(controls[menuCtlVTable-controlServiceBase+slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(controls[menuCtlVTable-controlServiceBase:], addRefTrap|1)
	binary.LittleEndian.PutUint32(controls[menuCtlVTable-controlServiceBase+4:], releaseTrap|1)
	deviceBitmap := make([]byte, 36)
	binary.LittleEndian.PutUint32(deviceBitmap[0:], bitmapVTable)
	binary.LittleEndian.PutUint32(deviceBitmap[8:], framebufferBase)
	binary.LittleEndian.PutUint16(deviceBitmap[20:], uint16(framebufferWidth))
	binary.LittleEndian.PutUint16(deviceBitmap[22:], uint16(framebufferHeight))
	binary.LittleEndian.PutUint16(deviceBitmap[24:], uint16(framebufferWidth*2))
	deviceBitmap[28] = 16
	deviceBitmap[29] = idibColorScheme565
	copy(controls[deviceBitmapObject-controlServiceBase:], deviceBitmap)
	if err := r.cpu.WriteMemory(controlServiceBase, controls[:]); err != nil {
		return fmt.Errorf("write BREW control services: %w", err)
	}
	// The exact title installs ARM pixel routines into its BREW heap and calls
	// them through the surface callback at +0x3c. The authenticated bytes begin
	// `ldr/ldr/ldr` at 0x0300e3c8, so this handset heap must be executable.
	if err := r.cpu.Map(heapBase, heapSize, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return fmt.Errorf("map BREW bootstrap heap: %w", err)
	}
	if err := r.cpu.Map(stackBase, stackSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		return fmt.Errorf("map BREW bootstrap stack: %w", err)
	}
	return nil
}

// ProbeAppletBoundary calls the authenticated module's real CreateInstance and
// completes the applet constructor through the researched service contracts.
func (r *Runtime) ProbeAppletBoundary(ctx context.Context) error {
	if r.boundary != nil {
		return r.boundary
	}
	if r.moduleObject == 0 {
		return fmt.Errorf("probe BREW applet before module bootstrap")
	}
	var encoded [4]byte
	if err := r.cpu.ReadMemory(r.moduleObject, encoded[:]); err != nil {
		return fmt.Errorf("read BREW module vtable: %w", err)
	}
	vtable := binary.LittleEndian.Uint32(encoded[:])
	if err := r.cpu.ReadMemory(vtable+8, encoded[:]); err != nil {
		return fmt.Errorf("read BREW module CreateInstance: %w", err)
	}
	createInstance := binary.LittleEndian.Uint32(encoded[:])
	if createInstance == 0 {
		return fmt.Errorf("BREW module CreateInstance is null")
	}
	for _, classID := range r.classIDs {
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0: r.moduleObject,
			cpu.RegisterR1: shellObject,
			cpu.RegisterR2: classID,
			cpu.RegisterR3: outputAddr + 4,
			cpu.RegisterSP: stackBase + stackSize - 16,
			cpu.RegisterLR: returnTrap | 1,
		} {
			if err := r.cpu.WriteRegister(register, value); err != nil {
				return fmt.Errorf("initialize BREW applet register r%d: %w", register, err)
			}
		}
		var zero [4]byte
		if err := r.cpu.WriteMemory(outputAddr+4, zero[:]); err != nil {
			return fmt.Errorf("clear BREW applet output: %w", err)
		}
		pc, mode := branchTarget(createInstance)
		result, err := r.runAppletCode(ctx, pc, mode, fmt.Sprintf("constructor class 0x%08x", classID))
		if err != nil {
			return err
		}
		if err := r.cpu.ReadMemory(outputAddr+4, encoded[:]); err != nil {
			return fmt.Errorf("read BREW applet object: %w", err)
		}
		candidate := binary.LittleEndian.Uint32(encoded[:])
		if candidate < heapBase || candidate >= heapBase+heapSize {
			candidate = result
		}
		if candidate >= heapBase && candidate < heapBase+heapSize {
			r.appletObject = candidate
			r.activeApplet = candidate
			return nil
		}
	}
	return fmt.Errorf("BREW module rejected %d candidate application ClassIDs", len(r.classIDs))
}

// DispatchEvent invokes the authenticated applet's real IApplet::HandleEvent.
// EVT_APP_START is 0; key press and release are 0x101 and 0x102 respectively.
func (r *Runtime) DispatchEvent(
	ctx context.Context,
	event uint32,
	wParam uint32,
	dwParam uint32,
) (bool, error) {
	if r.appletObject == 0 {
		return false, fmt.Errorf("dispatch BREW event before applet construction")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var encoded [4]byte
	if err := r.cpu.ReadMemory(r.appletObject, encoded[:]); err != nil {
		return false, fmt.Errorf("read BREW applet vtable: %w", err)
	}
	vtable := binary.LittleEndian.Uint32(encoded[:])
	if err := r.cpu.ReadMemory(vtable+8, encoded[:]); err != nil {
		return false, fmt.Errorf("read BREW HandleEvent slot: %w", err)
	}
	handleEvent := binary.LittleEndian.Uint32(encoded[:])
	if handleEvent == 0 {
		return false, fmt.Errorf("BREW HandleEvent is null")
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: r.appletObject,
		cpu.RegisterR1: event,
		cpu.RegisterR2: wParam,
		cpu.RegisterR3: dwParam,
		cpu.RegisterSP: stackBase + stackSize - 16,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := r.cpu.WriteRegister(register, value); err != nil {
			return false, fmt.Errorf("initialize BREW event register r%d: %w", register, err)
		}
	}
	pc, mode := branchTarget(handleEvent)
	result, err := r.runAppletCode(ctx, pc, mode, fmt.Sprintf("event 0x%03x", event))
	if err != nil {
		return false, err
	}
	r.eventCounts[event]++
	return result != 0, nil
}

func (r *Runtime) runAppletCode(
	ctx context.Context,
	pc uint32,
	mode cpu.Mode,
	operation string,
) (uint32, error) {
	lastHostCall := ""
	for traps := 0; traps < hostCallBudget; traps++ {
		result := r.cpu.Run(ctx, pc, mode, guestInstructionBudget)
		if result.Err != nil {
			if lastHostCall != "" {
				return 0, fmt.Errorf("execute BREW applet %s at PC 0x%08x after %s: %w", operation, result.PC, lastHostCall, result.Err)
			}
			return 0, fmt.Errorf("execute BREW applet %s at PC 0x%08x: %w", operation, result.PC, result.Err)
		}
		if result.Reason != cpu.StopBreakpoint {
			return 0, fmt.Errorf("execute BREW applet %s stopped at PC 0x%08x with reason %d", operation, result.PC, result.Reason)
		}
		lastHostCall = describeHostTrap(result.PC)
		var arguments [4]uint32
		for index := range arguments {
			arguments[index], _ = r.cpu.ReadRegister(cpu.RegisterR0 + uint32(index))
		}
		lastHostCall = fmt.Sprintf(
			"%s args %08x/%08x/%08x/%08x",
			lastHostCall,
			arguments[0], arguments[1], arguments[2], arguments[3],
		)
		switch result.PC {
		case allocTrap + 2:
			if err := r.returnAllocation(); err != nil {
				return 0, err
			}
			if r.activeApplet == 0 {
				candidate, err := r.cpu.ReadRegister(cpu.RegisterR0)
				if err != nil {
					return 0, fmt.Errorf("read BREW applet allocation: %w", err)
				}
				r.activeApplet = candidate
			}
		case freeTrap + 2:
			address, err := r.cpu.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return 0, fmt.Errorf("read BREW free address: %w", err)
			}
			r.releaseGuest(address)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return 0, fmt.Errorf("return BREW free status: %w", err)
			}
		case shellTrap + 2:
			if err := r.createShellInstance(); err != nil {
				return 0, err
			}
		case addRefTrap + 2:
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 1); err != nil {
				return 0, fmt.Errorf("return BREW interface reference count: %w", err)
			}
		case releaseTrap + 2:
			address, err := r.cpu.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return 0, fmt.Errorf("read BREW released interface: %w", err)
			}
			r.releaseInterfaceObject(address)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return 0, fmt.Errorf("return BREW released interface reference count: %w", err)
			}
		case returnTrap + 2:
			value, err := r.cpu.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return 0, fmt.Errorf("read BREW applet %s result: %w", operation, err)
			}
			return value, nil
		default:
			handled, nextPC, nextMode, err := r.handleAppletMethodTrap(result.PC)
			if err != nil {
				return 0, err
			}
			if !handled {
				return 0, fmt.Errorf("unexpected BREW applet breakpoint at PC 0x%08x", result.PC-2)
			}
			pc, mode = nextPC, nextMode
			continue
		}
		nextPC, nextMode, err := r.hostReturnTarget()
		if err != nil {
			return 0, err
		}
		pc, mode = nextPC, nextMode
	}
	return 0, fmt.Errorf("BREW applet %s exceeded host-call limit", operation)
}

func describeHostTrap(pc uint32) string {
	switch pc {
	case allocTrap + 2:
		return "AEEStdLib malloc"
	case freeTrap + 2:
		return "AEEStdLib free"
	case shellTrap + 2:
		return "IShell CreateInstance"
	case addRefTrap + 2:
		return "interface AddRef"
	case releaseTrap + 2:
		return "interface Release"
	case returnTrap + 2:
		return "guest return"
	}
	type trapRange struct {
		name  string
		base  uint32
		count uint32
	}
	for _, candidate := range []trapRange{
		{"AEEStdLib", helperMethodTrapBase, helperMethodCount},
		{"IShell", shellMethodTrapBase, shellMethodCount},
		{"IDisplay", displayTrapBase, displayMethodCount},
		{"IHeap", heapTrapBase, heapMethodCount},
		{"ISound", soundTrapBase, soundMethodCount},
		{"IFileMgr", fileMgrTrapBase, fileMgrMethodCount},
		{"IFile", fileTrapBase, fileMethodCount},
		{"IBitmap", bitmapTrapBase, bitmapMethodCount},
		{"IGraphics", graphicsTrapBase, graphicsMethodCount},
		{"ITAPI", tapiTrapBase, tapiMethodCount},
		{"ISoundPlayer", soundPlayerTrapBase, soundPlayerMethods},
		{"INetMgr", netTrapBase, netMethodCount},
		{"IImage", imageTrapBase, imageMethodCount},
		{"ITextCtl", textCtlTrapBase, textCtlMethodCount},
		{"IMenuCtl", menuCtlTrapBase, menuCtlMethodCount},
		{"IKTFService", ktfServiceTrapBase, ktfServiceMethodCount},
	} {
		if pc >= candidate.base+2 && pc < candidate.base+candidate.count*2+2 {
			return fmt.Sprintf("%s slot %d", candidate.name, (pc-2-candidate.base)/2)
		}
	}
	return fmt.Sprintf("host trap 0x%08x", pc-2)
}

func (r *Runtime) handleAppletMethodTrap(
	breakpoint uint32,
) (bool, uint32, cpu.Mode, error) {
	resume := func() (bool, uint32, cpu.Mode, error) {
		pc, mode, err := r.hostReturnTarget()
		return true, pc, mode, err
	}
	boundary := func(iface string, slot uint32) (bool, uint32, cpu.Mode, error) {
		returnAddress, err := r.cpu.ReadRegister(cpu.RegisterLR)
		if err != nil {
			return true, 0, cpu.ModeARM, fmt.Errorf("read BREW %s return address: %w", iface, err)
		}
		var arguments [4]uint32
		for index := range arguments {
			arguments[index], _ = r.cpu.ReadRegister(cpu.RegisterR0 + uint32(index))
		}
		r.boundary = &ExecutionBoundaryError{
			Interface:     iface,
			MethodSlot:    slot,
			GuestReturnPC: returnAddress &^ 1,
			Arguments:     arguments,
		}
		return true, 0, cpu.ModeARM, r.boundary
	}
	if breakpoint == deviceModelTrap+2 {
		if err := r.returnDeviceModel(); err != nil {
			return true, 0, cpu.ModeARM, err
		}
		return resume()
	}
	if breakpoint >= ktfServiceTrapBase+2 && breakpoint < ktfServiceTrapBase+ktfServiceMethodCount*2+2 {
		slot := (breakpoint - 2 - ktfServiceTrapBase) / 2
		switch slot {
		case 2:
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return KTF OEM service slot %d status: %w", slot, err)
			}
			return resume()
		case 3:
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return KTF OEM service slot %d status: %w", slot, err)
			}
			return resume()
		default:
			return boundary("IKTFService", slot)
		}
	}

	if breakpoint >= helperMethodTrapBase+2 && breakpoint < helperMethodTrapBase+helperMethodCount*2+2 {
		slot := (breakpoint - 2 - helperMethodTrapBase) / 2
		switch slot {
		case helperMemmoveSlot:
			if err := r.moveGuestMemory(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperMemsetSlot:
			if err := r.fillGuestMemory(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStrcpySlot:
			if err := r.copyGuestCString(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStrcatSlot:
			if err := r.appendGuestCString(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStrcmpSlot:
			if err := r.compareGuestCStrings(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStrlenSlot:
			if err := r.returnCStringLength(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStrchrSlot:
			if err := r.findGuestCStringByte(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStrrchrSlot:
			if err := r.findLastGuestCStringByte(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperSprintfSlot:
			if err := r.formatResourceName(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperWStrcpySlot:
			if err := r.copyGuestWideString(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperWStrcmpSlot:
			if err := r.compareGuestWideStrings(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperWStrlenSlot:
			if err := r.returnGuestWideStringLength(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperWSprintfSlot:
			if err := r.formatGuestWideString(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStrToWStrSlot:
			if err := r.convertGuestStringToWide(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperWStrToStrSlot:
			if err := r.convertGuestWideToString(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperSetupImageSlot:
			if err := r.setupNativeImage(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperWStrSizeSlot:
			if err := r.returnGuestWideStringSize(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperWStrNCopyNSlot:
			if err := r.copyGuestWideStringN(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperAtoiSlot:
			if err := r.parseGuestInteger(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperGetRandSlot:
			if err := r.fillGuestRandom(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperGetAEEVersionSlot:
			if err := r.cpu.WriteRegister(cpu.RegisterR0, aeeVersion); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW AEE version: %w", err)
			}
			return resume()
		case helperDbgPrintfSlot:
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW helper slot %d result: %w", slot, err)
			}
			return resume()
		case helperWStrCompressSlot:
			if err := r.compressGuestWideString(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperGetTimeMSSlot, helperGetUpTimeMSSlot:
			milliseconds := uint32(r.clock / time.Millisecond)
			// Guest code commonly polls uptime inside a callback. The cooperative
			// clock normally advances between callbacks, so leaving it frozen here
			// turns a valid delay loop into an infinite host-call loop. Charge one
			// deterministic millisecond per observation to model time spent executing
			// the guest while preserving reproducible runs.
			r.clock += time.Millisecond
			if err := r.cpu.WriteRegister(cpu.RegisterR0, milliseconds); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW uptime: %w", err)
			}
			return resume()
		case helperGetSecondsSlot:
			// BREW calendar seconds use the GPS epoch (1980-01-06). Keep a fixed,
			// deterministic base and advance it with the cooperative guest clock.
			const calendarBase = uint32(630_720_000) // 2000-01-01 in BREW seconds.
			if err := r.cpu.WriteRegister(cpu.RegisterR0, calendarBase+uint32(r.clock/time.Second)); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW calendar seconds: %w", err)
			}
			return resume()
		case helperGetJulianDateSlot:
			seconds, err := r.cpu.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW Julian seconds: %w", err)
			}
			destination, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW Julian destination: %w", err)
			}
			// BREW seconds are measured from the GPS epoch. JulianType is seven
			// consecutive uint16 fields: year, month, day, hour, minute, second,
			// and weekday.
			date := time.Date(1980, time.January, 6, 0, 0, 0, 0, time.UTC).Add(time.Duration(seconds) * time.Second)
			var encoded [14]byte
			values := [...]uint16{uint16(date.Year()), uint16(date.Month()), uint16(date.Day()), uint16(date.Hour()), uint16(date.Minute()), uint16(date.Second()), uint16(date.Weekday())}
			for index, value := range values {
				binary.LittleEndian.PutUint16(encoded[index*2:], value)
			}
			if err := r.cpu.WriteMemory(destination, encoded[:]); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("write BREW Julian date: %w", err)
			}
			return resume()
		case helperSysFreeSlot:
			address, err := r.cpu.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW sysfree address: %w", err)
			}
			r.releaseGuest(address)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW sysfree status: %w", err)
			}
			return resume()
		case helperCurrentAppletSlot:
			if err := r.cpu.WriteRegister(cpu.RegisterR0, r.activeApplet); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW current applet: %w", err)
			}
			return resume()
		case helperStrncpySlot:
			if err := r.copyGuestStringN(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStrncmpSlot:
			if err := r.compareGuestStringsN(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStricmpSlot:
			if err := r.compareGuestCStringsFoldASCII(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStrstrSlot:
			if err := r.findGuestCString(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperMemcmpSlot:
			if err := r.compareGuestMemory(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStrExpandSlot:
			if err := r.expandGuestOEMString(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperStristrSlot:
			if err := r.findGuestCStringFoldASCII(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		default:
			return boundary("loader helper", slot)
		}
	}
	if breakpoint >= displayTrapBase+2 && breakpoint < displayTrapBase+displayMethodCount*2+2 {
		slot := (breakpoint - 2 - displayTrapBase) / 2
		switch slot {
		case 2: // GetFontMetrics(IDisplay *, AEEFont, int *, int *)
			if err := r.returnFontMetrics(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 3: // MeasureTextEx(IDisplay *, AEEFont, AECHAR *, int, int, int *)
			if err := r.measureDisplayText(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 4: // DrawText(...)
			// The handset system font is not packaged with the title. BREW accepts
			// the draw request even when that platform font cannot be rasterized.
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW DrawText status: %w", err)
			}
			return resume()
		case 7: // Update(IDisplay *)
			if err := r.commitFramebufferUpdate(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW display-update status: %w", err)
			}
			return resume()
		case 10: // SetColor(IDisplay *, AEEClrItem, RGBVAL)
			item, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW color item: %w", err)
			}
			value, err := r.cpu.ReadRegister(cpu.RegisterR2)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW color value: %w", err)
			}
			previous := uint32(0)
			if item < uint32(len(r.displayColors)) {
				previous = r.displayColors[item]
				r.displayColors[item] = value
			}
			if err := r.cpu.WriteRegister(cpu.RegisterR0, previous); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW previous color: %w", err)
			}
			return resume()
		case 5: // DrawRect(IDisplay *, AEERect *, RGBVAL, RGBVAL, flags)
			if err := r.drawDisplayRect(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 6: // BitBlt(IDisplay *, xd, yd, w, h, IBitmap *, xs, ys, rop)
			if err := r.blitDisplayBitmap(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 12: // DrawFrame(IDisplay *, AEERect *, AEEFrameType, RGBVAL)
			if err := r.drawDisplayFrame(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 8, 9: // SetAnnunciators, Backlight
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 16: // GetDeviceBitmap(IDisplay *, IBitmap **)
			output, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			if output == 0 {
				if err := r.cpu.WriteRegister(cpu.RegisterR0, 2); err != nil {
					return true, 0, cpu.ModeARM, err
				}
				return resume()
			}
			var encoded [4]byte
			binary.LittleEndian.PutUint32(encoded[:], deviceBitmapObject)
			if err := r.cpu.WriteMemory(output, encoded[:]); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("write BREW device bitmap: %w", err)
			}
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		default:
			return boundary("IDisplay", slot)
		}
	}
	if breakpoint >= bitmapTrapBase+2 && breakpoint < bitmapTrapBase+bitmapMethodCount*2+2 {
		slot := (breakpoint - 2 - bitmapTrapBase) / 2
		switch slot {
		case 2: // QueryInterface(IBitmap *, AEECLSID, void **)
			object, err := r.cpu.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			classID, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			output, err := r.cpu.ReadRegister(cpu.RegisterR2)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			const (
				bitmapInterfaceID = uint32(0x01001021)
				dib20InterfaceID  = uint32(0x0100102c)
				dibInterfaceID    = uint32(0x01001045)
			)
			if output == 0 || classID != bitmapInterfaceID && classID != dib20InterfaceID && classID != dibInterfaceID {
				if err := r.cpu.WriteRegister(cpu.RegisterR0, 1); err != nil {
					return true, 0, cpu.ModeARM, err
				}
				return resume()
			}
			var encoded [4]byte
			binary.LittleEndian.PutUint32(encoded[:], object)
			if err := r.cpu.WriteMemory(output, encoded[:]); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("write BREW bitmap interface: %w", err)
			}
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 3: // RGBToNative(IBitmap *, RGBVAL)
			rgb, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			red := uint16(rgb & 0xff)
			green := uint16((rgb >> 8) & 0xff)
			blue := uint16((rgb >> 16) & 0xff)
			native := uint32((red>>3)<<11 | (green>>2)<<5 | blue>>3)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, native); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 4: // NativeToRGB(IBitmap *, NativeColor)
			raw, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			value := uint16(raw)
			red := uint32((value >> 11) & 0x1f)
			green := uint32((value >> 5) & 0x3f)
			blue := uint32(value & 0x1f)
			rgb := (red<<3 | red>>2) | (green<<2|green>>4)<<8 | (blue<<3|blue>>2)<<16
			if err := r.cpu.WriteRegister(cpu.RegisterR0, rgb); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 6: // GetPixel(IBitmap *, unsigned, unsigned, NativeColor *)
			if err := r.returnBitmapPixel(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 10: // BltIn(IBitmap *, xDst, yDst, dx, dy, IBitmap *, xSrc, ySrc, rop)
			if err := r.blitBitmapIn(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 12: // GetInfo(IBitmap *, AEEBitmapInfo *, int)
			object, err := r.cpu.ReadRegister(cpu.RegisterR0)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			output, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			size, err := r.cpu.ReadRegister(cpu.RegisterR2)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			if output == 0 || size < 12 {
				if err := r.cpu.WriteRegister(cpu.RegisterR0, 2); err != nil {
					return true, 0, cpu.ModeARM, err
				}
				return resume()
			}
			var header [30]byte
			if err := r.cpu.ReadMemory(object, header[:]); err != nil || binary.LittleEndian.Uint32(header[:4]) != bitmapVTable {
				if err := r.cpu.WriteRegister(cpu.RegisterR0, 2); err != nil {
					return true, 0, cpu.ModeARM, err
				}
				return resume()
			}
			var info [12]byte
			binary.LittleEndian.PutUint32(info[0:4], uint32(binary.LittleEndian.Uint16(header[20:22])))
			binary.LittleEndian.PutUint32(info[4:8], uint32(binary.LittleEndian.Uint16(header[22:24])))
			binary.LittleEndian.PutUint32(info[8:12], uint32(header[28]))
			if err := r.cpu.WriteMemory(output, info[:]); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("write BREW bitmap info: %w", err)
			}
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 13: // CreateCompatibleBitmap(IBitmap *, IBitmap **, uint16, uint16)
			output, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			width, err := r.cpu.ReadRegister(cpu.RegisterR2)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			height, err := r.cpu.ReadRegister(cpu.RegisterR3)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			if output == 0 || width == 0 || height == 0 || uint64(width)*uint64(height) > maxNativeImagePixels {
				if err := r.cpu.WriteRegister(cpu.RegisterR0, 2); err != nil {
					return true, 0, cpu.ModeARM, err
				}
				return resume()
			}
			object, err := r.createNativeBitmap(image.NewRGBA(image.Rect(0, 0, int(width), int(height))))
			if err != nil {
				if err := r.cpu.WriteRegister(cpu.RegisterR0, 1); err != nil {
					return true, 0, cpu.ModeARM, err
				}
				return resume()
			}
			var encoded [4]byte
			binary.LittleEndian.PutUint32(encoded[:], object)
			if err := r.cpu.WriteMemory(output, encoded[:]); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("write compatible BREW bitmap: %w", err)
			}
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		default:
			return boundary("IBitmap", slot)
		}
	}
	if breakpoint >= graphicsTrapBase+2 && breakpoint < graphicsTrapBase+graphicsMethodCount*2+2 {
		slot := (breakpoint - 2 - graphicsTrapBase) / 2
		if handled, err := r.handleGraphicsMethod(slot); handled || err != nil {
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		}
		return boundary("IGraphics", slot)
	}
	if breakpoint >= tapiTrapBase+2 && breakpoint < tapiTrapBase+tapiMethodCount*2+2 {
		slot := (breakpoint - 2 - tapiTrapBase) / 2
		switch slot {
		case 2: // INotifier.SetMask(ITAPI *, uint32 *)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 3: // GetStatus(ITAPI *, TAPIStatus *)
			destination, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			if destination == 0 {
				if err := r.cpu.WriteRegister(cpu.RegisterR0, 2); err != nil { // EBADPARM
					return true, 0, cpu.ModeARM, err
				}
				return resume()
			}
			var status [24]byte
			copy(status[:16], []byte("000000000000000\x00"))
			status[16] = 0                                   // AEET_STATE_NONE
			binary.LittleEndian.PutUint32(status[20:], 1<<6) // registered
			if err := r.cpu.WriteMemory(destination, status[:]); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("write BREW telephony status: %w", err)
			}
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		default:
			return boundary("ITAPI", slot)
		}
	}
	if breakpoint >= soundPlayerTrapBase+2 && breakpoint < soundPlayerTrapBase+soundPlayerMethods*2+2 {
		slot := (breakpoint - 2 - soundPlayerTrapBase) / 2
		switch slot {
		case 2: // RegisterNotify(ISoundPlayer *, PFNSOUNDPLAYERSTATUS, void *)
			return resume()
		case 3: // Set(ISoundPlayer *, AEESoundPlayerInput, void *)
			// The second argument is an input discriminator, not an AEESoundInfo
			// pointer. The resource pointer remains guest-owned until Play.
			return resume()
		case 4, 5, 6, 7, 8, 9, 10, 11, 14, 15, 16: // playback/state controls
			return resume()
		case 12: // SetVolume
			volume, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			r.soundVolume = uint16(min(volume, 100))
			return resume()
		case 13: // GetVolume, delivered asynchronously on native BREW
			return resume()
		case 17, 18: // BREW 1.1 SetInfo/GetInfo
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		default:
			return boundary("ISoundPlayer", slot)
		}
	}
	if breakpoint >= netTrapBase+2 && breakpoint < netTrapBase+netMethodCount*2+2 {
		slot := (breakpoint - 2 - netTrapBase) / 2
		switch slot {
		case 2: // INotifier.SetMask(INetMgr *, const uint32 *)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 3: // GetHostByName: offline, no asynchronous resolver is available.
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 4: // GetLastError
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 1); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 5: // OpenSocket: offline runtime cannot create host sockets.
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 6: // NetStatus
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 4); err != nil { // NET_PPP_CLOSED
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 7: // GetMyIPAddr
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 8, 9, 10, 11: // Linger/event/options: no offline state to mutate.
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 1); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		default:
			return boundary("INetMgr", slot)
		}
	}
	if breakpoint >= imageTrapBase+2 && breakpoint < imageTrapBase+imageMethodCount*2+2 {
		slot := (breakpoint - 2 - imageTrapBase) / 2
		switch slot {
		case 2: // Draw(IImage *, int x, int y)
			if err := r.drawImage(false); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 3: // DrawFrame(IImage *, int frame, int x, int y)
			if err := r.drawImage(true); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 4: // GetInfo(IImage *, AEEImageInfo *)
			if err := r.returnImageInfo(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 5: // SetParm
			if err := r.setImageParameter(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 6: // Start(IImage *, int x, int y)
			if err := r.drawImage(false); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 7, 8, 10: // Stop, SetStream, Notify
			return resume()
		case 9: // HandleEvent
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		default:
			return boundary("IImage", slot)
		}
	}
	if breakpoint >= textCtlTrapBase+2 && breakpoint < textCtlTrapBase+textCtlMethodCount*2+2 {
		slot := (breakpoint - 2 - textCtlTrapBase) / 2
		if handled, err := r.handleTextControl(slot); handled || err != nil {
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		}
		return boundary("ITextCtl", slot)
	}
	if breakpoint >= menuCtlTrapBase+2 && breakpoint < menuCtlTrapBase+menuCtlMethodCount*2+2 {
		slot := (breakpoint - 2 - menuCtlTrapBase) / 2
		if handled, err := r.handleMenuControl(slot); handled || err != nil {
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		}
		return boundary("IMenuCtl", slot)
	}
	if breakpoint >= shellMethodTrapBase+2 && breakpoint < shellMethodTrapBase+shellMethodCount*2+2 {
		slot := (breakpoint - 2 - shellMethodTrapBase) / 2
		switch slot {
		case 4: // GetDeviceInfo(IShell *, AEEDeviceInfo *)
			if err := r.writeDeviceInfo(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 6: // CloseApplet
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 8: // ActiveApplet(IShell *)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, r.activeApplet); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW active applet: %w", err)
			}
			return resume()
		case 11: // SetTimer(IShell *, uint32, PFNNOTIFY, void *)
			delayMS, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW timer delay: %w", err)
			}
			function, err := r.cpu.ReadRegister(cpu.RegisterR2)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW timer callback: %w", err)
			}
			user, err := r.cpu.ReadRegister(cpu.RegisterR3)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW timer context: %w", err)
			}
			if function == 0 {
				if err := r.cpu.WriteRegister(cpu.RegisterR0, 2); err != nil {
					return true, 0, cpu.ModeARM, err
				}
				return resume()
			}
			r.timers = append(r.timers, brewCallback{
				function:  function,
				context:   user,
				remaining: time.Duration(delayMS) * time.Millisecond,
			})
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW timer status: %w", err)
			}
			return resume()
		case 12: // CancelTimer(IShell *, PFNNOTIFY, void *)
			function, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			context, err := r.cpu.ReadRegister(cpu.RegisterR2)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			kept := r.timers[:0]
			for _, timer := range r.timers {
				if timer.function != function || timer.context != context {
					kept = append(kept, timer)
				}
			}
			r.timers = kept
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 13: // GetTimerExpiration(IShell *, PFNNOTIFY, void *)
			function, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			context, err := r.cpu.ReadRegister(cpu.RegisterR2)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			remaining := uint32(0)
			for _, timer := range r.timers {
				if timer.function == function && timer.context == context {
					if timer.remaining > 0 {
						remaining = uint32(timer.remaining / time.Millisecond)
					}
					break
				}
			}
			if err := r.cpu.WriteRegister(cpu.RegisterR0, remaining); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW timer expiration: %w", err)
			}
			return resume()
		case 17: // LoadResString(IShell *, const char *, int16, AECHAR *, int)
			if err := r.loadShellResourceString(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 18: // LoadResData(IShell *, const char *, uint16, ResType)
			if err := r.loadShellResourceData(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 19: // LoadResObject(IShell *, const char *, uint16, AEECLSID)
			if err := r.loadShellResourceObject(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 20: // FreeResData(IShell *, void *)
			address, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW resource release address: %w", err)
			}
			r.releaseGuest(address)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 21: // SendEvent: cross-applet dispatch is unavailable in this runtime.
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 22: // Beep: accept the request without host audio side effects.
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 1); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 23: // GetPrefs
			if err := r.getPreferences(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 24: // SetPrefs
			if err := r.setPreferences(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 28: // MessageBoxText: acknowledge the modal message in headless mode.
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 1); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW MessageBoxText result: %w", err)
			}
			return resume()
		case 32: // GetHandler: no dynamically registered external handler.
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 36: // Resume(IShell *, AEECallback *)
			callback, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			if callback != 0 {
				data := make([]byte, 24)
				if err := r.cpu.ReadMemory(callback, data); err != nil {
					return true, 0, cpu.ModeARM, fmt.Errorf("read BREW callback: %w", err)
				}
				function := binary.LittleEndian.Uint32(data[16:20])
				context := binary.LittleEndian.Uint32(data[20:24])
				if function != 0 {
					r.timers = append(r.timers, brewCallback{function: function, context: context})
				}
			}
			return resume()
		case 41: // LoadResDataEx
			if err := r.loadShellResourceDataEx(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		default:
			return boundary("IShell", slot)
		}
	}
	if breakpoint >= heapTrapBase+2 && breakpoint < heapTrapBase+heapMethodCount*2+2 {
		slot := (breakpoint - 2 - heapTrapBase) / 2
		switch slot {
		case 2: // Malloc(IHeap *, uint32)
			size, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW heap allocation size: %w", err)
			}
			address, err := r.allocateGuest(size)
			if err != nil {
				address = 0
			}
			if err := r.cpu.WriteRegister(cpu.RegisterR0, address); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW heap allocation: %w", err)
			}
			return resume()
		case 4: // Free(IHeap *, void *)
			address, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW heap free address: %w", err)
			}
			r.releaseGuest(address)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW heap free status: %w", err)
			}
			return resume()
		case 6:
			size, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("read BREW heap availability size: %w", err)
			}
			available := heapBase + heapSize - r.heapNext
			if err := r.cpu.WriteRegister(cpu.RegisterR0, boolWord(size <= available)); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW heap availability: %w", err)
			}
			return resume()
		case 7:
			if err := r.cpu.WriteRegister(cpu.RegisterR0, r.heapNext-heapBase); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW heap usage: %w", err)
			}
			return resume()
		default:
			return boundary("IHeap", slot)
		}
	}
	if breakpoint >= soundTrapBase+2 && breakpoint < soundTrapBase+soundMethodCount*2+2 {
		slot := (breakpoint - 2 - soundTrapBase) / 2
		switch slot {
		case 2: // RegisterNotify(ISound *, PFNSOUNDSTATUS, void *)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW sound registration status: %w", err)
			}
			return resume()
		case 3: // Set(ISound *, const AEESoundInfo *)
			pointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			if pointer != 0 {
				if err := r.cpu.ReadMemory(pointer, r.soundInfo[:]); err != nil {
					return true, 0, cpu.ModeARM, fmt.Errorf("read BREW sound info: %w", err)
				}
			}
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 4: // Get(ISound *, AEESoundInfo *)
			pointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			if pointer != 0 {
				if err := r.cpu.WriteMemory(pointer, r.soundInfo[:]); err != nil {
					return true, 0, cpu.ModeARM, fmt.Errorf("write BREW sound info: %w", err)
				}
			}
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 5, 9, 11: // SetDevice, StopTone, StopVibrate
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 12: // SetVolume
			volume, err := r.cpu.ReadRegister(cpu.RegisterR1)
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			if volume > 100 {
				volume = 100
			}
			r.soundVolume = uint16(volume)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 13: // GetVolume, delivered through the registered sound callback.
			return resume()
		default:
			return boundary("ISound", slot)
		}
	}
	if breakpoint >= fileMgrTrapBase+2 && breakpoint < fileMgrTrapBase+fileMgrMethodCount*2+2 {
		slot := (breakpoint - 2 - fileMgrTrapBase) / 2
		switch slot {
		case 2: // OpenFile(IFileMgr *, const char *, OpenFileMode)
			if err := r.openGuestFile(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 3: // GetInfo(IFileMgr *, const char *, FileInfo *)
			if err := r.returnNamedFileInfo(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 4: // Remove(IFileMgr *, const char *)
			// The authenticated package is immutable. The exact first request is the
			// absent transient save "t.sre", so EFAILED is the honest result.
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 1); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW remove status: %w", err)
			}
			return resume()
		case 7: // Test(IFileMgr *, const char *)
			if err := r.testGuestFile(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 8: // GetFreeSpace(IFileMgr *, uint32 *total)
			if err := r.returnFileSpace(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		default:
			return boundary("IFileMgr", slot)
		}
	}
	if breakpoint >= fileTrapBase+2 && breakpoint < fileTrapBase+fileMethodCount*2+2 {
		slot := (breakpoint - 2 - fileTrapBase) / 2
		switch slot {
		case 3: // Read(IFile *, void *, uint32)
			if err := r.readGuestFile(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 4: // Cancel(IFile *)
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 5: // Write(IFile *, const void *, uint32)
			if err := r.writeGuestFile(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 6: // GetInfo(IFile *, FileInfo *)
			if err := r.returnFileInfo(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 7: // Seek(IFile *, FileSeekType, int32)
			if err := r.seekGuestFile(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		default:
			return boundary("IFile", slot)
		}
	}
	return false, 0, cpu.ModeARM, nil
}

// RunCallbacks advances the cooperative clock by elapsed and executes timers
// whose requested delay has expired. Callbacks rearmed by a callback remain
// queued for a later frame.
func (r *Runtime) RunCallbacks(ctx context.Context, elapsed time.Duration) error {
	if elapsed > 0 {
		r.clock += elapsed
	}
	pending := append([]brewCallback(nil), r.timers...)
	r.timers = r.timers[:0]
	for _, callback := range pending {
		callback.remaining -= elapsed
		if callback.remaining > 0 {
			r.timers = append(r.timers, callback)
			continue
		}
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0: callback.context,
			cpu.RegisterSP: stackBase + stackSize - 16,
			cpu.RegisterLR: returnTrap | 1,
		} {
			if err := r.cpu.WriteRegister(register, value); err != nil {
				return fmt.Errorf("initialize BREW callback register r%d: %w", register, err)
			}
		}
		pc, mode := branchTarget(callback.function)
		if _, err := r.runAppletCode(ctx, pc, mode, "timer callback"); err != nil {
			return err
		}
	}
	return nil
}

// Bootstrap executes the genuine module entry and module factory. A successful
// result proves executable ARM code was reached and produced its module object.
func (r *Runtime) Bootstrap(ctx context.Context) error {
	if r.moduleObject != 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for register, value := range map[uint32]uint32{
		// The offset-zero veneer reshuffles these into the generic module
		// constructor as shell and loader-helper arguments respectively.
		cpu.RegisterR0: shellObject,
		cpu.RegisterR1: helperBase,
		cpu.RegisterR2: outputAddr,
		cpu.RegisterSP: stackBase + stackSize - 16,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := r.cpu.WriteRegister(register, value); err != nil {
			return fmt.Errorf("initialize BREW register r%d: %w", register, err)
		}
	}
	var zero [4]byte
	if err := r.cpu.WriteMemory(outputAddr, zero[:]); err != nil {
		return fmt.Errorf("clear BREW module output: %w", err)
	}

	pc, mode := moduleBase, cpu.ModeARM
	for traps := 0; traps < hostCallBudget; traps++ {
		result := r.cpu.Run(ctx, pc, mode, guestInstructionBudget)
		if result.Err != nil {
			return fmt.Errorf("execute BREW module entry at PC 0x%08x: %w", result.PC, result.Err)
		}
		if result.Reason != cpu.StopBreakpoint {
			return fmt.Errorf("execute BREW module entry stopped at PC 0x%08x with reason %d", result.PC, result.Reason)
		}
		switch result.PC {
		case allocTrap + 2:
			if err := r.returnAllocation(); err != nil {
				return err
			}
			lr, err := r.cpu.ReadRegister(cpu.RegisterLR)
			if err != nil {
				return fmt.Errorf("read BREW allocator return address: %w", err)
			}
			pc, mode = branchTarget(lr)
		case returnTrap + 2:
			var encoded [4]byte
			if err := r.cpu.ReadMemory(outputAddr, encoded[:]); err != nil {
				return fmt.Errorf("read BREW module object: %w", err)
			}
			r.moduleObject = binary.LittleEndian.Uint32(encoded[:])
			if r.moduleObject < heapBase || r.moduleObject >= heapBase+heapSize {
				return fmt.Errorf("BREW module factory returned invalid object 0x%08x", r.moduleObject)
			}
			return nil
		default:
			return fmt.Errorf("unexpected BREW bootstrap breakpoint at PC 0x%08x", result.PC-2)
		}
	}
	return fmt.Errorf("BREW bootstrap exceeded host-call limit")
}

func boolWord(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

func (r *Runtime) writeDeviceInfo() error {
	address, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW device-info pointer: %w", err)
	}
	if address == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	// AEEDeviceInfo starts with eight stable uint16 fields followed by stable
	// scalar fields through dwLang. Bitfields and handset-specific extensions are
	// intentionally left zero rather than guessed.
	data := make([]byte, 44)
	for index, value := range []uint16{uint16(framebufferWidth), uint16(framebufferHeight), 0, 0, 8, 1, 0, 16} {
		binary.LittleEndian.PutUint16(data[index*2:], value)
	}
	binary.LittleEndian.PutUint32(data[24:], heapSize)
	if err := r.cpu.WriteMemory(address, data); err != nil {
		return fmt.Errorf("write BREW device info: %w", err)
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
		return fmt.Errorf("return BREW device-info status: %w", err)
	}
	return nil
}

func (r *Runtime) fillGuestMemory() error {
	address, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW memset destination: %w", err)
	}
	value, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW memset value: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW memset size: %w", err)
	}
	if size > heapSize {
		return fmt.Errorf("BREW memset size %d exceeds exact-title heap", size)
	}
	data := make([]byte, size)
	if byte(value) != 0 {
		for index := range data {
			data[index] = byte(value)
		}
	}
	if err := r.cpu.WriteMemory(address, data); err != nil {
		return fmt.Errorf("write BREW memset span at 0x%08x: %w", address, err)
	}
	return nil
}

func (r *Runtime) moveGuestMemory() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW memmove destination: %w", err)
	}
	source, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW memmove source: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW memmove size: %w", err)
	}
	if size > heapSize {
		return fmt.Errorf("BREW memmove size %d exceeds exact-title heap", size)
	}
	data := make([]byte, size)
	if err := r.cpu.ReadMemory(source, data); err != nil {
		return fmt.Errorf("read BREW memmove span at 0x%08x: %w", source, err)
	}
	if err := r.cpu.WriteMemory(destination, data); err != nil {
		return fmt.Errorf("write BREW memmove span at 0x%08x: %w", destination, err)
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, destination); err != nil {
		return fmt.Errorf("return BREW memmove destination: %w", err)
	}
	return nil
}

func (r *Runtime) returnCStringLength() error {
	address, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW strlen pointer: %w", err)
	}
	const maxCString = uint32(1 << 20)
	var one [1]byte
	for length := uint32(0); length < maxCString; length++ {
		if err := r.cpu.ReadMemory(address+length, one[:]); err != nil {
			return fmt.Errorf("read BREW strlen byte at 0x%08x: %w", address+length, err)
		}
		if one[0] == 0 {
			if err := r.cpu.WriteRegister(cpu.RegisterR0, length); err != nil {
				return fmt.Errorf("return BREW strlen result: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("BREW strlen at 0x%08x exceeded %d bytes", address, maxCString)
}

func (r *Runtime) readCString(address uint32) (string, error) {
	const maxCString = uint32(4096)
	data := make([]byte, 0, 64)
	var one [1]byte
	for offset := uint32(0); offset < maxCString; offset++ {
		if err := r.cpu.ReadMemory(address+offset, one[:]); err != nil {
			return "", fmt.Errorf("read BREW string at 0x%08x: %w", address+offset, err)
		}
		if one[0] == 0 {
			return string(data), nil
		}
		data = append(data, one[0])
	}
	return "", fmt.Errorf("BREW string at 0x%08x exceeded %d bytes", address, maxCString)
}

func (r *Runtime) copyGuestCString() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW strcpy destination: %w", err)
	}
	source, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW strcpy source: %w", err)
	}
	text, err := r.readCString(source)
	if err != nil {
		return err
	}
	if err := r.cpu.WriteMemory(destination, append([]byte(text), 0)); err != nil {
		return fmt.Errorf("write BREW strcpy destination: %w", err)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, destination)
}

func (r *Runtime) appendGuestCString() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW strcat destination: %w", err)
	}
	source, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW strcat source: %w", err)
	}
	prefix, err := r.readCString(destination)
	if err != nil {
		return err
	}
	suffix, err := r.readCString(source)
	if err != nil {
		return err
	}
	if len(prefix)+len(suffix)+1 > int(heapSize) {
		return fmt.Errorf("BREW strcat result exceeds runtime limit")
	}
	if err := r.cpu.WriteMemory(destination, append([]byte(prefix+suffix), 0)); err != nil {
		return fmt.Errorf("write BREW strcat destination: %w", err)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, destination)
}

func (r *Runtime) copyGuestWideString() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW wstrcpy destination: %w", err)
	}
	source, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW wstrcpy source: %w", err)
	}
	const maxWideStringUnits = uint32(1 << 19)
	data := make([]byte, 0, 64)
	var encoded [2]byte
	for units := uint32(0); units < maxWideStringUnits; units++ {
		address := source + units*2
		if err := r.cpu.ReadMemory(address, encoded[:]); err != nil {
			return fmt.Errorf("read BREW wstrcpy unit at 0x%08x: %w", address, err)
		}
		data = append(data, encoded[:]...)
		if binary.LittleEndian.Uint16(encoded[:]) == 0 {
			if err := r.cpu.WriteMemory(destination, data); err != nil {
				return fmt.Errorf("write BREW wstrcpy destination: %w", err)
			}
			return r.cpu.WriteRegister(cpu.RegisterR0, destination)
		}
	}
	return fmt.Errorf("BREW wstrcpy source at 0x%08x exceeded %d UTF-16 units", source, maxWideStringUnits)
}

func (r *Runtime) readGuestWideString(address uint32) ([]uint16, error) {
	const maxWideStringUnits = uint32(1 << 19)
	units := make([]uint16, 0, 32)
	var encoded [2]byte
	for index := uint32(0); index < maxWideStringUnits; index++ {
		unitAddress := address + index*2
		if err := r.cpu.ReadMemory(unitAddress, encoded[:]); err != nil {
			return nil, fmt.Errorf("read BREW wide-string unit at 0x%08x: %w", unitAddress, err)
		}
		unit := binary.LittleEndian.Uint16(encoded[:])
		if unit == 0 {
			return units, nil
		}
		units = append(units, unit)
	}
	return nil, fmt.Errorf("BREW wide string at 0x%08x exceeded %d UTF-16 units", address, maxWideStringUnits)
}

func encodeGuestWideString(units []uint16) []byte {
	encoded := make([]byte, (len(units)+1)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(encoded[index*2:], unit)
	}
	return encoded
}

func (r *Runtime) compareGuestWideStrings() error {
	leftPointer, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW wstrcmp left pointer: %w", err)
	}
	rightPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW wstrcmp right pointer: %w", err)
	}
	left, err := r.readGuestWideString(leftPointer)
	if err != nil {
		return err
	}
	right, err := r.readGuestWideString(rightPointer)
	if err != nil {
		return err
	}
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	result := int32(0)
	for index := 0; index < limit; index++ {
		if left[index] != right[index] {
			result = int32(left[index]) - int32(right[index])
			break
		}
	}
	if result == 0 && len(left) != len(right) {
		if len(left) < len(right) {
			result = -int32(right[len(left)])
		} else {
			result = int32(left[len(right)])
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, uint32(result))
}

func (r *Runtime) convertGuestStringToWide() error {
	source, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW strtowstr source: %w", err)
	}
	destination, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW strtowstr destination: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW strtowstr destination size: %w", err)
	}
	if size > heapSize {
		return fmt.Errorf("BREW strtowstr destination size %d exceeds runtime limit", size)
	}
	if size >= 2 {
		text, readErr := r.readCString(source)
		if readErr != nil {
			return readErr
		}
		decoded, _, decodeErr := transform.Bytes(korean.EUCKR.NewDecoder(), []byte(text))
		if decodeErr != nil {
			decoded = bytes.ToValidUTF8([]byte(text), []byte("\ufffd"))
		}
		units := utf16.Encode([]rune(string(decoded)))
		capacity := int(size/2) - 1
		if len(units) > capacity {
			units = units[:capacity]
		}
		if err := r.cpu.WriteMemory(destination, encodeGuestWideString(units)); err != nil {
			return fmt.Errorf("write BREW strtowstr destination: %w", err)
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, destination)
}

func (r *Runtime) convertGuestWideToString() error {
	source, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW wstrtostr source: %w", err)
	}
	destination, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW wstrtostr destination: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW wstrtostr destination size: %w", err)
	}
	if size > heapSize {
		return fmt.Errorf("BREW wstrtostr destination size %d exceeds runtime limit", size)
	}
	if size != 0 {
		units, readErr := r.readGuestWideString(source)
		if readErr != nil {
			return readErr
		}
		encoded, _, encodeErr := transform.Bytes(korean.EUCKR.NewEncoder(), []byte(string(utf16.Decode(units))))
		if encodeErr != nil {
			encoded = []byte(string(utf16.Decode(units)))
		}
		capacity := int(size) - 1
		if len(encoded) > capacity {
			encoded = encoded[:capacity]
		}
		if err := r.cpu.WriteMemory(destination, append(encoded, 0)); err != nil {
			return fmt.Errorf("write BREW wstrtostr destination: %w", err)
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, destination)
}

func (r *Runtime) returnGuestWideStringSize() error {
	address, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW wstrsize pointer: %w", err)
	}
	units, err := r.readGuestWideString(address)
	if err != nil {
		return err
	}
	size := uint32(0)
	if len(units) != 0 {
		size = uint32((len(units) + 1) * 2)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, size)
}

func (r *Runtime) copyGuestWideStringN() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW wstrncopyn destination: %w", err)
	}
	destinationBytes, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW wstrncopyn destination size: %w", err)
	}
	source, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW wstrncopyn source: %w", err)
	}
	rawSourceLength, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW wstrncopyn source length: %w", err)
	}
	if destinationBytes > heapSize {
		return fmt.Errorf("BREW wstrncopyn destination size %d exceeds runtime limit", destinationBytes)
	}
	units, err := r.readGuestWideString(source)
	if err != nil {
		return err
	}
	if sourceLength := int32(rawSourceLength); sourceLength >= 0 && int(sourceLength) < len(units) {
		units = units[:sourceLength]
	}
	capacity := int(destinationBytes / 2)
	if capacity == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	if len(units) >= capacity {
		units = units[:capacity-1]
	}
	if err := r.cpu.WriteMemory(destination, encodeGuestWideString(units)); err != nil {
		return fmt.Errorf("write BREW wstrncopyn destination: %w", err)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, uint32(len(units)))
}

func (r *Runtime) formatGuestWideString() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW wsprintf destination: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW wsprintf destination size: %w", err)
	}
	formatPointer, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW wsprintf format: %w", err)
	}
	formatUnits, err := r.readGuestWideString(formatPointer)
	if err != nil {
		return err
	}
	format := string(utf16.Decode(formatUnits))
	arg := func(index uint32) (uint32, error) {
		if index == 0 {
			return r.cpu.ReadRegister(cpu.RegisterR3)
		}
		sp, readErr := r.cpu.ReadRegister(cpu.RegisterSP)
		if readErr != nil {
			return 0, readErr
		}
		var encoded [4]byte
		if readErr := r.cpu.ReadMemory(sp+(index-1)*4, encoded[:]); readErr != nil {
			return 0, readErr
		}
		return binary.LittleEndian.Uint32(encoded[:]), nil
	}
	values := make([]any, 0, 8)
	goFormat := []byte(format)
	argumentIndex := uint32(0)
	for index := 0; index < len(format); index++ {
		if format[index] != '%' {
			continue
		}
		if index+1 < len(format) && format[index+1] == '%' {
			index++
			continue
		}
		end := index + 1
		for end < len(format) && strings.ContainsRune("-+ #0.123456789", rune(format[end])) {
			end++
		}
		if end >= len(format) || !strings.ContainsRune("cdsuXx", rune(format[end])) {
			return fmt.Errorf("BREW execution boundary: unsupported wsprintf format %q", format)
		}
		value, readErr := arg(argumentIndex)
		if readErr != nil {
			return fmt.Errorf("read BREW wsprintf argument %d: %w", argumentIndex, readErr)
		}
		switch format[end] {
		case 'c':
			values = append(values, rune(uint16(value)))
		case 's':
			units, stringErr := r.readGuestWideString(value)
			if stringErr != nil {
				return stringErr
			}
			values = append(values, string(utf16.Decode(units)))
		case 'd':
			values = append(values, int32(value))
		case 'u':
			goFormat[end] = 'd'
			values = append(values, value)
		case 'x', 'X':
			values = append(values, value)
		}
		argumentIndex++
		index = end
	}
	if size > heapSize {
		return fmt.Errorf("BREW wsprintf destination size %d exceeds runtime limit", size)
	}
	if size < 2 {
		return nil
	}
	units := utf16.Encode([]rune(fmt.Sprintf(string(goFormat), values...)))
	capacity := int(size/2) - 1
	if len(units) > capacity {
		units = units[:capacity]
	}
	if err := r.cpu.WriteMemory(destination, encodeGuestWideString(units)); err != nil {
		return fmt.Errorf("write BREW wsprintf result: %w", err)
	}
	return nil
}

func (r *Runtime) expandGuestOEMString() error {
	source, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW strexpand source: %w", err)
	}
	count, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW strexpand source length: %w", err)
	}
	destination, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW strexpand destination: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW strexpand destination size: %w", err)
	}
	if count > heapSize || size > heapSize {
		return fmt.Errorf("BREW strexpand span exceeds runtime limit")
	}
	if destination == 0 || size < 2 {
		return nil
	}
	raw := make([]byte, count)
	if count != 0 {
		if err := r.cpu.ReadMemory(source, raw); err != nil {
			return fmt.Errorf("read BREW strexpand source at 0x%08x: %w", source, err)
		}
	}
	if end := bytes.IndexByte(raw, 0); end >= 0 {
		raw = raw[:end]
	}
	decoded, _, decodeErr := transform.Bytes(korean.EUCKR.NewDecoder(), raw)
	if decodeErr != nil {
		decoded = bytes.ToValidUTF8(raw, []byte("\ufffd"))
	}
	units := utf16.Encode([]rune(string(decoded)))
	capacity := int(size/2) - 1
	if len(units) > capacity {
		units = units[:capacity]
	}
	encoded := make([]byte, (len(units)+1)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(encoded[index*2:], unit)
	}
	if err := r.cpu.WriteMemory(destination, encoded); err != nil {
		return fmt.Errorf("write BREW strexpand destination at 0x%08x: %w", destination, err)
	}
	return nil
}

func (r *Runtime) compressGuestWideString() error {
	source, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW wstrcompress source: %w", err)
	}
	rawCount, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW wstrcompress source length: %w", err)
	}
	destination, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW wstrcompress destination: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW wstrcompress destination size: %w", err)
	}
	if destination == 0 || size == 0 {
		return nil
	}
	if size > heapSize {
		return fmt.Errorf("BREW wstrcompress destination size %d exceeds runtime limit", size)
	}
	units, err := r.readGuestWideString(source)
	if err != nil {
		return err
	}
	if int32(rawCount) >= 0 && uint32(len(units)) > rawCount {
		units = units[:rawCount]
	}
	encoded, _, encodeErr := transform.Bytes(korean.EUCKR.NewEncoder(), []byte(string(utf16.Decode(units))))
	if encodeErr != nil {
		encoded = []byte(strings.Map(func(value rune) rune {
			if value <= 0x7f {
				return value
			}
			return '?'
		}, string(utf16.Decode(units))))
	}
	if uint32(len(encoded)) >= size {
		encoded = encoded[:size-1]
	}
	encoded = append(encoded, 0)
	if err := r.cpu.WriteMemory(destination, encoded); err != nil {
		return fmt.Errorf("write BREW wstrcompress destination at 0x%08x: %w", destination, err)
	}
	return nil
}

func (r *Runtime) returnGuestWideStringLength() error {
	address, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW wstrlen pointer: %w", err)
	}
	const maxWideStringUnits = uint32(1 << 19)
	var encoded [2]byte
	for units := uint32(0); units < maxWideStringUnits; units++ {
		unitAddress := address + units*2
		if err := r.cpu.ReadMemory(unitAddress, encoded[:]); err != nil {
			return fmt.Errorf("read BREW wstrlen unit at 0x%08x: %w", unitAddress, err)
		}
		if binary.LittleEndian.Uint16(encoded[:]) == 0 {
			return r.cpu.WriteRegister(cpu.RegisterR0, units)
		}
	}
	return fmt.Errorf("BREW wstrlen at 0x%08x exceeded %d UTF-16 units", address, maxWideStringUnits)
}

func (r *Runtime) compareGuestCStrings() error {
	leftPointer, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW strcmp left pointer: %w", err)
	}
	rightPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW strcmp right pointer: %w", err)
	}
	left, err := r.readCString(leftPointer)
	if err != nil {
		return err
	}
	right, err := r.readCString(rightPointer)
	if err != nil {
		return err
	}
	result := int32(strings.Compare(left, right))
	return r.cpu.WriteRegister(cpu.RegisterR0, uint32(result))
}

func (r *Runtime) compareGuestCStringsFoldASCII() error {
	leftPointer, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW stricmp left pointer: %w", err)
	}
	rightPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW stricmp right pointer: %w", err)
	}
	left, err := r.readCString(leftPointer)
	if err != nil {
		return err
	}
	right, err := r.readCString(rightPointer)
	if err != nil {
		return err
	}
	fold := func(value byte) byte {
		if value >= 'A' && value <= 'Z' {
			return value + ('a' - 'A')
		}
		return value
	}
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	result := int32(0)
	for index := 0; index < limit; index++ {
		leftByte, rightByte := fold(left[index]), fold(right[index])
		if leftByte != rightByte {
			result = int32(leftByte) - int32(rightByte)
			break
		}
	}
	if result == 0 && len(left) != len(right) {
		if len(left) < len(right) {
			result = -int32(fold(right[len(left)]))
		} else {
			result = int32(fold(left[len(right)]))
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, uint32(result))
}

func (r *Runtime) findGuestCString() error {
	haystackPointer, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW strstr haystack pointer: %w", err)
	}
	needlePointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW strstr needle pointer: %w", err)
	}
	haystack, err := r.readCString(haystackPointer)
	if err != nil {
		return err
	}
	needle, err := r.readCString(needlePointer)
	if err != nil {
		return err
	}
	result := uint32(0)
	if index := strings.Index(haystack, needle); index >= 0 {
		result = haystackPointer + uint32(index)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, result)
}

func (r *Runtime) findGuestCStringByte() error {
	pointer, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW strchr string pointer: %w", err)
	}
	rawValue, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW strchr value: %w", err)
	}
	text, err := r.readCString(pointer)
	if err != nil {
		return err
	}
	result := uint32(0)
	value := byte(rawValue)
	if value == 0 {
		result = pointer + uint32(len(text))
	} else if index := strings.IndexByte(text, value); index >= 0 {
		result = pointer + uint32(index)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, result)
}

func (r *Runtime) findLastGuestCStringByte() error {
	pointer, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW strrchr string pointer: %w", err)
	}
	rawValue, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW strrchr value: %w", err)
	}
	text, err := r.readCString(pointer)
	if err != nil {
		return err
	}
	result := uint32(0)
	value := byte(rawValue)
	if value == 0 {
		result = pointer + uint32(len(text))
	} else if index := strings.LastIndexByte(text, value); index >= 0 {
		result = pointer + uint32(index)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, result)
}

func (r *Runtime) findGuestCStringFoldASCII() error {
	haystackPointer, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW stristr haystack pointer: %w", err)
	}
	needlePointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW stristr needle pointer: %w", err)
	}
	haystack, err := r.readCString(haystackPointer)
	if err != nil {
		return err
	}
	needle, err := r.readCString(needlePointer)
	if err != nil {
		return err
	}
	foldASCII := func(text string) string {
		folded := []byte(text)
		for index, value := range folded {
			if value >= 'A' && value <= 'Z' {
				folded[index] = value + ('a' - 'A')
			}
		}
		return string(folded)
	}
	result := uint32(0)
	if index := strings.Index(foldASCII(haystack), foldASCII(needle)); index >= 0 {
		result = haystackPointer + uint32(index)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, result)
}

func (r *Runtime) parseGuestInteger() error {
	pointer, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW atoi pointer: %w", err)
	}
	text, err := r.readCString(pointer)
	if err != nil {
		return err
	}
	index := 0
	for index < len(text) {
		switch text[index] {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			index++
		default:
			goto sign
		}
	}
sign:
	negative := false
	if index < len(text) && (text[index] == '+' || text[index] == '-') {
		negative = text[index] == '-'
		index++
	}
	var value uint32
	for index < len(text) && text[index] >= '0' && text[index] <= '9' {
		value = value*10 + uint32(text[index]-'0')
		index++
	}
	if negative {
		value = uint32(-int32(value))
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, value)
}

func (r *Runtime) fillGuestRandom() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW random destination: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW random size: %w", err)
	}
	if size > heapSize {
		return fmt.Errorf("BREW random size %d exceeds runtime limit", size)
	}
	data := make([]byte, size)
	for index := range data {
		r.randomState = r.randomState*1_103_515_245 + 12_345
		data[index] = byte(r.randomState >> 16)
	}
	if err := r.cpu.WriteMemory(destination, data); err != nil {
		return fmt.Errorf("write BREW random bytes: %w", err)
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
		return fmt.Errorf("return BREW random status: %w", err)
	}
	return nil
}

func (r *Runtime) copyGuestStringN() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW strncpy destination: %w", err)
	}
	source, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW strncpy source: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW strncpy size: %w", err)
	}
	if size > heapSize {
		return fmt.Errorf("BREW strncpy size %d exceeds runtime limit", size)
	}
	data := make([]byte, size)
	var one [1]byte
	terminated := false
	for index := uint32(0); index < size && !terminated; index++ {
		if err := r.cpu.ReadMemory(source+index, one[:]); err != nil {
			return fmt.Errorf("read BREW strncpy source byte: %w", err)
		}
		data[index] = one[0]
		terminated = one[0] == 0
	}
	if err := r.cpu.WriteMemory(destination, data); err != nil {
		return fmt.Errorf("write BREW strncpy destination: %w", err)
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, destination); err != nil {
		return fmt.Errorf("return BREW strncpy destination: %w", err)
	}
	return nil
}

func (r *Runtime) compareGuestStringsN() error {
	left, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW strncmp left pointer: %w", err)
	}
	right, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW strncmp right pointer: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW strncmp size: %w", err)
	}
	if size > heapSize {
		return fmt.Errorf("BREW strncmp size %d exceeds runtime limit", size)
	}
	var leftByte, rightByte [1]byte
	result := int32(0)
	for index := uint32(0); index < size; index++ {
		if err := r.cpu.ReadMemory(left+index, leftByte[:]); err != nil {
			return fmt.Errorf("read BREW strncmp left byte: %w", err)
		}
		if err := r.cpu.ReadMemory(right+index, rightByte[:]); err != nil {
			return fmt.Errorf("read BREW strncmp right byte: %w", err)
		}
		if leftByte[0] != rightByte[0] {
			result = int32(leftByte[0]) - int32(rightByte[0])
			break
		}
		if leftByte[0] == 0 {
			break
		}
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, uint32(result)); err != nil {
		return fmt.Errorf("return BREW strncmp result: %w", err)
	}
	return nil
}

func (r *Runtime) compareGuestMemory() error {
	left, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW memcmp left pointer: %w", err)
	}
	right, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW memcmp right pointer: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW memcmp size: %w", err)
	}
	if size > heapSize {
		return fmt.Errorf("BREW memcmp size %d exceeds runtime limit", size)
	}
	leftData, rightData := make([]byte, size), make([]byte, size)
	if err := r.cpu.ReadMemory(left, leftData); err != nil {
		return fmt.Errorf("read BREW memcmp left span: %w", err)
	}
	if err := r.cpu.ReadMemory(right, rightData); err != nil {
		return fmt.Errorf("read BREW memcmp right span: %w", err)
	}
	result := int32(0)
	for index := range leftData {
		if leftData[index] != rightData[index] {
			result = int32(leftData[index]) - int32(rightData[index])
			break
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, uint32(result))
}

func (r *Runtime) writeGuestFile() error {
	source, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW file write source: %w", err)
	}
	count, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW file write count: %w", err)
	}
	if count > heapSize {
		return fmt.Errorf("BREW file write count %d exceeds runtime limit", count)
	}
	data := make([]byte, count)
	if err := r.cpu.ReadMemory(source, data); err != nil {
		return fmt.Errorf("read BREW file write data: %w", err)
	}
	end := r.fileOffset + count
	if end < r.fileOffset || end > heapSize {
		return fmt.Errorf("BREW file write extent %d exceeds runtime limit", end)
	}
	if uint32(len(r.currentFile)) < end {
		grown := make([]byte, end)
		copy(grown, r.currentFile)
		r.currentFile = grown
	}
	copy(r.currentFile[r.fileOffset:end], data)
	r.fileOffset = end
	if r.currentPath != "" {
		r.files[r.currentPath] = r.currentFile
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, count)
}

func (r *Runtime) openGuestFile() error {
	pathPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW file path pointer: %w", err)
	}
	path, err := r.readCString(pathPointer)
	if err != nil {
		return err
	}
	normalized := normalizeGuestPath(path)
	contents, resolved, ok := r.lookupGuestFile(normalized)
	result := uint32(0)
	if ok {
		r.currentFile = contents
		r.currentPath = resolved
		r.fileOffset = 0
		result = fileObject
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, result); err != nil {
		return fmt.Errorf("return BREW file object: %w", err)
	}
	return nil
}

func (r *Runtime) testGuestFile() error {
	pathPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW tested path pointer: %w", err)
	}
	path, err := r.readCString(pathPointer)
	if err != nil {
		return err
	}
	normalized := normalizeGuestPath(path)
	_, _, ok := r.lookupGuestFile(normalized)
	status := uint32(1)
	if ok {
		status = 0
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, status); err != nil {
		return fmt.Errorf("return BREW file test status: %w", err)
	}
	return nil
}

func normalizeGuestPath(path string) string {
	return strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "fs:/~/")
}

func (r *Runtime) lookupGuestFile(normalized string) ([]byte, string, bool) {
	if contents, ok := r.files[normalized]; ok {
		return contents, normalized, true
	}
	suffix := "/" + normalized
	var match string
	for name := range r.files {
		if strings.EqualFold(name, normalized) ||
			len(name) >= len(suffix) && strings.EqualFold(name[len(name)-len(suffix):], suffix) {
			if match != "" {
				return nil, "", false
			}
			match = name
		}
	}
	if match == "" {
		return nil, "", false
	}
	return r.files[match], match, true
}

func (r *Runtime) returnNamedFileInfo() error {
	pathPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW file-info path pointer: %w", err)
	}
	out, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW file-info output pointer: %w", err)
	}
	path, err := r.readCString(pathPointer)
	if err != nil {
		return err
	}
	contents, _, ok := r.lookupGuestFile(normalizeGuestPath(path))
	if !ok || out == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 1)
	}
	data := make([]byte, 12)
	binary.LittleEndian.PutUint32(data[8:12], uint32(len(contents)))
	if err := r.cpu.WriteMemory(out, data); err != nil {
		return fmt.Errorf("write BREW named file info: %w", err)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, 0)
}

func (r *Runtime) returnFileSpace() error {
	totalOut, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW file-space output: %w", err)
	}
	const total = uint32(512 * 1024 * 1024)
	const free = uint32(256 * 1024 * 1024)
	if totalOut != 0 {
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], total)
		if err := r.cpu.WriteMemory(totalOut, encoded[:]); err != nil {
			return fmt.Errorf("write BREW total file space: %w", err)
		}
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, free); err != nil {
		return fmt.Errorf("return BREW free file space: %w", err)
	}
	return nil
}

func (r *Runtime) formatResourceName() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW sprintf destination: %w", err)
	}
	formatPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW sprintf format: %w", err)
	}
	format, err := r.readCString(formatPointer)
	if err != nil {
		return err
	}
	arg := func(index uint32) (int32, error) {
		if index < 2 {
			value, readErr := r.cpu.ReadRegister(cpu.RegisterR2 + index)
			return int32(value), readErr
		}
		sp, readErr := r.cpu.ReadRegister(cpu.RegisterSP)
		if readErr != nil {
			return 0, readErr
		}
		var encoded [4]byte
		if readErr := r.cpu.ReadMemory(sp+(index-2)*4, encoded[:]); readErr != nil {
			return 0, readErr
		}
		return int32(binary.LittleEndian.Uint32(encoded[:])), nil
	}
	values := make([]any, 0, 8)
	goFormat := []byte(format)
	argumentIndex := uint32(0)
	for index := 0; index < len(format); index++ {
		if format[index] != '%' {
			continue
		}
		if index+1 < len(format) && format[index+1] == '%' {
			index++
			continue
		}
		end := index + 1
		for end < len(format) && strings.ContainsRune("-+ #0.123456789", rune(format[end])) {
			end++
		}
		if end >= len(format) || !strings.ContainsRune("cdsuXx", rune(format[end])) {
			return fmt.Errorf("BREW execution boundary: unsupported sprintf format %q", format)
		}
		value, readErr := arg(argumentIndex)
		if readErr != nil {
			return fmt.Errorf("read BREW sprintf argument %d: %w", argumentIndex, readErr)
		}
		switch format[end] {
		case 'c':
			values = append(values, rune(uint8(value)))
		case 's':
			text, readErr := r.readCString(uint32(value))
			if readErr != nil {
				return readErr
			}
			values = append(values, text)
		case 'd':
			values = append(values, value)
		case 'u':
			goFormat[end] = 'd'
			values = append(values, uint32(value))
		case 'x', 'X':
			values = append(values, uint32(value))
		}
		argumentIndex++
		index = end
	}
	text := fmt.Sprintf(string(goFormat), values...)
	data := append([]byte(text), 0)
	if err := r.cpu.WriteMemory(destination, data); err != nil {
		return fmt.Errorf("write BREW sprintf result: %w", err)
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, uint32(len(text))); err != nil {
		return fmt.Errorf("return BREW sprintf length: %w", err)
	}
	return nil
}

func (r *Runtime) returnFileInfo() error {
	address, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW file-info output: %w", err)
	}
	if address == 0 || r.currentFile == nil {
		return r.cpu.WriteRegister(cpu.RegisterR0, 1)
	}
	// This title allocates a 0x4c-byte FileInfo but only authenticates the stable
	// prefix and reads size at +8. Do not guess the remaining packed layout.
	data := make([]byte, 12)
	binary.LittleEndian.PutUint32(data[0:4], 0) // FA_NORMAL
	binary.LittleEndian.PutUint32(data[4:8], 0) // no fabricated timestamp
	binary.LittleEndian.PutUint32(data[8:12], uint32(len(r.currentFile)))
	if err := r.cpu.WriteMemory(address, data); err != nil {
		return fmt.Errorf("write BREW file info: %w", err)
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
		return fmt.Errorf("return BREW file-info status: %w", err)
	}
	return nil
}

func (r *Runtime) seekGuestFile() error {
	origin, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW seek origin: %w", err)
	}
	rawOffset, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW seek offset: %w", err)
	}
	var base int64
	switch origin {
	case 0:
		base = 0
	case 1:
		base = int64(len(r.currentFile))
	case 2:
		base = int64(r.fileOffset)
	default:
		return r.cpu.WriteRegister(cpu.RegisterR0, 1)
	}
	next := base + int64(int32(rawOffset))
	if next < 0 || next > int64(len(r.currentFile)) {
		return r.cpu.WriteRegister(cpu.RegisterR0, 1)
	}
	r.fileOffset = uint32(next)
	result := uint32(0)
	if origin == 2 && rawOffset == 0 {
		result = r.fileOffset
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, result); err != nil {
		return fmt.Errorf("return BREW seek position: %w", err)
	}
	return nil
}

func (r *Runtime) readGuestFile() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW file destination: %w", err)
	}
	requested, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW file byte count: %w", err)
	}
	remaining := uint32(len(r.currentFile)) - min(r.fileOffset, uint32(len(r.currentFile)))
	count := min(requested, remaining)
	if count != 0 {
		data := r.currentFile[r.fileOffset : r.fileOffset+count]
		if err := r.cpu.WriteMemory(destination, data); err != nil {
			return fmt.Errorf("write BREW file read buffer: %w", err)
		}
		r.fileOffset += count
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, count); err != nil {
		return fmt.Errorf("return BREW file read count: %w", err)
	}
	return nil
}

func (r *Runtime) createShellInstance() error {
	classID, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW shell class ID: %w", err)
	}
	var object uint32
	status := uint32(0)
	switch classID {
	case 0x01001000: // AEECLSID_SHELL
		object = shellObject
	case DisplayClassID:
		object = displayObject
	case HeapClassID:
		object = heapObject
	case FileMgrClassID:
		object = fileMgrObject
	case OptionalDeviceClassID:
		object = deviceModelObject
	case KTFServiceClassID:
		object = ktfServiceObject
	case Sound10ClassID:
		object = soundObject
	case SoundPlayerClassID:
		object = soundPlayerObject
	case GraphicsClassID:
		object = graphicsObject
	case TAPIClassID:
		object = tapiObject
	case Net11ClassID:
		object = netObject
	case TextCtl10ClassID:
		object = textCtlObject
	case IconViewCtl10ClassID, 0x01003000, 0x01003005, 0x01003007:
		// MenuCtl, DateCtl and ClockCtl share the stable IControl prefix used by
		// these legacy titles. The headless menu implementation supplies that
		// stateful prefix and safely rejects control-specific extensions.
		object = menuCtlObject
	default:
		// ISHELL_CreateInstance reports unsupported optional handset services to
		// the guest. Treating feature discovery as a fatal execution boundary
		// prevents applications from taking their documented fallback path.
		status = 3 // AEE_ECLASSNOTSUPPORT
	}
	out, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW shell output pointer: %w", err)
	}
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], object)
	if err := r.cpu.WriteMemory(out, encoded[:]); err != nil {
		return fmt.Errorf("write BREW shell object: %w", err)
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, status); err != nil {
		return fmt.Errorf("return BREW shell creation status: %w", err)
	}
	return nil
}

func (r *Runtime) returnDeviceModel() error {
	address, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW device-model buffer: %w", err)
	}
	if address == 0 {
		return fmt.Errorf("BREW device-model buffer is null")
	}
	if err := r.cpu.WriteMemory(address, []byte("KTF-X2000\x00")); err != nil {
		return fmt.Errorf("write BREW device model: %w", err)
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, 1); err != nil {
		return fmt.Errorf("return BREW device-model success: %w", err)
	}
	return nil
}

func (r *Runtime) framebufferMutated() (bool, error) {
	pixels := make([]byte, framebufferBytes)
	if err := r.cpu.ReadMemory(framebufferBase, pixels); err != nil {
		return false, fmt.Errorf("read BREW guest framebuffer: %w", err)
	}
	for _, value := range pixels {
		if value != 0 {
			return true, nil
		}
	}
	return false, nil
}

// commitFramebufferUpdate records every guest Update call, but only publishes
// a frame after the native RGB565 surface differs from the last committed
// surface. An untouched zero-filled framebuffer is not rendering evidence.
func (r *Runtime) commitFramebufferUpdate() error {
	pixels := make([]byte, framebufferBytes)
	if err := r.cpu.ReadMemory(framebufferBase, pixels); err != nil {
		return fmt.Errorf("snapshot BREW guest framebuffer: %w", err)
	}
	r.updates++
	changed := false
	if len(r.presented) == len(pixels) {
		changed = !bytes.Equal(r.presented, pixels)
	} else {
		for _, value := range pixels {
			if value != 0 {
				changed = true
				break
			}
		}
	}
	if changed {
		r.presented = pixels
		r.guestFrame = true
	}
	return nil
}

func (r *Runtime) drawDisplayRect() error {
	rectPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW display rectangle: %w", err)
	}
	fill, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW display fill color: %w", err)
	}
	sp, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return fmt.Errorf("read BREW display stack: %w", err)
	}
	var encoded [8]byte
	if err := r.cpu.ReadMemory(sp, encoded[:4]); err != nil {
		return fmt.Errorf("read BREW DrawRect flags: %w", err)
	}
	flags := binary.LittleEndian.Uint32(encoded[:4])
	if flags != 0 && flags&2 == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	if err := r.fillDisplayRectangle(rectPointer, fill); err != nil {
		return err
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
		return fmt.Errorf("return BREW DrawRect status: %w", err)
	}
	return nil
}

func (r *Runtime) drawDisplayFrame() error {
	rectPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW frame rectangle: %w", err)
	}
	fill, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW frame fill color: %w", err)
	}
	if err := r.fillDisplayRectangle(rectPointer, fill); err != nil {
		return err
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
		return fmt.Errorf("return BREW DrawFrame status: %w", err)
	}
	return nil
}

func (r *Runtime) fillDisplayRectangle(rectPointer, fill uint32) error {
	var encoded [8]byte
	x, y, width, height := int32(0), int32(0), int32(framebufferWidth), int32(framebufferHeight)
	if rectPointer != 0 {
		if err := r.cpu.ReadMemory(rectPointer, encoded[:]); err != nil {
			return fmt.Errorf("read BREW DrawRect bounds: %w", err)
		}
		x = int32(int16(binary.LittleEndian.Uint16(encoded[0:2])))
		y = int32(int16(binary.LittleEndian.Uint16(encoded[2:4])))
		width = int32(int16(binary.LittleEndian.Uint16(encoded[4:6])))
		height = int32(int16(binary.LittleEndian.Uint16(encoded[6:8])))
	}
	red := uint16(fill & 0xff)
	green := uint16((fill >> 8) & 0xff)
	blue := uint16((fill >> 16) & 0xff)
	native := (red>>3)<<11 | (green>>2)<<5 | blue>>3
	row := make([]byte, framebufferWidth*2)
	for index := uint32(0); index < framebufferWidth; index++ {
		binary.LittleEndian.PutUint16(row[index*2:], native)
	}
	left := max(x, 0)
	top := max(y, 0)
	right := min(x+width, int32(framebufferWidth))
	bottom := min(y+height, int32(framebufferHeight))
	if right > left {
		row = row[:uint32(right-left)*2]
		for py := top; py < bottom; py++ {
			address := framebufferBase + uint32(py)*framebufferWidth*2 + uint32(left)*2
			if err := r.cpu.WriteMemory(address, row); err != nil {
				return fmt.Errorf("write BREW DrawRect row: %w", err)
			}
		}
	}
	return nil
}

func (r *Runtime) returnFontMetrics() error {
	ascentOut, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW font ascent output: %w", err)
	}
	descentOut, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW font descent output: %w", err)
	}
	var encoded [4]byte
	for address, value := range map[uint32]uint32{ascentOut: 12, descentOut: 4} {
		if address == 0 {
			continue
		}
		binary.LittleEndian.PutUint32(encoded[:], value)
		if err := r.cpu.WriteMemory(address, encoded[:]); err != nil {
			return fmt.Errorf("write BREW font metric: %w", err)
		}
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, 16); err != nil {
		return fmt.Errorf("return BREW font height: %w", err)
	}
	return nil
}

func (r *Runtime) measureDisplayText() error {
	text, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW measured text: %w", err)
	}
	rawCount, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW measured character count: %w", err)
	}
	count := uint32(0)
	if int32(rawCount) >= 0 {
		count = rawCount
	} else {
		var encoded [2]byte
		for count < 4096 {
			if err := r.cpu.ReadMemory(text+count*2, encoded[:]); err != nil {
				return fmt.Errorf("read BREW measured AECHAR: %w", err)
			}
			if binary.LittleEndian.Uint16(encoded[:]) == 0 {
				break
			}
			count++
		}
	}
	sp, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return fmt.Errorf("read BREW MeasureTextEx stack: %w", err)
	}
	var encoded [4]byte
	if err := r.cpu.ReadMemory(sp, encoded[:]); err != nil {
		return fmt.Errorf("read BREW maximum text width: %w", err)
	}
	maxWidth := int32(binary.LittleEndian.Uint32(encoded[:]))
	fits := count
	if maxWidth >= 0 {
		fits = min(count, uint32(maxWidth/7))
	}
	if err := r.cpu.ReadMemory(sp+4, encoded[:]); err != nil {
		return fmt.Errorf("read BREW text-fit output: %w", err)
	}
	fitOut := binary.LittleEndian.Uint32(encoded[:])
	if fitOut != 0 {
		binary.LittleEndian.PutUint32(encoded[:], fits)
		if err := r.cpu.WriteMemory(fitOut, encoded[:]); err != nil {
			return fmt.Errorf("write BREW text-fit count: %w", err)
		}
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, fits*7); err != nil {
		return fmt.Errorf("return BREW measured text width: %w", err)
	}
	return nil
}

// Framebuffer returns a detached RGBA conversion of the title's RGB565 native
// surface. presented is true only after guest code mutates and updates it.
func (r *Runtime) Framebuffer() (frame *image.RGBA, presented bool, err error) {
	pixels := r.presented
	frame = image.NewRGBA(image.Rect(0, 0, int(framebufferWidth), int(framebufferHeight)))
	if len(pixels) != int(framebufferBytes) {
		return frame, false, nil
	}
	for offset := uint32(0); offset < framebufferBytes; offset += 2 {
		value := binary.LittleEndian.Uint16(pixels[offset:])
		red := uint8((value >> 11) & 0x1f)
		green := uint8((value >> 5) & 0x3f)
		blue := uint8(value & 0x1f)
		index := offset / 2
		frame.SetRGBA(int(index%framebufferWidth), int(index/framebufferWidth), color.RGBA{
			R: red<<3 | red>>2,
			G: green<<2 | green>>4,
			B: blue<<3 | blue>>2,
			A: 0xff,
		})
	}
	return frame, r.guestFrame, nil
}

func (r *Runtime) hostReturnTarget() (uint32, cpu.Mode, error) {
	lr, err := r.cpu.ReadRegister(cpu.RegisterLR)
	if err != nil {
		return 0, cpu.ModeARM, fmt.Errorf("read BREW host-call return address: %w", err)
	}
	pc, mode := branchTarget(lr)
	return pc, mode, nil
}

func (r *Runtime) returnAllocation() error {
	size, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW allocation size: %w", err)
	}
	address, err := r.allocateGuest(size)
	if err != nil {
		// BREW malloc reports allocation failure with NULL. Guest-controlled zero
		// sizes and bounded-arena exhaustion are not host runtime faults.
		address = 0
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, address); err != nil {
		return fmt.Errorf("return BREW allocation: %w", err)
	}
	return nil
}

func branchTarget(address uint32) (uint32, cpu.Mode) {
	if address&1 != 0 {
		return address &^ 1, cpu.ModeThumb
	}
	return address &^ 3, cpu.ModeARM
}

func (r *Runtime) ModuleObject() uint32 { return r.moduleObject }

// FrameStats reports guest IDisplay::Update calls and whether one of those
// calls committed a changed framebuffer.
func (r *Runtime) FrameStats() (presentCount uint64, frameValid bool) {
	return r.updates, r.guestFrame
}

// EventCount reports successfully completed HandleEvent dispatches by AEEEvent.
func (r *Runtime) EventCount(event uint32) uint64 { return r.eventCounts[event] }

func (r *Runtime) Close() error {
	if r == nil || r.cpu == nil {
		return nil
	}
	return r.cpu.Close()
}
