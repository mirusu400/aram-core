package minigame

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/wipi"
)

const (
	DAT_SHA256     = "955a39b3c09d6228224234dab18b3b38fe89da518c0b614a7cba47e6f9f96900"
	ProfileID      = "wipi-1.2.1/skt/samsung/sch-w830/minigame-qvga-oem"
	BootstrapEvent = uint32(0x1100)
	SetupEvent     = uint32(0x1101)
	StartEvent     = uint32(0x0504)
	FrameEvent     = uint32(0x0505)

	systemBase                = wipi.SystemBase
	systemSize                = wipi.SystemSize
	trampolineBase            = wipi.TrampolineBase
	trampolineSize            = wipi.TrampolineSize
	eadsResolverTrampoline    = uint32(0x01108fc0)
	eadsErrorTrampoline       = uint32(0x01108fc4)
	eadsServiceTableBase      = uint32(0x01008000)
	eadsServiceTrampolineBase = uint32(0x01109000)
	eadsPaletteAddress        = uint32(0x0100f000)
	ImageHeapBase             = uint32(0x20000000)
	ImageHeapSize             = uint32(0x02000000)
	eadsResourceTableOffset   = uint32(0x038c)
	eadsResourceCount         = uint32(0x0191)
	eadsEventInstructionLimit = uint64(2_000_000)
	eadsStackTop              = guest.DefaultStackBase + guest.DefaultStackSize - 0x100
)

var eadsServiceIDs = [...]uint32{
	0x0a0, 0x110, 0x113, 0x102, 0x100, 0x101, 0x15e,
	0x153, 0x164, 0x156, 0x15d, 0x152, 0x161,
}

type eadsServiceCall struct {
	id   uint32
	slot uint32
}

type eadsDecodedImage struct {
	pixels      uint32
	width       int
	height      int
	transparent byte
	source      uint32
}

// EADSEventResult is the deterministic execution accounting for one native
// title event. API calls include resolver and OEM-service trampoline calls.
type EADSEventResult struct {
	Event        uint32
	Instructions uint64
	APICalls     uint64
	ReturnValue  uint32
}

// EADSFrameStats exposes the lifecycle trace used to validate the recovered
// MinigameQVGAOEM runtime without leaking mutable guest state.
type EADSFrameStats struct {
	Events       []EADSEventResult
	PresentCount uint32
	TickMS       uint32
}

// WIPIHost is the slice of the public WIPI runtime the EADS minigame layer
// depends on: trap dispatch and the shared guest heap.
type WIPIHost interface {
	DispatchTrap(ctx context.Context, trap uint32) (bool, error)
	SharedHeap() *guest.Heap
}

type Runtime struct {
	cpu   cpu.Backend
	frame *image.RGBA

	public    WIPIHost
	heap      *guest.Heap
	imageHeap guest.Heap

	serviceAddresses map[uint32]uint32
	serviceByStub    map[uint32]eadsServiceCall
	images           map[uint32]eadsDecodedImage

	dataBase uint32
	bssSize  uint32
	entry    uint32

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

	rngState     uint32
	tickMS       uint32
	presentCount uint32
	enabled      bool
	stage        int
	Stats        EADSFrameStats
}

func NewRuntime(
	backend cpu.Backend,
	frame *image.RGBA,
	public WIPIHost,
	dataBase uint32,
	bssSize uint32,
	entry uint32,
) (*Runtime, error) {
	for _, mapping := range []struct {
		address     uint32
		size        uint32
		permissions cpu.Permissions
		label       string
	}{
		{ImageHeapBase, ImageHeapSize, cpu.PermissionRead | cpu.PermissionWrite, "image heap"},
	} {
		if err := backend.Map(mapping.address, mapping.size, mapping.permissions); err != nil {
			return nil, fmt.Errorf("map EADS %s: %w", mapping.label, err)
		}
	}

	if public == nil {
		return nil, fmt.Errorf("initialize MinigameQVGAOEM runtime: public WIPI runtime is nil")
	}

	runtime := &Runtime{
		cpu:              backend,
		frame:            frame,
		public:           public,
		heap:             public.SharedHeap(),
		imageHeap:        guest.NewHeap(backend, ImageHeapBase, ImageHeapSize),
		serviceAddresses: make(map[uint32]uint32, len(eadsServiceIDs)),
		serviceByStub:    make(map[uint32]eadsServiceCall, len(eadsServiceIDs)*64),
		images:           make(map[uint32]eadsDecodedImage),
		dataBase:         dataBase,
		bssSize:          bssSize,
		entry:            entry,
		width:            frame.Bounds().Dx(),
		height:           frame.Bounds().Dy(),
		clipRight:        frame.Bounds().Dx() - 1,
		clipBottom:       frame.Bounds().Dy() - 1,
		drawColor:        0x00ffffff,
		surfaceWidth:     frame.Bounds().Dx(),
		surfaceHeight:    frame.Bounds().Dy(),
		rngState:         1,
		enabled:          true,
	}
	if err := runtime.setupServiceTables(); err != nil {
		return nil, err
	}
	if err := runtime.setupScreenFramebuffer(); err != nil {
		return nil, err
	}
	if err := runtime.preallocateResourceObjects(); err != nil {
		return nil, err
	}
	runtime.SyncFrame()
	return runtime, nil
}

