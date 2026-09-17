package brewrt

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"strings"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

const (
	moduleBase              = uint32(0x01000000)
	helperBase              = uint32(0x02000000)
	allocTrap               = helperBase + 0x800
	returnTrap              = helperBase + 0x802
	shellTrap               = helperBase + 0x804
	freeTrap                = helperBase + 0x806
	addRefTrap              = helperBase + 0x808
	releaseTrap             = helperBase + 0x80a
	displayTrapBase         = helperBase + 0x900
	shellMethodTrapBase     = helperBase + 0xa00
	heapTrapBase            = helperBase + 0xb00
	helperMethodTrapBase    = helperBase + 0xc00
	displayMeasureAnchor    = helperBase + 0xd00
	deviceModelTrap         = helperBase + 0xd40
	soundTrapBase           = helperBase + 0xe00
	shellObject             = helperBase + 0x100
	shellVTable             = helperBase + 0x200
	displayObject           = helperBase + 0x300
	displayVTable           = helperBase + 0x400
	heapObject              = helperBase + 0x500
	heapVTable              = helperBase + 0x600
	deviceModelObject       = helperBase + 0x700
	deviceModelVTable       = helperBase + 0x720
	soundObject             = helperBase + 0x740
	soundVTable             = helperBase + 0x760
	displayMethodCount      = uint32(26)
	shellMethodCount        = uint32(52)
	heapMethodCount         = uint32(9)
	soundMethodCount        = uint32(15)
	helperMethodCount       = uint32(64)
	helperMemmoveSlot       = uint32(0)
	helperMemsetSlot        = uint32(1)
	helperStrlenSlot        = uint32(5)
	helperSprintfSlot       = uint32(8)
	helperDbgPrintfSlot     = uint32(39)
	helperGetAEEVersionSlot = uint32(35)
	helperGetTimeMSSlot     = uint32(43)
	helperCurrentAppletSlot = uint32(0x30)
	// The exact title branches explicitly for BREW 1.0, 1.2 and 2.1. Its KTF
	// handset path is the BREW 2.1 branch.
	aeeVersion         = uint32(0x02010000)
	heapBase           = uint32(0x03000000)
	heapSize           = uint32(0x00100000)
	stackBase          = uint32(0x04000000)
	stackSize          = uint32(0x00010000)
	outputAddr         = stackBase + 0x100
	framebufferBase    = uint32(0x05000000)
	framebufferWidth   = uint32(120)
	framebufferHeight  = uint32(160)
	framebufferBytes   = framebufferWidth * framebufferHeight * 2
	framebufferMapSize = uint32(0x0000a000)
	serviceBase        = helperBase + 0x1000
	fileMgrObject      = serviceBase + 0x100
	fileMgrVTable      = serviceBase + 0x200
	fileMgrTrapBase    = serviceBase + 0x400
	fileMgrMethodCount = uint32(21)
	fileObject         = serviceBase + 0x500
	fileVTable         = serviceBase + 0x600
	fileTrapBase       = serviceBase + 0x700
	fileMethodCount    = uint32(12)

	bootstrapBudget = uint64(2_000_000)
	hostCallBudget  = 4096
)

// Runtime executes the authenticated module and applet ARM code with the
// portable interpreter. It exposes only service methods reached and verified by
// the exact title; every unknown class or vtable slot remains a typed boundary.
type Runtime struct {
	cpu           cpu.Backend
	moduleObject  uint32
	appletObject  uint32
	activeApplet  uint32
	heapNext      uint32
	updates       uint64
	guestFrame    bool
	displayColors [16]uint32
	eventCounts   map[uint32]uint64
	files         map[string][]byte
	currentFile   []byte
	currentPath   string
	fileOffset    uint32
	timers        []brewCallback
	boundary      *ExecutionBoundaryError
}

type brewCallback struct {
	function uint32
	context  uint32
}

// ExecutionBoundaryError identifies the first unimplemented proprietary ABI
// request without pretending that a package splash is a guest-rendered frame.
type ExecutionBoundaryError struct {
	ClassID       uint32
	Interface     string
	MethodSlot    uint32
	GuestReturnPC uint32
}

