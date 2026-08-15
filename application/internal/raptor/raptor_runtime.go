package raptor

import (
	"context"
	"encoding/binary"
	"fmt"

	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	raptorloader "github.com/mirusu400/aram-core/loader/raptor"
	"github.com/mirusu400/aram-core/wipi"
)

const (
	ProfileID = "wipi-1.2.1/lgt/raptor"

	KernelBase = uint32(0x01008000)
	DletBase   = uint32(0x01008400)
	WIPICBase  = uint32(0x01008800)

	VolumeTable       = uint32(0x01008c00)
	raptorLocalVolume = uint32(0x01008c08)
	raptorShareVolume = uint32(0x01008c0b)

	raptorImportStubBase  = uint32(0x0110a000)
	raptorImportStubSize  = uint32(0x00004000)
	raptorDletModuleStub  = uint32(0x0110e000)
	raptorDletResolveStub = uint32(0x0110e004)
	raptorDletWaitStub    = uint32(0x0110e008)

	raptorCletHeaderSize = uint32(0x30)
	DependencyDataSlot   = uint32(0x214)

	// The LGT Raptor lifecycle ABI translates HAL key events to these
	// CletHandleEvent type values. The reference Clet compares them directly
	// before dispatching its press and release handlers.
	raptorKeyPressEvent   = uint32(502)
	raptorKeyReleaseEvent = uint32(503)

	StartupFrameLimit = 64
)

type Clet struct {
	Table       uint32
	Name        string
	Initialize  uint32
	Start       uint32
	Destroy     uint32
	Pause       uint32
	Resume      uint32
	Paint       uint32
	HandleEvent uint32
}

type CallbackTask struct {
	Callback wipirt.GuestCallback
	Context  []byte
}

type Runtime struct {
	CPU    cpu.Backend
	Public *wipirt.Runtime
	Pkg    raptorloader.Package
	Clet   Clet
	Java   *JavaRuntime

	CallbackTasks []*CallbackTask

	ModuleInitialized bool
	Started           bool
	resolvedImports   map[raptorImportKey]uint64
	importSlots       []raptorImportKey
	importSlotByKey   map[raptorImportKey]uint32
	ImportTrace       []raptorImportCall
}

type raptorImportKey struct {
	Module  uint32
	Ordinal uint32
}

type raptorImportCall struct {
	Module  uint32
	Ordinal uint32
	Args    [4]uint32
	LR      uint32
}

func InputCallback(
	procedure uint32,
	event machinecore.InputEvent,
) (wipirt.GuestCallback, bool) {
	key, ok := guest.InputKeyCode(event.Control)
	if !ok || procedure == 0 {
		return wipirt.GuestCallback{}, false
	}
	eventType := raptorKeyReleaseEvent
	if event.Pressed {
		eventType = raptorKeyPressEvent
	}
	return wipirt.GuestCallback{
		Procedure: procedure,
		Args: [4]uint32{
			eventType,
			uint32(int32(key)),
			0,
		},
	}, true
}

func NewRuntime(
	backend cpu.Backend,
	public *wipirt.Runtime,
	pkg raptorloader.Package,
) (*Runtime, error) {
	clet, err := inspectRaptorClet(pkg.Image)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		CPU:             backend,
		Public:          public,
		Pkg:             pkg,
		Clet:            clet,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	if err := runtime.InstallInterfaces(); err != nil {
		return nil, err
	}
	return runtime, nil
}

func inspectRaptorClet(image raptorloader.Image) (Clet, error) {
	data, ok := image.DataSection()
	if !ok || len(data.Data) < int(raptorCletHeaderSize) {
		return Clet{}, fmt.Errorf(
			"initialize Raptor Clet: read-write lifecycle header is missing",
		)
	}
	text, ok := image.CodeSection()
	if !ok {
		return Clet{}, fmt.Errorf("initialize Raptor Clet: code section is missing")
	}
	// Most modules put the lifecycle header at the start of the read-write
	// region, but some place it further in, so find it by its shape rather
	// than by a fixed offset: version 3, the code base, a readable name, and
	// six Thumb entry points. The base may carry the interworking bit, the
	// same way entry offsets do.
	limit := len(data.Data) - int(raptorCletHeaderSize)
	var module Clet
	for offset := 0; offset <= limit; offset += 4 {
		if clet, ok := raptorCletAt(image, data, text, offset); ok {
			if clet.Start != 0 {
				return clet, nil
			}
			if module.Table == 0 {
				module = clet
			}
		}
	}
	if module.Table != 0 {
		return module, nil
	}
	return Clet{}, fmt.Errorf(
		"initialize Raptor Clet: %s holds no version 3 lifecycle header",
		data.Name,
	)
}