func (r *Runtime) Reset() error {
	backend := r.cpu
	frame := r.frame
	public := r.public
	dataBase := r.dataBase
	bssSize := r.bssSize
	entry := r.entry
	width := r.width
	height := r.height

	for _, region := range []struct {
		address uint32
		size    uint32
		label   string
	}{
		{ImageHeapBase, ImageHeapSize, "image heap"},
	} {
		if err := guest.ZeroMemory(backend, region.address, region.size); err != nil {
			return fmt.Errorf("reset EADS %s: %w", region.label, err)
		}
	}
	*r = Runtime{
		cpu:              backend,
		frame:            frame,
		public:           public,
		heap:             public.SharedHeap(),
		imageHeap:        guest.NewHeap(backend, ImageHeapBase, ImageHeapSize),
		serviceAddresses: make(map[uint32]uint32, len(eadsServiceIDs)),
		serviceByStub:    make(map[uint32]eadsServiceCall, len(eadsServiceIDs)*64),
		images:           make(map[uint32]eadsDecodedImage),
		dataBase:         dataBase,
		bssSize:          bssSize,
		entry:            entry,
		width:            width,
		height:           height,
		clipRight:        width - 1,
		clipBottom:       height - 1,
		drawColor:        0x00ffffff,
		surfaceWidth:     width,
		surfaceHeight:    height,
		rngState:         1,
		enabled:          true,
	}
	if err := r.setupServiceTables(); err != nil {
		return err
	}
	if err := r.setupScreenFramebuffer(); err != nil {
		return err
	}
	if err := r.preallocateResourceObjects(); err != nil {
		return err
	}
	r.SyncFrame()
	return nil
}

func (r *Runtime) setupServiceTables() error {
	var encoded [4]byte
	for tableIndex, serviceID := range eadsServiceIDs {
		table := eadsServiceTableBase + uint32(tableIndex)*0x100
		r.serviceAddresses[serviceID] = table
		for slot := uint32(0); slot < 0x100; slot += 4 {
			stub := eadsServiceTrampolineBase +
				(uint32(tableIndex)*64+slot/4)*4
			r.serviceByStub[stub] = eadsServiceCall{id: serviceID, slot: slot}
			binary.LittleEndian.PutUint32(encoded[:], stub|1)
			if err := r.cpu.WriteMemory(table+slot, encoded[:]); err != nil {
				return fmt.Errorf("initialize EADS service 0x%03x slot 0x%02x: %w",
					serviceID, slot, err)
			}
		}
	}
	return nil
}

func (r *Runtime) setupScreenFramebuffer() error {
	var err error
	r.screenPixels, err = r.heap.Allocate(uint32(r.width*r.height*4), true)
	if err != nil || r.screenPixels == 0 {
		return fmt.Errorf("allocate EADS framebuffer pixels: %w", err)
	}
	r.screenHandle, err = r.heap.Allocate(24, true)
	if err != nil || r.screenHandle == 0 {
		return fmt.Errorf("allocate EADS framebuffer handle: %w", err)
	}
	values := [...]uint32{
		r.screenPixels,
		uint32(r.width),
		uint32(r.height),
		uint32(r.width * 4),
		32,
		0,
	}
	var descriptor [24]byte
	for index, value := range values {
		binary.LittleEndian.PutUint32(descriptor[index*4:], value)
	}
	if err := r.cpu.WriteMemory(r.screenHandle, descriptor[:]); err != nil {
		return fmt.Errorf("initialize EADS framebuffer handle: %w", err)
	}
	return nil
}