func (e *ExecutionBoundaryError) Error() string {
	if e.Interface != "" {
		return fmt.Sprintf(
			"BREW execution boundary: %s vtable slot %d is not implemented (guest return PC 0x%08x)",
			e.Interface,
			e.MethodSlot,
			e.GuestReturnPC,
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
	r := &Runtime{cpu: backend, heapNext: heapBase, files: pkg.Files, eventCounts: make(map[uint32]uint64)}
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
	binary.LittleEndian.PutUint32(imageData[4:8], helperBase)
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
	var helper [0x1000]byte
	// The second loader-prefix word points at this 64-entry helper table. Unknown
	// helpers remain distinct typed traps rather than fabricated successes.
	for slot := uint32(0); slot < helperMethodCount; slot++ {
		trap := helperMethodTrapBase + slot*2
		binary.LittleEndian.PutUint16(helper[trap-helperBase:], 0xbe08)
		binary.LittleEndian.PutUint32(helper[slot*4:], trap|1)
	}
	binary.LittleEndian.PutUint32(helper[0x68:], allocTrap|1)
	binary.LittleEndian.PutUint32(helper[0x6c:], freeTrap|1)
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
	if err := r.cpu.WriteMemory(serviceBase, services[:]); err != nil {
		return fmt.Errorf("write BREW exact-title services: %w", err)
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
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: r.moduleObject,
		cpu.RegisterR1: shellObject,
		cpu.RegisterR2: ClassID,
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
	if _, err := r.runAppletCode(ctx, pc, mode, "constructor"); err != nil {
		return err
	}
	if err := r.cpu.ReadMemory(outputAddr+4, encoded[:]); err != nil {
		return fmt.Errorf("read BREW applet object: %w", err)
	}
	r.appletObject = binary.LittleEndian.Uint32(encoded[:])
	if r.appletObject < heapBase || r.appletObject >= heapBase+heapSize {
		return fmt.Errorf("BREW applet factory returned invalid object 0x%08x", r.appletObject)
	}
	r.activeApplet = r.appletObject
	return nil
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
	for traps := 0; traps < hostCallBudget; traps++ {
		result := r.cpu.Run(ctx, pc, mode, bootstrapBudget)
		if result.Err != nil {
			return 0, fmt.Errorf("execute BREW applet %s at PC 0x%08x: %w", operation, result.PC, result.Err)
		}
		if result.Reason != cpu.StopBreakpoint {
			return 0, fmt.Errorf("execute BREW applet %s stopped at PC 0x%08x with reason %d", operation, result.PC, result.Reason)
		}
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
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return 0, fmt.Errorf("return BREW free status: %w", err)
			}
		case shellTrap + 2:
			if err := r.createShellInstance(); err != nil {
				return 0, err
			}
		case addRefTrap + 2, releaseTrap + 2:
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 1); err != nil {
				return 0, fmt.Errorf("return BREW interface reference count: %w", err)
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
		r.boundary = &ExecutionBoundaryError{
			Interface:     iface,
			MethodSlot:    slot,
			GuestReturnPC: returnAddress &^ 1,
		}
		return true, 0, cpu.ModeARM, r.boundary
	}
	if breakpoint == deviceModelTrap+2 {
		if err := r.returnDeviceModel(); err != nil {
			return true, 0, cpu.ModeARM, err
		}
		return resume()
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
		case helperStrlenSlot:
			if err := r.returnCStringLength(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperSprintfSlot:
			if err := r.formatResourceName(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case helperGetAEEVersionSlot:
			if err := r.cpu.WriteRegister(cpu.RegisterR0, aeeVersion); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW AEE version: %w", err)
			}
			return resume()
		case helperDbgPrintfSlot, helperGetTimeMSSlot:
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW helper slot %d result: %w", slot, err)
			}
			return resume()
		case helperCurrentAppletSlot:
			if err := r.cpu.WriteRegister(cpu.RegisterR0, r.activeApplet); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW current applet: %w", err)
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
			r.updates++
			changed, err := r.framebufferMutated()
			if err != nil {
				return true, 0, cpu.ModeARM, err
			}
			r.guestFrame = r.guestFrame || changed
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
		default:
			return boundary("IDisplay", slot)
		}
	}
	if breakpoint >= shellMethodTrapBase+2 && breakpoint < shellMethodTrapBase+shellMethodCount*2+2 {
		slot := (breakpoint - 2 - shellMethodTrapBase) / 2
		switch slot {
		case 4: // GetDeviceInfo(IShell *, AEEDeviceInfo *)
			if err := r.writeDeviceInfo(); err != nil {
				return true, 0, cpu.ModeARM, err
			}
			return resume()
		case 11: // SetTimer(IShell *, uint32, PFNNOTIFY, void *)
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
			r.timers = append(r.timers, brewCallback{function: function, context: user})
			if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				return true, 0, cpu.ModeARM, fmt.Errorf("return BREW timer status: %w", err)
			}
			return resume()
		default:
			return boundary("IShell", slot)
		}
	}
	if breakpoint >= heapTrapBase+2 && breakpoint < heapTrapBase+heapMethodCount*2+2 {
		slot := (breakpoint - 2 - heapTrapBase) / 2
		switch slot {
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

// RunCallbacks executes one scheduled timer generation. Callbacks rearmed by a
// callback remain queued for the next frame, matching cooperative BREW timing.
func (r *Runtime) RunCallbacks(ctx context.Context) error {
	pending := append([]brewCallback(nil), r.timers...)
	r.timers = r.timers[:0]
	for _, callback := range pending {
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
		result := r.cpu.Run(ctx, pc, mode, bootstrapBudget)
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

func (r *Runtime) openGuestFile() error {
	pathPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW file path pointer: %w", err)
	}
	path, err := r.readCString(pathPointer)
	if err != nil {
		return err
	}
	normalized := strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "fs:/~/")
	contents, ok := r.files["32536/"+normalized]
	if !ok {
		contents, ok = r.files[normalized]
	}
	result := uint32(0)
	if ok {
		r.currentFile = contents
		r.currentPath = normalized
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
	normalized := strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "fs:/~/")
	_, ok := r.files["32536/"+normalized]
	if !ok {
		_, ok = r.files[normalized]
	}
	status := uint32(1)
	if ok {
		status = 0
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, status); err != nil {
		return fmt.Errorf("return BREW file test status: %w", err)
	}
	return nil
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
	values := make([]any, 0, 6)
	count := uint32(0)
	switch format {
	case "%d", "%d.No Record", "%d.map", "%d.rec", "%d.scb", "%d.snr",
		"%d.spk", "%d.sre", "%d.til", "%d.zip", "ui%d.cpp", "ui%d.frm":
		count = 1
	case " %d-%d", "%d[%d]":
		count = 2
	case "%d[%d-%d]", "v %d.%d.%d":
		count = 3
	case "%d.%02d/%02d/%02d %2d:%2d":
		count = 6
	default:
		return fmt.Errorf("BREW execution boundary: unsupported sprintf format %q", format)
	}
	for index := uint32(0); index < count; index++ {
		value, readErr := arg(index)
		if readErr != nil {
			return fmt.Errorf("read BREW sprintf argument %d: %w", index, readErr)
		}
		values = append(values, value)
	}
	text := fmt.Sprintf(format, values...)
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
	case DisplayClassID:
		object = displayObject
	case HeapClassID:
		object = heapObject
	case FileMgrClassID:
		object = fileMgrObject
	case OptionalDeviceClassID:
		object = deviceModelObject
	case SoundClassID:
		object = soundObject
	default:
		returnAddress, readErr := r.cpu.ReadRegister(cpu.RegisterLR)
		if readErr != nil {
			return fmt.Errorf("read BREW shell return address: %w", readErr)
		}
		r.boundary = &ExecutionBoundaryError{
			ClassID:       classID,
			GuestReturnPC: returnAddress &^ 1,
		}
		return r.boundary
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
	if err := r.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
		return fmt.Errorf("return BREW DrawRect status: %w", err)
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
	pixels := make([]byte, framebufferBytes)
	if err := r.cpu.ReadMemory(framebufferBase, pixels); err != nil {
		return nil, false, fmt.Errorf("read BREW guest framebuffer: %w", err)
	}
	frame = image.NewRGBA(image.Rect(0, 0, int(framebufferWidth), int(framebufferHeight)))
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
	size = (size + 7) &^ 7
	if size == 0 || size > heapBase+heapSize-r.heapNext {
		return fmt.Errorf("BREW bootstrap allocation %d exceeds heap", size)
	}
	address := r.heapNext
	r.heapNext += size
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

// EventCount reports successfully completed HandleEvent dispatches by AEEEvent.
func (r *Runtime) EventCount(event uint32) uint64 { return r.eventCounts[event] }

func (r *Runtime) Close() error {
	if r == nil || r.cpu == nil {
		return nil
	}
	return r.cpu.Close()
}
