package application

import (
	"context"
	"encoding/binary"
	"fmt"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader/raptor"
	"github.com/mirusu400/aram-core/wipi"
)

const (
	raptorProfileID = "wipi-1.2.1/lgt/raptor"

	raptorKernelBase = uint32(0x01008000)
	raptorDletBase   = uint32(0x01008400)
	raptorWIPICBase  = uint32(0x01008800)

	raptorVolumeTable = uint32(0x01008c00)
	raptorLocalVolume = uint32(0x01008c08)
	raptorShareVolume = uint32(0x01008c0b)

	raptorImportStubBase  = uint32(0x0110a000)
	raptorImportStubSize  = uint32(0x00004000)
	raptorDletModuleStub  = uint32(0x0110e000)
	raptorDletResolveStub = uint32(0x0110e004)
	raptorDletWaitStub    = uint32(0x0110e008)

	raptorCletHeaderSize     = uint32(0x30)
	raptorDependencyDataSlot = uint32(0x214)

	// The LGT Raptor lifecycle ABI translates HAL key events to these
	// CletHandleEvent type values. The reference Clet compares them directly
	// before dispatching its press and release handlers.
	raptorKeyPressEvent   = uint32(502)
	raptorKeyReleaseEvent = uint32(503)

	raptorStartupFrameLimit = 64
)

type raptorClet struct {
	Table       uint32
	Name        string
	Start       uint32
	Destroy     uint32
	Pause       uint32
	Resume      uint32
	Paint       uint32
	HandleEvent uint32
}

type raptorRuntime struct {
	cpu    cpu.Backend
	public *wipiRuntime
	pkg    raptor.Package
	clet   raptorClet

	moduleInitialized bool
	started           bool
	resolvedImports   map[uint32]uint64
	importTrace       []raptorImportCall
}

type raptorImportCall struct {
	Ordinal uint32
	Args    [4]uint32
	LR      uint32
}

func raptorInputCallback(
	procedure uint32,
	event machinecore.InputEvent,
) (wipiGuestCallback, bool) {
	key, ok := inputKeyCode(event.Control)
	if !ok || procedure == 0 {
		return wipiGuestCallback{}, false
	}
	eventType := raptorKeyReleaseEvent
	if event.Pressed {
		eventType = raptorKeyPressEvent
	}
	return wipiGuestCallback{
		procedure: procedure,
		args: [4]uint32{
			eventType,
			uint32(int32(key)),
			0,
		},
	}, true
}

func newRaptorRuntime(
	backend cpu.Backend,
	public *wipiRuntime,
	pkg raptor.Package,
) (*raptorRuntime, error) {
	clet, err := inspectRaptorClet(pkg.Image)
	if err != nil {
		return nil, err
	}
	runtime := &raptorRuntime{
		cpu:             backend,
		public:          public,
		pkg:             pkg,
		clet:            clet,
		resolvedImports: make(map[uint32]uint64),
	}
	if err := runtime.installInterfaces(); err != nil {
		return nil, err
	}
	return runtime, nil
}

func inspectRaptorClet(image raptor.Image) (raptorClet, error) {
	data, ok := image.DataSection()
	if !ok || len(data.Data) < int(raptorCletHeaderSize) {
		return raptorClet{}, fmt.Errorf(
			"initialize Raptor Clet: read-write lifecycle header is missing",
		)
	}
	text, ok := image.CodeSection()
	if !ok {
		return raptorClet{}, fmt.Errorf("initialize Raptor Clet: code section is missing")
	}
	// Most modules put the lifecycle header at the start of the read-write
	// region, but some place it further in, so find it by its shape rather
	// than by a fixed offset: version 3, the code base, a readable name, and
	// six Thumb entry points. The base may carry the interworking bit, the
	// same way entry offsets do.
	limit := len(data.Data) - int(raptorCletHeaderSize)
	for offset := 0; offset <= limit; offset += 4 {
		if clet, ok := raptorCletAt(image, data, text, offset); ok {
			return clet, nil
		}
	}
	return raptorClet{}, fmt.Errorf(
		"initialize Raptor Clet: %s holds no version 3 lifecycle header",
		data.Name,
	)
}