func (r *Runtime) preallocateResourceObjects() error {
	table := r.dataBase + eadsResourceTableOffset
	end := uint64(r.dataBase) + uint64(r.bssSize)
	var encoded [4]byte
	for resourceID := uint32(0); resourceID < eadsResourceCount; resourceID++ {
		descriptor := table + resourceID*20
		if uint64(descriptor)+20 > end {
			break
		}
		object, err := r.imageHeap.Allocate(0x20, true)
		if err != nil {
			return fmt.Errorf("allocate EADS resource %d: %w", resourceID, err)
		}
		if object == 0 {
			return fmt.Errorf("allocate EADS resource %d: image heap exhausted", resourceID)
		}
		binary.LittleEndian.PutUint32(encoded[:], object)
		if err := r.cpu.WriteMemory(descriptor, encoded[:]); err != nil {
			return fmt.Errorf("initialize EADS resource %d: %w", resourceID, err)
		}
	}
	return nil
}

func (r *Runtime) createBootstrap() (uint32, uint32, error) {
	resolver, err := r.heap.Allocate(0x10, true)
	if err != nil {
		return 0, 0, err
	}
	callback, err := r.heap.Allocate(0x10, true)
	if err != nil {
		return 0, 0, err
	}
	if resolver == 0 || callback == 0 {
		return 0, 0, fmt.Errorf("guest heap exhausted")
	}
	if err := r.writeU32(resolver+4, eadsResolverTrampoline|1); err != nil {
		return 0, 0, err
	}
	if err := r.writeU32(resolver+8, eadsErrorTrampoline|1); err != nil {
		return 0, 0, err
	}
	return resolver, callback, nil
}

func (r *Runtime) RenderFirstFrame(ctx context.Context) error {
	if r.stage >= 5 {
		return nil
	}
	if r.stage != 0 {
		return fmt.Errorf("EADS lifecycle is partially initialized at stage %d", r.stage)
	}
	resolver, callback, err := r.createBootstrap()
	if err != nil {
		return fmt.Errorf("create EADS bootstrap: %w", err)
	}
	events := []struct {
		event uint32
		args  [4]uint32
	}{
		{BootstrapEvent, [4]uint32{BootstrapEvent, resolver, callback, 0}},
		{SetupEvent, [4]uint32{SetupEvent, 0, 0, 0}},
		{StartEvent, [4]uint32{StartEvent, 0, 0, 0}},
		{FrameEvent, [4]uint32{FrameEvent, 0, 0, 0}},
		{FrameEvent, [4]uint32{FrameEvent, 0, 0, 0}},
	}
	for index, event := range events {
		result, runErr := r.runEvent(ctx, event.event, event.args)
		if runErr != nil {
			return fmt.Errorf("EADS event 0x%04x at lifecycle stage %d: %w",
				event.event, index, runErr)
		}
		r.Stats.Events = append(r.Stats.Events, result)
		r.stage++
	}
	r.Stats.PresentCount = r.presentCount
	r.Stats.TickMS = r.tickMS
	return nil
}

func (r *Runtime) StepFrame(ctx context.Context) (EADSEventResult, error) {
	if r.stage < 5 {
		if err := r.RenderFirstFrame(ctx); err != nil {
			return EADSEventResult{}, err
		}
	}
	result, err := r.runEvent(ctx, FrameEvent, [4]uint32{FrameEvent})
	if err == nil {
		r.Stats.Events = append(r.Stats.Events, result)
		r.Stats.PresentCount = r.presentCount
		r.Stats.TickMS = r.tickMS
	}
	return result, err
}