func raptorCletAt(
	image raptorloader.Image,
	data raptorloader.Section,
	text raptorloader.Section,
	offset int,
) (Clet, bool) {
	header := data.Data[offset:]
	initialize := binary.LittleEndian.Uint32(header[4:8])
	if binary.LittleEndian.Uint32(header[0:4]) != 3 ||
		!raptorExecutableAddress(image, initialize&^1) ||
		binary.LittleEndian.Uint32(header[0x0c:0x10]) != 0 ||
		binary.LittleEndian.Uint32(header[0x10:0x14]) != 0 ||
		binary.LittleEndian.Uint32(header[0x14:0x18]) != 0 {
		return Clet{}, false
	}
	name, ok := raptorImageCString(
		image,
		binary.LittleEndian.Uint32(header[8:12]),
		255,
	)
	if !ok || name == "" {
		return Clet{}, false
	}
	clet := Clet{
		Table:       data.Address + uint32(offset),
		Name:        name,
		Initialize:  initialize,
		Start:       binary.LittleEndian.Uint32(header[0x18:0x1c]),
		Destroy:     binary.LittleEndian.Uint32(header[0x1c:0x20]),
		Pause:       binary.LittleEndian.Uint32(header[0x20:0x24]),
		Resume:      binary.LittleEndian.Uint32(header[0x24:0x28]),
		Paint:       binary.LittleEndian.Uint32(header[0x28:0x2c]),
		HandleEvent: binary.LittleEndian.Uint32(header[0x2c:0x30]),
	}
	for _, address := range []uint32{
		clet.Start,
		clet.Destroy,
		clet.Pause,
		clet.Resume,
		clet.Paint,
		clet.HandleEvent,
	} {
		if address&1 == 0 || !raptorExecutableAddress(image, address&^1) {
			clet.Start = 0
			clet.Destroy = 0
			clet.Pause = 0
			clet.Resume = 0
			clet.Paint = 0
			clet.HandleEvent = 0
			return clet, true
		}
	}
	return clet, true
}

func raptorImageCString(
	image raptorloader.Image,
	address uint32,
	limit int,
) (string, bool) {
	for _, section := range image.Sections {
		if section.ZeroFill() || address < section.Address {
			continue
		}
		offset := uint64(address) - uint64(section.Address)
		if offset >= uint64(len(section.Data)) {
			continue
		}
		available := section.Data[offset:]
		if len(available) > limit+1 {
			available = available[:limit+1]
		}
		end := 0
		for end < len(available) && available[end] != 0 {
			end++
		}
		if end == len(available) {
			return "", false
		}
		return string(available[:end]), true
	}
	return "", false
}

func raptorExecutableAddress(image raptorloader.Image, address uint32) bool {
	for _, section := range image.Sections {
		if !section.Allocated() || !section.Executable() {
			continue
		}
		if address >= section.Address &&
			uint64(address) < uint64(section.Address)+uint64(section.Size) {
			return true
		}
	}
	return false
}

func raptorSectionExecutable(section raptorloader.Section) bool {
	if section.Executable() {
		return true
	}
	// ARM RVCT Raptor modules place their statically linked import veneers in
	// ER_RW without setting SHF_EXECINSTR. Each 16-byte veneer starts by saving
	// LR and branching to the common import dispatcher. The handset executes
	// these records directly, so grant execute permission to the containing
	// section when that unmistakable prologue is present.
	return len(section.Data) >= 8 &&
		binary.LittleEndian.Uint32(section.Data[0:4]) == 0xe52de004 &&
		binary.LittleEndian.Uint32(section.Data[4:8])&0xff000000 == 0xeb000000
}