func raptorCletAt(
	image raptor.Image,
	data raptor.Section,
	text raptor.Section,
	offset int,
) (raptorClet, bool) {
	header := data.Data[offset:]
	if binary.LittleEndian.Uint32(header[0:4]) != 3 ||
		binary.LittleEndian.Uint32(header[4:8])&^1 != text.Address {
		return raptorClet{}, false
	}
	name, ok := raptorImageCString(
		image,
		binary.LittleEndian.Uint32(header[8:12]),
		255,
	)
	if !ok || name == "" {
		return raptorClet{}, false
	}
	clet := raptorClet{
		Table:       data.Address + uint32(offset),
		Name:        name,
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
			return raptorClet{}, false
		}
	}
	return clet, true
}

func raptorImageCString(
	image raptor.Image,
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

func raptorExecutableAddress(image raptor.Image, address uint32) bool {
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

func mapRaptorImage(backend cpu.Backend, image raptor.Image) error {
	for _, section := range image.AllocatedSections() {
		permissions := cpu.PermissionRead | cpu.PermissionWrite
		if section.Executable() {
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

func (r *raptorRuntime) installInterfaces() error {
	dlet := make([]byte, 12)
	binary.LittleEndian.PutUint32(dlet[0:4], raptorDletModuleStub|1)
	binary.LittleEndian.PutUint32(dlet[4:8], raptorDletResolveStub|1)
	binary.LittleEndian.PutUint32(dlet[8:12], raptorDletWaitStub|1)
	if err := r.cpu.WriteMemory(raptorDletBase, dlet); err != nil {
		return fmt.Errorf("install Raptor dlet interface: %w", err)
	}
	volumes := make([]byte, 14)
	binary.LittleEndian.PutUint32(volumes[0:4], raptorLocalVolume)
	binary.LittleEndian.PutUint32(volumes[4:8], raptorShareVolume)
	copy(volumes[8:11], "/L\x00")
	copy(volumes[11:14], "/S\x00")
	if err := r.cpu.WriteMemory(raptorVolumeTable, volumes); err != nil {
		return fmt.Errorf("install Raptor volume interface: %w", err)
	}
	return nil
}

func (r *raptorRuntime) restoreImage() error {
	for _, section := range r.pkg.Image.AllocatedSections() {
		if err := zeroGuestMemory(r.cpu, section.Address, section.Size); err != nil {
			return fmt.Errorf("clear Raptor section %q: %w", section.Name, err)
		}
		if len(section.Data) != 0 {
			if err := r.cpu.WriteMemory(section.Address, section.Data); err != nil {
				return fmt.Errorf("restore Raptor section %q: %w", section.Name, err)
			}
		}
	}
	r.moduleInitialized = false
	r.started = false
	r.resolvedImports = make(map[uint32]uint64)
	r.importTrace = nil
	return nil
}

func (r *raptorRuntime) dispatchTrap(
	ctx context.Context,
	trap uint32,
) (bool, error) {
	switch trap {
	case raptorDletModuleStub:
		module, err := r.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return true, err
		}
		if module == 0 {
			module = 1
		}
		return true, r.public.returnFromTrap(wipiReturn{low: module})

	case raptorDletResolveStub:
		ordinal, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return true, err
		}
		if ordinal >= raptorImportStubSize/4 {
			return true, fmt.Errorf("Raptor import ordinal 0x%x exceeds trampoline range", ordinal)
		}
		r.resolvedImports[ordinal]++
		stub := raptorImportStubBase + ordinal*4
		return true, r.public.returnFromTrap(wipiReturn{low: stub | 1})

	case raptorDletWaitStub:
		return true, r.public.returnFromTrap(wipiReturn{})
	}
	if trap < raptorImportStubBase ||
		trap >= raptorImportStubBase+raptorImportStubSize ||
		(trap-raptorImportStubBase)%4 != 0 {
		return false, nil
	}
	ordinal := (trap - raptorImportStubBase) / 4
	return true, r.dispatchImport(ctx, ordinal)
}

func (r *raptorRuntime) dispatchImport(
	ctx context.Context,
	ordinal uint32,
) error {
	call := raptorImportCall{Ordinal: ordinal}
	for register := cpu.RegisterR0; register <= cpu.RegisterR3; register++ {
		value, err := r.cpu.ReadRegister(register)
		if err != nil {
			return err
		}
		call.Args[register] = value
	}
	lr, err := r.cpu.ReadRegister(cpu.RegisterLR)
	if err != nil {
		return err
	}
	call.LR = lr
	if len(r.importTrace) < maxSavedWIPIEntries {
		r.importTrace = append(r.importTrace, call)
	}
	if result, name, handled, privateErr := r.dispatchPrivateImport(ordinal); handled {
		if privateErr != nil {
			return fmt.Errorf("%s: %w", name, privateErr)
		}
		r.public.stats.APICalls++
		r.public.stats.ImplementedCalls++
		r.public.stats.LastAPI = name
		r.public.observed[name]++
		return r.public.returnFromTrap(result)
	}
	if publicName, ok := raptorWIPIImportName(ordinal); ok {
		api, found := wipi.Lookup(publicName)
		if !found {
			return fmt.Errorf(
				"Raptor import %d names unknown public WIPI API %q",
				ordinal,
				publicName,
			)
		}
		return r.public.dispatchAPI(ctx, api)
	}
	name := fmt.Sprintf("RAPTOR.wipic#%d", ordinal)
	r.public.stats.APICalls++
	r.public.stats.UnimplementedCalls++
	r.public.stats.LastAPI = name
	r.public.stats.LastUnimplemented = name
	r.public.observed[name]++
	r.public.unimplemented[name]++
	return r.public.returnFromTrap(wipiReturn{})
}

func (r *raptorRuntime) dispatchPrivateImport(
	ordinal uint32,
) (wipiReturn, string, bool, error) {
	switch ordinal {
	case 50, 51, 52, 53, 54:
		handle, err := r.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return wipiReturn{}, "", true, err
		}
		if handle == 0 {
			handle, err = r.public.ensureScreenFramebuffer()
			if err != nil {
				return wipiReturn{}, "", true, err
			}
		}
		framebuffer := r.public.framebuffers[handle]
		if framebuffer.handle == 0 {
			for _, candidate := range r.public.framebuffers {
				if candidate.pixels == handle {
					framebuffer = candidate
					break
				}
			}
		}
		// LGT's BPL/BPP veneers leave the preceding helper's return value in
		// R0, so those two device-wide properties cannot rely on a descriptor.
		if framebuffer.handle == 0 && ordinal >= 53 {
			framebuffer = r.public.framebuffers[r.public.screenHandle]
		}
		if framebuffer.handle == 0 {
			return wipiReturn{}, "", true, fmt.Errorf(
				"unknown framebuffer handle 0x%08x",
				handle,
			)
		}
		switch ordinal {
		case 50:
			return wipiReturn{low: framebuffer.pixels},
				"RAPTOR.grpGetFrameBufferPixels", true, nil
		case 51:
			return wipiReturn{low: uint32(framebuffer.width)},
				"RAPTOR.grpGetFrameBufferWidth", true, nil
		case 52:
			return wipiReturn{low: uint32(framebuffer.height)},
				"RAPTOR.grpGetFrameBufferHeight", true, nil
		case 53:
			bytesPerPixel := framebuffer.bitsPerPixel / 8
			return wipiReturn{low: uint32(framebuffer.width * bytesPerPixel)},
				"RAPTOR.grpGetFrameBufferBytesPerLine", true, nil
		default:
			return wipiReturn{low: uint32(framebuffer.bitsPerPixel)},
				"RAPTOR.grpGetFrameBufferBitsPerPixel", true, nil
		}
	case 300:
		return wipiReturn{low: 2},
			"RAPTOR.fsGetVolumeCount", true, nil
	case 301:
		return wipiReturn{low: raptorVolumeTable},
			"RAPTOR.fsGetVolumeList", true, nil
	case 302:
		// LGT's filesystem adapter accepts a volume index here. The public
		// virtual filesystem presents both advertised roots through one
		// namespace, so no host-side selection is necessary.
		return wipiReturn{},
			"RAPTOR.fsSelectVolume", true, nil
	case 122:
		timer, err := r.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return wipiReturn{}, "", true, err
		}
		callback, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return wipiReturn{}, "", true, err
		}
		// LGT defines MCTimer as one callback word. The generic public runtime
		// also supports a larger guest-visible timer layout for other ABIs.
		if timer != 0 {
			err = r.public.writeU32(timer, callback)
		}
		return wipiReturn{}, "RAPTOR.knlDefTimer", true, err
	case 123:
		timer, err := r.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return wipiReturn{}, "", true, err
		}
		timeout, err := r.cpu.ReadRegister(cpu.RegisterR1)
		if err != nil {
			return wipiReturn{}, "", true, err
		}
		parameter, err := r.cpu.ReadRegister(cpu.RegisterR3)
		if err != nil {
			return wipiReturn{}, "", true, err
		}
		result, err := r.public.setTimer(timer, uint64(timeout), parameter, false)
		return result, "RAPTOR.knlSetTimer", true, err
	case 124:
		timer, err := r.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			return wipiReturn{}, "", true, err
		}
		err = r.public.unsetTimer(timer, false)
		return wipiReturn{}, "RAPTOR.knlUnsetTimer", true, err
	default:
		return wipiReturn{}, "", false, nil
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
	case 1040:
		return "strstr", true
	case 1041:
		return "strlen", true
	case 1044:
		return "memcpy", true
	case 1048:
		return "memset", true
	default:
		return "", false
	}
}