func (r *Runtime) runEvent(
	ctx context.Context,
	event uint32,
	args [4]uint32,
) (EADSEventResult, error) {
	for register := cpu.RegisterR0; register <= cpu.RegisterR12; register++ {
		if err := r.cpu.WriteRegister(register, 0); err != nil {
			return EADSEventResult{}, err
		}
	}
	for register, value := range args {
		if err := r.cpu.WriteRegister(uint32(register), value); err != nil {
			return EADSEventResult{}, err
		}
	}
	for _, register := range []struct {
		id    uint32
		value uint32
	}{
		{cpu.RegisterSP, eadsStackTop},
		{cpu.RegisterLR, guest.ReturnSentinel | 1},
		{cpu.RegisterPC, r.entry &^ 1},
		{cpu.RegisterCPSR, cpu.StatusThumb},
	} {
		if err := r.cpu.WriteRegister(register.id, register.value); err != nil {
			return EADSEventResult{}, err
		}
	}

	result := EADSEventResult{Event: event}
	for result.Instructions < eadsEventInstructionLimit {
		pc, err := r.cpu.ReadRegister(cpu.RegisterPC)
		if err != nil {
			return result, err
		}
		cpsr, err := r.cpu.ReadRegister(cpu.RegisterCPSR)
		if err != nil {
			return result, err
		}
		mode := cpu.ModeARM
		if cpsr&cpu.StatusThumb != 0 {
			mode = cpu.ModeThumb
		}
		run := r.cpu.Run(
			ctx,
			pc,
			mode,
			eadsEventInstructionLimit-result.Instructions,
		)
		result.Instructions += run.Instructions
		if run.Err != nil {
			return result, fmt.Errorf("execute at 0x%08x after %d instructions: %w",
				run.PC, result.Instructions, run.Err)
		}
		if run.Reason != cpu.StopBreakpoint {
			return result, fmt.Errorf("unexpected CPU stop %d at 0x%08x after %d instructions",
				run.Reason, run.PC, result.Instructions)
		}
		trap := run.PC - 2
		if trap == guest.ReturnSentinel {
			// Unicorn's end address is exclusive: reaching the sentinel ends
			// an event before its instruction is counted. Keep the portable
			// backend's observable accounting aligned with that oracle.
			result.Instructions--
			value, readErr := r.cpu.ReadRegister(cpu.RegisterR0)
			if readErr != nil {
				return result, readErr
			}
			if writeErr := r.cpu.WriteRegister(cpu.RegisterPC, guest.ReturnSentinel); writeErr != nil {
				return result, writeErr
			}
			result.ReturnValue = value
			if err := r.ensureResourceObjects(); err != nil {
				return result, err
			}
			return result, nil
		}
		result.APICalls++
		switch trap {
		case eadsResolverTrampoline:
			serviceID, readErr := r.arg(0)
			if readErr != nil {
				return result, readErr
			}
			if err := r.returnFromTrap(r.serviceAddresses[serviceID]); err != nil {
				return result, err
			}
		case eadsErrorTrampoline:
			if err := r.returnFromTrap(0); err != nil {
				return result, err
			}
		default:
			call, ok := r.serviceByStub[trap]
			if !ok {
				handled, publicErr := r.public.DispatchTrap(ctx, trap)
				if publicErr != nil {
					return result, publicErr
				}
				if handled {
					continue
				}
				return result, fmt.Errorf("unknown host trampoline 0x%08x", trap)
			}
			value, dispatchErr := r.dispatch(call)
			if dispatchErr != nil {
				return result, fmt.Errorf("service 0x%03x slot 0x%02x: %w",
					call.id, call.slot, dispatchErr)
			}
			if err := r.returnFromTrap(value); err != nil {
				return result, err
			}
		}
	}
	return result, fmt.Errorf("instruction limit %d reached", eadsEventInstructionLimit)
}