func MapRaptorImage(backend cpu.Backend, image raptorloader.Image) error {
	for _, section := range image.AllocatedSections() {
		permissions := cpu.PermissionRead | cpu.PermissionWrite
		if raptorSectionExecutable(section) {
			permissions |= cpu.PermissionExecute
		}
		if err := backend.Map(section.Address, section.Size, permissions); err != nil {
			return fmt.Errorf(
				"map Raptor section %q at 0x%08x: %w",
				section.Name,
				section.Address,
				err,
			)
		}
		if len(section.Data) != 0 {
			if err := backend.WriteMemory(section.Address, section.Data); err != nil {
				return fmt.Errorf(
					"copy Raptor section %q at 0x%08x: %w",
					section.Name,
					section.Address,
					err,
				)
			}
		}
	}
	return nil
}

func (r *Runtime) InstallInterfaces() error {
	dlet := make([]byte, 12)
	binary.LittleEndian.PutUint32(dlet[0:4], raptorDletModuleStub|1)
	binary.LittleEndian.PutUint32(dlet[4:8], raptorDletResolveStub|1)
	binary.LittleEndian.PutUint32(dlet[8:12], raptorDletWaitStub|1)
	if err := r.CPU.WriteMemory(DletBase, dlet); err != nil {
		return fmt.Errorf("install Raptor dlet interface: %w", err)
	}
	volumes := make([]byte, 14)
	binary.LittleEndian.PutUint32(volumes[0:4], raptorLocalVolume)
	binary.LittleEndian.PutUint32(volumes[4:8], raptorShareVolume)
	copy(volumes[8:11], "/L\x00")
	copy(volumes[11:14], "/S\x00")
	if err := r.CPU.WriteMemory(VolumeTable, volumes); err != nil {
		return fmt.Errorf("install Raptor volume interface: %w", err)
	}
	return nil
}

func (r *Runtime) RestoreImage() error {
	if err := r.DestroyRaptorJava(); err != nil {
		return fmt.Errorf("destroy Raptor Java adapter: %w", err)
	}
	for _, section := range r.Pkg.Image.AllocatedSections() {
		if err := guest.ZeroMemory(r.CPU, section.Address, section.Size); err != nil {
			return fmt.Errorf("clear Raptor section %q: %w", section.Name, err)
		}
		if len(section.Data) != 0 {
			if err := r.CPU.WriteMemory(section.Address, section.Data); err != nil {
				return fmt.Errorf("restore Raptor section %q: %w", section.Name, err)
			}
		}
	}
	r.ModuleInitialized = false
	r.Started = false
	r.resolvedImports = make(map[raptorImportKey]uint64)
	r.importSlots = nil
	r.importSlotByKey = make(map[raptorImportKey]uint32)
	r.ImportTrace = nil
	r.CallbackTasks = nil
	return nil
}

func (r *Runtime) DispatchTrap(
	ctx context.Context,
	trap uint32,
) (bool, error) {
	switch trap {
	case raptorDletModuleStub:
		module, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return true, err
		}
		if module == 0 {
			module = 1
		}
		return true, r.Public.ReturnFromTrap(guest.WIPIReturn{Low: module})

	case raptorDletResolveStub:
		module, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return true, err
		}
		ordinal, err := r.CPU.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		key := raptorImportKey{Module: module, Ordinal: ordinal}
		stub, err := r.importStub(key)
		if err != nil {
			return true, err
		}
		r.resolvedImports[key]++
		return true, r.Public.ReturnFromTrap(guest.WIPIReturn{Low: stub | 1})

	case raptorDletWaitStub:
		return true, r.Public.ReturnFromTrap(guest.WIPIReturn{})
	}
	if trap < raptorImportStubBase ||
		trap >= raptorImportStubBase+raptorImportStubSize ||
		(trap-raptorImportStubBase)%4 != 0 {
		return false, nil
	}
	slot := (trap - raptorImportStubBase) / 4
	if slot >= uint32(len(r.importSlots)) {
		return true, fmt.Errorf("Raptor import trampoline slot %d is unresolved", slot)
	}
	return true, r.dispatchImport(ctx, r.importSlots[slot])
}