func (m *Machine) runRaptorStart(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return cpu.ErrClosed
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused:
	default:
		state := m.state
		m.mu.Unlock()
		return fmt.Errorf("execute Raptor application from %s: %w", state, ErrInvalidState)
	}
	runtime := m.raptor
	m.state = machinecore.StateRunning
	if err := m.wipi.beginServiceExecution(); err != nil {
		m.state = machinecore.StateFaulted
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	var (
		result cpu.Result
		err    error
	)
	if !runtime.moduleInitialized {
		result, _, err = m.invokeWIPICallback(ctx, wipiGuestCallback{
			procedure: runtime.pkg.Image.Entry | 1,
			args: [4]uint32{
				raptorKernelBase,
				raptorDletBase,
				raptorWIPICBase,
			},
		})
		if err == nil {
			runtime.moduleInitialized = true
			var dataBase [4]byte
			if readErr := m.cpu.ReadMemory(
				raptorKernelBase+raptorDependencyDataSlot,
				dataBase[:],
			); readErr != nil {
				err = readErr
			} else if got := binary.LittleEndian.Uint32(dataBase[:]); got != runtime.clet.Table {
				err = fmt.Errorf(
					"Raptor initializer installed data base 0x%08x, want 0x%08x",
					got,
					runtime.clet.Table,
				)
			}
		}
	}
	if err == nil && !runtime.started {
		result, _, err = m.invokeWIPICallback(ctx, wipiGuestCallback{
			procedure: runtime.clet.Start,
		})
		if err == nil {
			runtime.started = true
		}
	}
	if err == nil && runtime.started && result.Instructions == 0 {
		result = cpu.Result{
			Reason: cpu.StopBreakpoint,
			PC:     returnSentinel,
		}
	}
	return m.finishRaptorCall(result, err, result.Instructions)
}