func (r *Runtime) returnFromTrap(value uint32) error {
	if err := r.cpu.WriteRegister(cpu.RegisterR0, value); err != nil {
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

func (r *Runtime) arg(index int) (uint32, error) {
	if index < 4 {
		return r.cpu.ReadRegister(uint32(index))
	}
	sp, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return 0, err
	}
	return r.readU32(sp + uint32(index-4)*4)
}

func (r *Runtime) args(count int) ([]uint32, error) {
	values := make([]uint32, count)
	for index := range values {
		value, err := r.arg(index)
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	return values, nil
}

func (r *Runtime) dispatch(call eadsServiceCall) (uint32, error) {
	args, err := r.args(8)
	if err != nil {
		return 0, err
	}
	switch {
	case call.id == 0x153 && call.slot == 0x08:
		requested := int32(args[1])
		if requested <= 0 || uint32(requested) > guest.HeapSize {
			return 0, nil
		}
		return r.heap.Allocate(uint32(requested), false)
	case call.id == 0x153 && call.slot == 0x0c:
		r.heap.Release(args[1])
		return 0, nil
	case call.id == 0x153 && call.slot == 0x48:
		r.rngState = args[1] & 0x7fffffff
		return 0, nil
	case call.id == 0x153 && call.slot == 0x4c:
		lower, upper := int32(args[1]), int32(args[2])
		if lower > upper {
			lower, upper = upper, lower
		}
		r.rngState = (1103515245*r.rngState + 12345) & 0x7fffffff
		span := int64(upper) - int64(lower) + 1
		if span < 1 {
			span = 1
		}
		return uint32(int64(lower) + int64(r.rngState)%span), nil
	case call.id == 0x0a0 && call.slot == 0x0c:
		if r.palette == 0 {
			r.palette = eadsPaletteAddress
			palette := make([]byte, 256*4)
			for index := 0; index < 256; index++ {
				rgb := rgb332(byte(index))
				value := rgb888To565(byte(rgb>>16), byte(rgb>>8), byte(rgb))
				binary.LittleEndian.PutUint32(palette[index*4:], uint32(value))
			}
			if err := r.cpu.WriteMemory(r.palette, palette); err != nil {
				return 0, err
			}
		}
		return r.palette, nil
	case call.id == 0x0a0 && call.slot == 0x2c:
		r.surfaceFormat = args[1]
		return 0, nil
	case call.id == 0x0a0 && call.slot == 0x30:
		r.surfacePalette = args[1]
		return 0, nil
	case call.id == 0x0a0 && call.slot == 0x34:
		r.surfaceWidth = max(0, int(int32(args[1])))
		return 0, nil
	case call.id == 0x0a0 && call.slot == 0x38:
		r.surfaceHeight = max(0, int(int32(args[1])))
		return 0, nil
	case call.id == 0x0a0 && call.slot == 0x3c:
		r.surfacePixels = args[1]
		return 0, nil
	case call.id == 0x0a0 && call.slot == 0x48:
		x, y := int(int32(args[1])), int(int32(args[2]))
		width, height := max(0, int(int32(args[3]))), max(0, int(int32(args[4])))
		r.clipLeft = guest.Clamp(x, 0, r.width-1)
		r.clipTop = guest.Clamp(y, 0, r.height-1)
		r.clipRight = guest.Clamp(x+width-1, 0, r.width-1)
		r.clipBottom = guest.Clamp(y+height-1, 0, r.height-1)
		return 0, nil
	case call.id == 0x0a0 && call.slot == 0x68:
		r.surfaceWork = args[1]
		return 0, nil
	case call.id == 0x0a0 && call.slot == 0x74:
		r.presentCount++
		r.tickMS += 16
		return 0, nil
	case call.id == 0x0a0 && call.slot == 0x78:
		x, y := int(int32(args[1])), int(int32(args[2]))
		var pixel uint32
		if x >= 0 && x < r.width && y >= 0 && y < r.height {
			pixel, err = r.readU32(r.screenPixels + uint32((y*r.width+x)*4))
			if err != nil {
				return 0, err
			}
		}
		if args[3] != 0 {
			if err := r.cpu.WriteMemory(args[3], []byte{
				byte(pixel >> 16), byte(pixel >> 8), byte(pixel),
			}); err != nil {
				return 0, err
			}
		}
		return pixel, nil
	case call.id == 0x0a0 && call.slot == 0x94:
		red, green, blue := rgb565To888(uint16(args[1]))
		for index, value := range []byte{red, green, blue} {
			if args[index+2] != 0 {
				if err := r.cpu.WriteMemory(args[index+2], []byte{value}); err != nil {
					return 0, err
				}
			}
		}
		return 0, nil
	case call.id == 0x0a0 && call.slot == 0x98:
		return uint32(rgb888To565(byte(args[1]), byte(args[2]), byte(args[3]))), nil
	case call.id == 0x113 && call.slot == 0x08:
		return r.decodeImage(args)
	case call.id == 0x113 && call.slot == 0x18:
		pixels := args[2]
		for target, decoded := range r.images {
			if decoded.pixels == pixels {
				delete(r.images, target)
			}
		}
		if pixels != 0 {
			r.imageHeap.Release(pixels)
		}
		return 0, nil
	case call.id == 0x113 && call.slot == 0x24:
		drawn, err := r.blitRGB565(
			int(int32(args[1])), int(int32(args[2])),
			int(int32(args[3])), int(int32(args[4])),
			args[5], uint16(args[6]),
		)
		if drawn {
			return 1, err
		}
		return 0, err
	case call.id == 0x113 && call.slot == 0x28:
		decoded, ok := r.images[args[3]]
		if !ok {
			return 0, nil
		}
		drawn, err := r.blitIndices(int(int32(args[1])), int(int32(args[2])), decoded)
		if drawn {
			return 1, err
		}
		return 0, err
	case call.id == 0x110 && call.slot == 0x10:
		red, green, blue := rgb565To888(uint16(args[1]))
		r.drawColor = uint32(red)<<16 | uint32(green)<<8 | uint32(blue)
		return 0, nil
	case call.id == 0x110 && call.slot == 0x18:
		r.drawStyle = args[1]
		return 0, nil
	case call.id == 0x110 && call.slot == 0x24:
		red, green, blue := rgb565To888(uint16(args[4]))
		pixel := uint32(red)<<16 | uint32(green)<<8 | uint32(blue)
		x1, y, x2 := int(int32(args[1])), int(int32(args[2])), int(int32(args[3]))
		if y >= r.clipTop && y <= r.clipBottom {
			for x := max(x1, r.clipLeft); x <= min(x2, r.clipRight); x++ {
				if err := r.putPixel(x, y, pixel); err != nil {
					return 0, err
				}
			}
		}
		return 0, nil
	case call.id == 0x110 && call.slot == 0x30:
		return 0, r.drawRectangle(args)
	case call.id == 0x164 && call.slot == 0x10:
		return r.tickMS, nil
	case call.id == 0x164 && call.slot == 0x0c:
		return 0, nil
	case call.id == 0x15d && call.slot == 0x18:
		r.enabled = args[1] != 0
		return 0, nil
	case call.id == 0x15e && call.slot == 0x14:
		text, err := r.readCString(args[1], 1<<20)
		if err != nil {
			return 0, err
		}
		// The lifecycle path only queries ASCII labels before the first game
		// frame. This is the deterministic fallback metric used by the oracle.
		return uint32(len(text) * 6), nil
	case call.id == 0x15e && call.slot == 0x0c:
		copy(r.textSurface[:], args[1:6])
		return 0, nil
	case call.id == 0x15e && (call.slot == 0x08 || call.slot == 0x30):
		return 0, nil
	case call.id == 0x161 && (call.slot == 0x10 || call.slot == 0x14):
		return 0, nil
	default:
		// Constructor/destructor and still-opaque device service slots observed
		// during lifecycle setup use zero as their ordinary success value.
		return 0, nil
	}
}

func (r *Runtime) decodeImage(args []uint32) (uint32, error) {
	target := args[1]
	width, height := int(int32(args[2])), int(int32(args[3]))
	decoded, ok, err := r.decodeMQ(args[4], width, height, byte(args[5]))
	if err != nil {
		return ^uint32(0), err
	}
	if target == 0 || !ok || width <= 0 || height <= 0 {
		return ^uint32(0), nil
	}
	if previous, exists := r.images[target]; exists {
		r.imageHeap.Release(previous.pixels)
		delete(r.images, target)
	}
	pixels, err := r.imageHeap.Allocate(uint32(len(decoded)), false)
	if err != nil {
		return ^uint32(0), err
	}
	if pixels == 0 {
		return ^uint32(0), nil
	}
	if err := r.cpu.WriteMemory(pixels, decoded); err != nil {
		return ^uint32(0), err
	}
	if err := r.writeU32(target+0x0c, pixels); err != nil {
		return ^uint32(0), err
	}
	r.images[target] = eadsDecodedImage{
		pixels:      pixels,
		width:       width,
		height:      height,
		transparent: byte(args[5]),
		source:      args[4],
	}
	return 0, nil
}

func (r *Runtime) decodeMQ(
	source uint32,
	width int,
	height int,
	transparent byte,
) ([]byte, bool, error) {
	if source == 0 || width <= 0 || height <= 0 ||
		uint64(width)*uint64(height) > 16<<20 {
		return nil, false, nil
	}
	var header [6]byte
	if err := r.cpu.ReadMemory(source, header[:]); err != nil {
		return nil, false, err
	}
	expected := width * height
	outputCount := int(binary.LittleEndian.Uint16(header[2:]))
	recordSize := int(binary.LittleEndian.Uint16(header[4:]))
	if string(header[:2]) != "MQ" || outputCount != expected ||
		recordSize < 6 || recordSize > 0x10000 {
		return nil, false, nil
	}
	packed := make([]byte, recordSize-6)
	if err := r.cpu.ReadMemory(source+6, packed); err != nil {
		return nil, false, err
	}
	ring := make([]byte, 4096)
	for index := range ring {
		ring[index] = transparent
	}
	output := make([]byte, 0, outputCount)
	ringPosition, cursor := 0, 0
	for len(output) < outputCount && cursor < len(packed) {
		flags := packed[cursor]
		cursor++
		for bit := 7; bit >= 0 && len(output) < outputCount; bit-- {
			if flags&(1<<bit) != 0 {
				if cursor >= len(packed) {
					return nil, false, nil
				}
				value := packed[cursor]
				cursor++
				output = append(output, value)
				ring[ringPosition] = value
				ringPosition = (ringPosition + 1) & 0xfff
				continue
			}
			if cursor+1 >= len(packed) {
				return nil, false, nil
			}
			first, second := packed[cursor], packed[cursor+1]
			cursor += 2
			offset := int(first)<<4 | int(second>>4)
			length := int(second&0x0f) + 3
			for index := 0; index < length && len(output) < outputCount; index++ {
				value := ring[(offset+index)&0xfff]
				output = append(output, value)
				ring[ringPosition] = value
				ringPosition = (ringPosition + 1) & 0xfff
			}
		}
	}
	return output, len(output) == outputCount, nil
}

func (r *Runtime) blitIndices(
	x int,
	y int,
	decoded eadsDecodedImage,
) (bool, error) {
	if decoded.pixels == 0 {
		return false, nil
	}
	indices := make([]byte, decoded.width*decoded.height)
	if err := r.cpu.ReadMemory(decoded.pixels, indices); err != nil {
		return false, err
	}
	for sourceY := 0; sourceY < decoded.height; sourceY++ {
		targetY := y + sourceY
		if targetY < 0 || targetY >= r.height ||
			targetY < r.clipTop || targetY > r.clipBottom {
			continue
		}
		for sourceX := 0; sourceX < decoded.width; sourceX++ {
			targetX := x + sourceX
			if targetX < 0 || targetX >= r.width ||
				targetX < r.clipLeft || targetX > r.clipRight {
				continue
			}
			index := indices[sourceY*decoded.width+sourceX]
			if index == decoded.transparent {
				continue
			}
			if err := r.putPixel(targetX, targetY, rgb332(index)); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

func (r *Runtime) blitRGB565(
	x int,
	y int,
	width int,
	height int,
	pixels uint32,
	transparent uint16,
) (bool, error) {
	if pixels == 0 || width <= 0 || height <= 0 ||
		uint64(width)*uint64(height) > 16<<20 {
		return false, nil
	}
	source := make([]byte, width*height*2)
	if err := r.cpu.ReadMemory(pixels, source); err != nil {
		return false, err
	}
	for sourceY := 0; sourceY < height; sourceY++ {
		targetY := y + sourceY
		if targetY < 0 || targetY >= r.height ||
			targetY < r.clipTop || targetY > r.clipBottom {
			continue
		}
		for sourceX := 0; sourceX < width; sourceX++ {
			targetX := x + sourceX
			if targetX < 0 || targetX >= r.width ||
				targetX < r.clipLeft || targetX > r.clipRight {
				continue
			}
			value := binary.LittleEndian.Uint16(source[(sourceY*width+sourceX)*2:])
			if value == transparent {
				continue
			}
			red, green, blue := rgb565To888(value)
			pixel := uint32(red)<<16 | uint32(green)<<8 | uint32(blue)
			if err := r.putPixel(targetX, targetY, pixel); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

func (r *Runtime) drawRectangle(args []uint32) error {
	x, y := int(int32(args[1])), int(int32(args[2]))
	width, height := max(0, int(int32(args[3]))), max(0, int(int32(args[4])))
	outline := args[5] != 0
	if width <= 0 {
		return nil
	}
	for row := 0; row < height; row++ {
		targetY := y + row
		if targetY < r.clipTop || targetY > r.clipBottom {
			continue
		}
		if outline && row != 0 && row != height-1 {
			for _, column := range []int{0, width - 1} {
				targetX := x + column
				if targetX >= r.clipLeft && targetX <= r.clipRight {
					if err := r.putPixel(targetX, targetY, r.drawColor); err != nil {
						return err
					}
				}
			}
			continue
		}
		for column := 0; column < width; column++ {
			targetX := x + column
			if targetX >= r.clipLeft && targetX <= r.clipRight {
				if err := r.putPixel(targetX, targetY, r.drawColor); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r *Runtime) putPixel(x, y int, pixel uint32) error {
	if x < 0 || y < 0 || x >= r.width || y >= r.height {
		return nil
	}
	return r.writeU32(r.screenPixels+uint32((y*r.width+x)*4), pixel)
}

func (r *Runtime) ensureResourceObjects() error {
	table := r.dataBase + eadsResourceTableOffset
	end := uint64(r.dataBase) + uint64(r.bssSize)
	for resourceID := uint32(0); resourceID < eadsResourceCount; resourceID++ {
		descriptor := table + resourceID*20
		if uint64(descriptor)+20 > end {
			break
		}
		object, err := r.readU32(descriptor)
		if err != nil || object != 0 {
			if err != nil {
				return err
			}
			continue
		}
		var metadata [13]byte
		if err := r.cpu.ReadMemory(descriptor+4, metadata[:]); err != nil {
			return err
		}
		width := binary.LittleEndian.Uint16(metadata[0:])
		height := binary.LittleEndian.Uint16(metadata[2:])
		source := binary.LittleEndian.Uint32(metadata[4:])
		kind := metadata[12]
		if width == 0 || height == 0 || width > 4096 || height > 4096 ||
			kind != 1 {
			continue
		}
		var magic [2]byte
		if err := r.cpu.ReadMemory(source, magic[:]); err != nil || string(magic[:]) != "MQ" {
			continue
		}
		object, err = r.imageHeap.Allocate(0x20, true)
		if err != nil || object == 0 {
			return err
		}
		if err := r.writeU32(descriptor, object); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) SyncFrame() {
	raw := make([]byte, r.width*r.height*4)
	if err := r.cpu.ReadMemory(r.screenPixels, raw); err != nil {
		return
	}
	for y := 0; y < r.height; y++ {
		for x := 0; x < r.width; x++ {
			pixel := binary.LittleEndian.Uint32(raw[(y*r.width+x)*4:])
			r.frame.SetRGBA(x, y, color.RGBA{
				R: byte(pixel >> 16),
				G: byte(pixel >> 8),
				B: byte(pixel),
				A: 0xff,
			})
		}
	}
}

func (r *Runtime) readU32(address uint32) (uint32, error) {
	return guest.ReadU32(r.cpu, address)
}

func (r *Runtime) writeU32(address, value uint32) error {
	return guest.WriteU32(r.cpu, address, value)
}

func (r *Runtime) readCString(address uint32, limit int) ([]byte, error) {
	if address == 0 {
		return nil, nil
	}
	result := make([]byte, 0, min(limit, 256))
	var buffer [256]byte
	for len(result) < limit {
		count := min(len(buffer), limit-len(result))
		if err := r.cpu.ReadMemory(address+uint32(len(result)), buffer[:count]); err != nil {
			return nil, err
		}
		for index, value := range buffer[:count] {
			if value == 0 {
				return append(result, buffer[:index]...), nil
			}
		}
		result = append(result, buffer[:count]...)
	}
	return nil, fmt.Errorf("unterminated string at 0x%08x", address)
}

func rgb332(index byte) uint32 {
	red := uint32(index>>5) * 255 / 7
	green := uint32((index>>2)&7) * 255 / 7
	blue := uint32(index&3) * 255 / 3
	return red<<16 | green<<8 | blue
}

func rgb565To888(value uint16) (byte, byte, byte) {
	red := byte(uint32(value>>11&0x1f) * 255 / 31)
	green := byte(uint32(value>>5&0x3f) * 255 / 63)
	blue := byte(uint32(value&0x1f) * 255 / 31)
	return red, green, blue
}

func rgb888To565(red, green, blue byte) uint16 {
	red5 := (uint32(red)*31 + 127) / 255
	green6 := (uint32(green)*63 + 127) / 255
	blue5 := (uint32(blue)*31 + 127) / 255
	return uint16(red5<<11 | green6<<5 | blue5)
}