func (r *Runtime) dispatchImport(
	ctx context.Context,
	key raptorImportKey,
) error {
	call := raptorImportCall{Module: key.Module, Ordinal: key.Ordinal}
	for register := cpu.RegisterR0; register <= cpu.RegisterR3; register++ {
		value, err := r.CPU.ReadRegister(register)
		if err != nil {
			return err
		}
		call.Args[register] = value
	}
	lr, err := r.CPU.ReadRegister(cpu.RegisterLR)
	if err != nil {
		return err
	}
	call.LR = lr
	if len(r.ImportTrace) < wipirt.MaxSavedEntries {
		r.ImportTrace = append(r.ImportTrace, call)
	} else {
		keep := len(r.ImportTrace) / 2
		copy(r.ImportTrace, r.ImportTrace[len(r.ImportTrace)-keep:])
		r.ImportTrace = append(r.ImportTrace[:keep], call)
	}
	if result, name, handled, javaErr := r.dispatchJavaImport(ctx, key); handled {
		if javaErr != nil {
			return fmt.Errorf("%s: %w", name, javaErr)
		}
		r.Public.Stats.APICalls++
		r.Public.Stats.ImplementedCalls++
		r.Public.Stats.LastAPI = name
		r.Public.Observed[name]++
		return r.Public.ReturnFromTrap(result)
	}
	if key.Module == 507 {
		if result, name, handled, privateErr := r.DispatchPrivateImport(key.Ordinal); handled {
			if privateErr != nil {
				return fmt.Errorf("%s: %w", name, privateErr)
			}
			r.Public.Stats.APICalls++
			r.Public.Stats.ImplementedCalls++
			r.Public.Stats.LastAPI = name
			r.Public.Observed[name]++
			return r.Public.ReturnFromTrap(result)
		}
	}
	if publicName, ok := raptorWIPIImportName(key.Ordinal); ok &&
		(key.Module == 1 || key.Module == 507) {
		api, found := wipi.Lookup(publicName)
		if !found {
			return fmt.Errorf(
				"Raptor import %d names unknown public WIPI API %q",
				key.Ordinal,
				publicName,
			)
		}
		return r.Public.DispatchAPI(ctx, api)
	}
	name := fmt.Sprintf("RAPTOR.module%d#%d", key.Module, key.Ordinal)
	r.Public.Stats.APICalls++
	r.Public.Stats.UnimplementedCalls++
	r.Public.Stats.LastAPI = name
	r.Public.Stats.LastUnimplemented = name
	r.Public.Observed[name]++
	r.Public.Unimplemented[name]++
	return r.Public.ReturnFromTrap(guest.WIPIReturn{})
}