func (m *Machine) startRaptor(ctx context.Context) error {
	m.mu.Lock()
	started := m.raptor != nil && m.raptor.started
	m.mu.Unlock()
	if started {
		return m.stepRaptorFrame(ctx)
	}
	if err := m.runRaptorStart(ctx); err != nil {
		return err
	}
	// Raptor Clets commonly return from startClet after arming a timer. Advance
	// that event loop to the first visibly changed frame so the product Start
	// command reaches an actual application screen instead of the callback
	// return sentinel.
	for range raptorStartupFrameLimit {
		if err := m.stepRaptorFrame(ctx); err != nil {
			return err
		}
		if m.raptorFrameVisible() {
			return nil
		}
		m.mu.Lock()
		stopped := m.state == machinecore.StateStopped
		m.mu.Unlock()
		if stopped {
			return nil
		}
	}
	return nil
}

func (m *Machine) raptorFrameVisible() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for offset := 0; offset+3 < len(m.frame.Pix); offset += 4 {
		if m.frame.Pix[offset] != 0 ||
			m.frame.Pix[offset+1] != 0 ||
			m.frame.Pix[offset+2] != 0 {
			return true
		}
	}
	return false
}

func (m *Machine) stepRaptorFrame(ctx context.Context) error {
	m.mu.Lock()
	started := m.raptor != nil && m.raptor.started
	m.mu.Unlock()
	if !started {
		if err := m.runRaptorStart(ctx); err != nil {
			return err
		}
	}
	callbackResult, stopped, err := m.pumpWIPICallbacks(ctx, wipiFrameDuration)
	if err != nil || stopped {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return cpu.ErrClosed
	}
	if m.state != machinecore.StatePaused && m.state != machinecore.StateReady {
		state := m.state
		m.mu.Unlock()
		return fmt.Errorf("paint Raptor application from %s: %w", state, ErrInvalidState)
	}
	runtime := m.raptor
	bounds := m.frame.Bounds()
	m.state = machinecore.StateRunning
	if err := m.wipi.beginServiceExecution(); err != nil {
		m.state = machinecore.StateFaulted
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	result, _, callErr := m.invokeWIPICallback(ctx, wipiGuestCallback{
		procedure: runtime.clet.Paint,
		args: [4]uint32{
			0,
			0,
			uint32(bounds.Dx()),
			uint32(bounds.Dy()),
		},
	})
	paintInstructions := result.Instructions
	result.Instructions += callbackResult.Instructions
	return m.finishRaptorCall(result, callErr, paintInstructions)
}

func (m *Machine) finishRaptorCall(
	result cpu.Result,
	err error,
	serviceInstructions uint64,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastResult = result
	switch {
	case err != nil:
		m.state = machinecore.StateFaulted
	case result.Reason == cpu.StopExited:
		m.state = machinecore.StateStopped
	default:
		m.state = machinecore.StatePaused
	}
	fault := ""
	if err != nil {
		fault = err.Error()
	}
	if serviceErr := m.wipi.finishServiceExecution(
		m.state,
		serviceInstructions,
		fault,
	); serviceErr != nil {
		m.state = machinecore.StateFaulted
		return serviceErr
	}
	if err != nil {
		return fmt.Errorf("execute Raptor Clet at 0x%08x: %w", result.PC, err)
	}
	return nil
}

func mergeRaptorResources(
	pkg map[string][]byte,
	overrides map[string][]byte,
) map[string][]byte {
	result := cloneByteMap(pkg)
	for name, data := range overrides {
		result[name] = append([]byte(nil), data...)
	}
	return result
}

func raptorPrimarySections(image raptor.Image) (
	text raptor.Section,
	bss raptor.Section,
	err error,
) {
	var ok bool
	text, ok = image.CodeSection()
	if !ok {
		return raptor.Section{}, raptor.Section{}, fmt.Errorf("Raptor code section is missing")
	}
	bss, ok = image.ZeroSection()
	if !ok {
		return raptor.Section{}, raptor.Section{}, fmt.Errorf("Raptor zero-fill section is missing")
	}
	return text, bss, nil
}

func raptorRequiredMemory(image raptor.Image) uint64 {
	total := uint64(DefaultStackSize) +
		uint64(systemSize) +
		uint64(trampolineSize) +
		uint64(guestHeapSize)
	for _, section := range image.AllocatedSections() {
		total += uint64(section.Size)
	}
	return total
}