func (r *Runtime) DispatchPrivateImport(
	ordinal uint32,
) (guest.WIPIReturn, string, bool, error) {
	switch ordinal {
	case 1200:
		// MC_sndCreate(typeName, capacity, completionCallback) → handle.
		// 제노니아1 creates one clip per sound and shares a single handle
		// stored in its sound object; the completion callback (arg 2) lets it
		// reload a track after an effect finishes.
		typeAddr, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		capacity, err := r.CPU.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		callback, err := r.CPU.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		mediaType := ""
		if typeAddr != 0 {
			raw, readErr := r.Public.ReadCString(typeAddr)
			if readErr != nil {
				return guest.WIPIReturn{}, "", true, readErr
			}
			mediaType = string(raw)
		}
		handle, createErr := r.Public.RaptorCreateClip(mediaType, int32(capacity), callback)
		if createErr != nil {
			return guest.WIPIReturn{}, "", true, createErr
		}
		return guest.WIPIReturn{Low: handle}, "RAPTOR.sndCreate", true, nil
	case 1203:
		// MC_sndPutData(handle, source, length) → positive on success.
		handle, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		source, err := r.CPU.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		length, err := r.CPU.ReadRegister(cpu.RegisterR2)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		if r.Public.RaptorPutClipData(handle, source, int32(length)) {
			return guest.WIPIReturn{Low: length}, "RAPTOR.sndPutData", true, nil
		}
		return guest.WIPIReturn{}, "RAPTOR.sndPutData", true, nil
	case 1221:
		// MC_sndRewind(handle, 0) → 0 on success.
		handle, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		r.Public.RaptorRewindClip(handle)
		return guest.WIPIReturn{}, "RAPTOR.sndRewind", true, nil
	case 1209:
		// MC_sndSetVolume(handle, volume).
		handle, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		volume, err := r.CPU.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		r.Public.RaptorSetClipVolume(handle, int32(volume))
		return guest.WIPIReturn{}, "RAPTOR.sndSetVolume", true, nil
	case 1210:
		// MC_sndPlay(handle, loopFlag) → 0 on success.
		handle, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		loop, err := r.CPU.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		if !r.Public.RaptorPlayClip(handle, loop&1 != 0) {
			return guest.WIPIReturn{Low: ^uint32(0)}, "RAPTOR.sndPlay", true, nil
		}
		return guest.WIPIReturn{}, "RAPTOR.sndPlay", true, nil
	case 50, 51, 52, 53, 54:
		handle, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		if handle == 0 {
			handle, err = r.Public.EnsureScreenFramebuffer()
			if err != nil {
				return guest.WIPIReturn{}, "", true, err
			}
		}
		framebuffer := r.Public.Framebuffers[handle]
		if framebuffer.Handle == 0 {
			for _, candidate := range r.Public.Framebuffers {
				if candidate.Pixels == handle {
					framebuffer = candidate
					break
				}
			}
		}
		// LGT's BPL/BPP veneers leave the preceding helper's return value in
		// R0, so those two device-wide properties cannot rely on a descriptor.
		if framebuffer.Handle == 0 && ordinal >= 53 {
			framebuffer = r.Public.Framebuffers[r.Public.ScreenHandle]
		}
		if framebuffer.Handle == 0 {
			return guest.WIPIReturn{}, "", true, fmt.Errorf(
				"unknown framebuffer handle 0x%08x",
				handle,
			)
		}
		switch ordinal {
		case 50:
			return guest.WIPIReturn{Low: framebuffer.Pixels},
				"RAPTOR.grpGetFrameBufferPixels", true, nil
		case 51:
			return guest.WIPIReturn{Low: uint32(framebuffer.Width)},
				"RAPTOR.grpGetFrameBufferWidth", true, nil
		case 52:
			return guest.WIPIReturn{Low: uint32(framebuffer.Height)},
				"RAPTOR.grpGetFrameBufferHeight", true, nil
		case 53:
			bytesPerPixel := framebuffer.BitsPerPixel / 8
			return guest.WIPIReturn{Low: uint32(framebuffer.Width * bytesPerPixel)},
				"RAPTOR.grpGetFrameBufferBytesPerLine", true, nil
		default:
			return guest.WIPIReturn{Low: uint32(framebuffer.BitsPerPixel)},
				"RAPTOR.grpGetFrameBufferBitsPerPixel", true, nil
		}
	case 300:
		return guest.WIPIReturn{Low: 2},
			"RAPTOR.fsGetVolumeCount", true, nil
	case 301:
		return guest.WIPIReturn{Low: VolumeTable},
			"RAPTOR.fsGetVolumeList", true, nil
	case 302:
		// LGT's filesystem adapter accepts a volume index here. The public
		// virtual filesystem presents both advertised roots through one
		// namespace, so no host-side selection is necessary.
		return guest.WIPIReturn{},
			"RAPTOR.fsSelectVolume", true, nil
	case 122:
		timer, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		callback, err := r.CPU.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		// LGT defines MCTimer as one callback word. The generic public runtime
		// also supports a larger guest-visible timer layout for other ABIs.
		if timer != 0 {
			err = r.Public.WriteU32(timer, callback)
		}
		return guest.WIPIReturn{}, "RAPTOR.knlDefTimer", true, err
	case 123:
		timer, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		timeout, err := r.CPU.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		parameter, err := r.CPU.ReadRegister(cpu.RegisterR3)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		result, err := r.Public.SetTimer(timer, uint64(timeout), parameter, false)
		return result, "RAPTOR.knlSetTimer", true, err
	case 124:
		timer, err := r.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return guest.WIPIReturn{}, "", true, err
		}
		err = r.Public.UnsetTimer(timer, false)
		return guest.WIPIReturn{}, "RAPTOR.knlUnsetTimer", true, err
	case 1233:
		// The KTF Raptor title 얍 invokes this provider-private startup hook
		// once with (3, 1) and discards the result before querying handset
		// properties. No guest-visible state is exchanged.
		return guest.WIPIReturn{}, "RAPTOR.privateStartup1233", true, nil
	case 1400:
		// Raptor modules may invoke this provider-private runtime initializer
		// with (0, 2, 0, 0) before their public imports are resolved. Its
		// result is ignored, so model the provider's successful no-op boundary.
		return guest.WIPIReturn{}, "RAPTOR.privateRuntimeInit1400", true, nil
	default:
		return guest.WIPIReturn{}, "", false, nil
	}
}

func raptorWIPIImportName(ordinal uint32) (string, bool) {
	switch ordinal {
	case 100:
		return "MC_knlPrintk", true
	case 101:
		return "MC_knlSprintk", true
	case 120:
		return "MC_knlGetTotalMemory", true
	case 121:
		return "MC_knlGetFreeMemory", true
	case 125:
		return "MC_knlCurrentTime", true
	case 126:
		return "MC_knlGetSystemProperty", true
	case 127:
		return "MC_knlSetSystemProperty", true
	case 200:
		return "MC_grpGetImageProperty", true
	case 201:
		return "MC_grpGetImageFrameBuffer", true
	case 202:
		return "MC_grpGetScreenFrameBuffer", true
	case 204:
		return "MC_grpCreateOffScreenFrameBuffer", true
	case 205:
		return "MC_grpInitContext", true
	case 206:
		return "MC_grpSetContext", true
	case 209:
		return "MC_grpDrawLine", true
	// Bracketed by DrawLine and FillRect, so this is the vtable slot between
	// them. The title only reaches it once the save file loads.
	case 210:
		return "MC_grpDrawRect", true
	case 211:
		return "MC_grpFillRect", true
	// The low MC_grp vtable indices align with the WIPI catalog byte offsets
	// (ordinal = 200 + slot/4): CopyFrameBuffer 0x30, DrawImage 0x34, CopyArea
	// 0x38, DrawArc 0x3c, FillArc 0x40, DrawString 0x44. 라그나로크 바이올렛
	// renders every sprite through DrawImage (213); without it the draws were
	// silently dropped and the screen stayed black.
	case 212:
		return "MC_grpCopyFrameBuffer", true
	case 213:
		return "MC_grpDrawImage", true
	case 214:
		return "MC_grpCopyArea", true
	case 215:
		return "MC_grpDrawArc", true
	case 216:
		return "MC_grpFillArc", true
	case 217:
		return "MC_grpDrawString", true
	// DrawUnicodeString 0x48, GetRGBPixels 0x4c, SetRGBPixels 0x50 continue the
	// aligned range before the raptor +1 divergence at FlushLcd. 검은방2/3 draw
	// all their Korean text through DrawUnicodeString (218).
	case 218:
		return "MC_grpDrawUnicodeString", true
	case 219:
		return "MC_grpGetRGBPixels", true
	case 220:
		return "MC_grpSetRGBPixels", true
	case 222:
		return "MC_grpFlushLcd", true
	case 223:
		return "MC_grpGetPixelFromRGB", true
	case 225:
		return "MC_grpGetDisplayInfo", true
	// 영웅서기3 asks for a font handle here with (face, size, style) and feeds
	// the result straight into MC_grpSetContext index 7, the context's font
	// field, so an unresolved import leaves the Clet drawing with font 0.
	case 227:
		return "MC_grpGetFont", true
	case 233:
		return "MC_grpCreateImage", true
	// The 400 block is MC_FS in vtable order. 영웅서기3 pins every entry from
	// its own call sites: open takes a name and a mode, write is reached
	// through a helper that prints "===> FileWrite Error", close is called on
	// both the success and failure paths, seek is used with SEEK_END to size a
	// file, read takes (fd, buffer, length), and the attribute call takes the
	// name with a stack buffer whose word at +8 is then used as the length.
	case 400:
		return "MC_fsOpen", true
	case 401:
		return "MC_fsRead", true
	case 402:
		return "MC_fsWrite", true
	case 403:
		return "MC_fsClose", true
	case 404:
		return "MC_fsSeek", true
	case 405:
		return "MC_fsFileAttribute", true
	// The FS block continues in the public vtable order used by the KTF
	// bridge: slot 6 is remove and slot 16 is the existence probe. 제노니아1
	// pins 416 from its call sites — it asks for data/*.zt1 package files
	// (name, access mode 1, attribute buffer) before deciding whether to
	// download them from the carrier server.
	case 406:
		return "MC_fsRemove", true
	case 416:
		return "MC_fsIsExist", true
	case 117:
		return "MC_knlAlloc", true
	case 118:
		return "MC_knlCalloc", true
	case 119:
		return "MC_knlFree", true
	case 122:
		return "MC_knlDefTimer", true
	case 123:
		return "MC_knlSetTimer", true
	case 128:
		return "MC_knlGetResourceID", true
	case 129:
		return "MC_knlGetResource", true
	case 1029:
		return "strcpy", true
	// The C string family is contiguous from strcpy, so the ordinal between
	// strcpy and strcat is strncpy.
	case 1030:
		return "strncpy", true
	case 1031:
		return "strcat", true
	// The block keeps following string.h declaration order: strncat sits at
	// 1032, then the comparison pair. 제노니아1 pins 1033/1034 from its call
	// sites — it compares the handset model property against "SPH-V6900",
	// "SCH-B590", and the "PT-S" prefix during startup.
	case 1033:
		return "strcmp", true
	case 1034:
		return "strncmp", true
	case 1040:
		return "strstr", true
	case 1041:
		return "strlen", true
	case 1044:
		return "memcpy", true
	// The mem family is contiguous in <string.h> declaration order between the
	// confirmed anchors memcpy (1044) and memset (1048): memcpy, memmove, memcmp,
	// memchr, memset. 데몬헌터 calls 1045 with overlapping heap pointers 8 bytes
	// apart (memmove), and all four route to existing overlap-safe handlers in
	// dispatchCStdlib.
	case 1045:
		return "memmove", true
	case 1046:
		return "memcmp", true
	case 1047:
		return "memchr", true
	case 1048:
		return "memset", true
	// localtime(const time_t*) -> struct tm*: 4 titles (레이카르나, 블레이드마스터3,
	// 뮤직팩토리, 2010밴쿠버올림픽) call 1056 with a pointer and immediately deref the
	// returned struct at tm field offsets (sec/min/hour 0/4/8, mday 12), which
	// faulted while unimplemented (returned 0).
	case 1056:
		return "localtime", true
	default:
		return "", false
	}
}

func MergeResources(
	pkg map[string][]byte,
	overrides map[string][]byte,
) map[string][]byte {
	result := guest.CloneSliceMap(pkg)
	for name, data := range overrides {
		result[name] = append([]byte(nil), data...)
	}
	return result
}

func PrimarySections(image raptorloader.Image) (
	text raptorloader.Section,
	bss raptorloader.Section,
	err error,
) {
	var ok bool
	text, ok = image.CodeSection()
	if !ok {
		return raptorloader.Section{}, raptorloader.Section{}, fmt.Errorf("Raptor code section is missing")
	}
	bss, ok = image.ZeroSection()
	if !ok {
		return raptorloader.Section{}, raptorloader.Section{}, fmt.Errorf("Raptor zero-fill section is missing")
	}
	return text, bss, nil
}

func RequiredMemory(image raptorloader.Image) uint64 {
	total := uint64(guest.DefaultStackSize) +
		uint64(wipi.SystemSize) +
		uint64(wipi.TrampolineSize) +
		uint64(guest.HeapSize)
	for _, section := range image.AllocatedSections() {
		total += uint64(section.Size)
	}
	return total
}
