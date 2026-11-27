package application

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"

	"github.com/mirusu400/aram-core/application/internal/guest"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/daf"
	"github.com/mirusu400/aram-core/wipi"
)

const (
	// CletProfileID names the SKT native WIPI-C Clet platform.
	CletProfileID = "wipi-c-clet/skt/generic"

	// cletTextBase is where a relocatable Clet image is placed. It is clear of
	// the WIPI system page, the trampolines, the shared heap and the stack.
	cletTextBase = uint32(0x30000000)

	// Standard WIPI Clet event types delivered to handleCletEvent.
	cletKeyPressEvent   = uint32(2)
	cletKeyReleaseEvent = uint32(3)
)

// cletRuntime drives a native WIPI-C Clet: an image with no main loop of its
// own, entered through its lifecycle functions and its timer callbacks.
type cletRuntime struct {
	entries     daf.Entries
	dataBase    uint32
	initialData []byte
	started     bool
}

func (m *Machine) loadDAF(ctx context.Context, source machinecore.Source, pkg daf.Package) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	textSize := uint32(len(pkg.Image.Text))
	dataSize := uint32(len(pkg.Image.Data)) + pkg.Image.BSSSize
	requiredMemory := uint64(textSize) + uint64(dataSize) + uint64(DefaultStackSize) +
		uint64(wipi.SystemSize) + uint64(wipi.TrampolineSize) + uint64(guest.HeapSize)
	if requiredMemory > m.memoryLimit {
		return fmt.Errorf("load %q: guest memory %d exceeds limit %d",
			source.Name, requiredMemory, m.memoryLimit)
	}
	const page = uint32(0x1000)
	dataBase := cletTextBase + (textSize+page-1)&^(page-1) + page
	loaded, err := pkg.Image.Load(cletTextBase, dataBase, cletImportRoot)
	if err != nil {
		return fmt.Errorf("relocate Clet image: %w", err)
	}
	if err := m.cpu.Map(cletTextBase, textSize,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return fmt.Errorf("map Clet text: %w", err)
	}
	if err := m.cpu.WriteMemory(cletTextBase, loaded.Text); err != nil {
		return fmt.Errorf("copy Clet text: %w", err)
	}
	if err := m.cpu.Map(dataBase, dataSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		return fmt.Errorf("map Clet data: %w", err)
	}
	if err := m.cpu.WriteMemory(dataBase, loaded.Data); err != nil {
		return fmt.Errorf("copy Clet data: %w", err)
	}
	if err := m.cpu.Map(DefaultStackBase, DefaultStackSize,
		cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		return fmt.Errorf("map application stack: %w", err)
	}
	if err := wipirt.MapRuntimeMemory(m.cpu); err != nil {
		return err
	}
	profileID := source.ProfileID
	if profileID == "" {
		profileID = CletProfileID
	}
	publicRuntime, err := wipirt.NewRuntimeForProfile(
		m.cpu, m.frame, profileID, "skt", 16, "wipi-c",
		m.fallbackFont, m.outputSampleRate, m.outputChannels,
	)
	if err != nil {
		return fmt.Errorf("initialize public WIPI runtime: %w", err)
	}
	if err := publicRuntime.SetWallEpoch(m.wallEpochMillis); err != nil {
		return fmt.Errorf("initialize public WIPI wall clock: %w", err)
	}
	m.wipi = publicRuntime
	publicRuntime.IndirectMemoryIDs = true
	publicRuntime.OfflineCarrierAuth = m.offlineCarrierAuth
	publicRuntime.InvokeSync = func(
		callbackContext context.Context,
		callback wipirt.GuestCallback,
	) (uint32, error) {
		_, value, callbackErr := m.invokeWIPICallback(callbackContext, callback)
		return value, callbackErr
	}
	resources := make(map[string][]byte, len(pkg.Image.Resources)+len(m.initialResources))
	for _, resource := range pkg.Image.Resources {
		resources[resource.Name] = resource.Data
	}
	for name, data := range m.initialResources {
		resources[name] = data
	}
	m.initialResources = resources
	// Whatever else the archive carries beside the executable is the
	// application's own directory, which it opens through MC_fs.
	directory := make(map[string][]byte)
	for name, data := range pkg.Files {
		if name != pkg.DAFName && name != pkg.MODName {
			directory[name] = data
		}
	}
	publicRuntime.RegisterSharedPackageFiles(directory)
	if err := m.installWIPIResources(); err != nil {
		return err
	}

	entry := loaded.Entries.Start
	if err := m.cpu.WriteRegister(cpu.RegisterSP, DefaultStackBase+DefaultStackSize); err != nil {
		return fmt.Errorf("initialize stack pointer: %w", err)
	}
	if err := m.cpu.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1); err != nil {
		return fmt.Errorf("initialize link register: %w", err)
	}
	if err := m.cpu.WriteRegister(cpu.RegisterPC, entry&^uint32(1)); err != nil {
		return fmt.Errorf("initialize entry point: %w", err)
	}
	mode, status := cpu.ModeARM, uint32(0)
	if entry&1 != 0 {
		mode, status = cpu.ModeThumb, cpu.StatusThumb
	}
	if err := m.cpu.WriteRegister(cpu.RegisterCPSR, status); err != nil {
		return fmt.Errorf("initialize execution mode: %w", err)
	}
	initialContext, err := m.cpu.SaveContext()
	if err != nil {
		return fmt.Errorf("capture initial CPU context: %w", err)
	}

	source.ProfileID = profileID
	m.source = source
	m.info = ImageInfo{
		Name:       pkg.BaseName,
		ProfileID:  profileID,
		SourceKind: loader.KindDAF,
		ImageSHA256: imageSHA256(loader.KindDAF, []imageSegment{
			{Address: cletTextBase, Size: textSize, Writable: true, Executable: true, Data: loaded.Text},
			{Address: dataBase, Size: dataSize, Writable: true, Data: loaded.Data},
		}),
		EntryPoint:  entry,
		Mode:        mode,
		TextAddress: cletTextBase,
		TextSize:    textSize,
		BSSAddress:  dataBase,
		BSSSize:     dataSize,
	}
	m.clet = &cletRuntime{
		entries:     loaded.Entries,
		dataBase:    dataBase,
		initialData: loaded.Data,
	}
	if err := m.clet.installImports(m.cpu, publicRuntime); err != nil {
		return err
	}
	m.initialText = loaded.Text
	m.initialContext = initialContext
	m.state = machinecore.StateReady
	draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	return nil
}

// cletStartupFrameLimit bounds how long Start follows a Clet's timers toward
// its first presented frame.
const cletStartupFrameLimit = 64

// startClet runs startClet and then the frames it takes the Clet to present.
// A Clet returns from startClet after arming a timer, so the first screen
// belongs to a later callback; the product Start command should reach it.
func (m *Machine) startClet(ctx context.Context) error {
	if err := m.stepCletFrame(ctx); err != nil {
		return err
	}
	for range cletStartupFrameLimit {
		m.mu.Lock()
		done := m.state != machinecore.StatePaused || m.wipi.Stats.PresentCount != 0
		m.mu.Unlock()
		if done {
			return nil
		}
		if err := m.stepCletFrame(ctx); err != nil {
			return err
		}
	}
	return nil
}

// stepCletFrame runs one video quantum of a Clet: startClet on the first
// frame, then whatever timers and input came due.
func (m *Machine) stepCletFrame(ctx context.Context) error {
	m.mu.Lock()
	started := m.clet.started
	m.mu.Unlock()
	if !started {
		return m.runCletStart(ctx)
	}
	_, _, err := m.pumpWIPICallbacks(ctx, guest.WIPIFrameDuration)
	return err
}

func (m *Machine) runCletStart(ctx context.Context) error {
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
		return fmt.Errorf("start Clet from %s: %w", state, ErrInvalidState)
	}
	procedure := m.clet.entries.Start
	m.state = machinecore.StateRunning
	if err := m.wipi.BeginServiceExecution(); err != nil {
		m.state = machinecore.StateFaulted
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	result, _, err := m.invokeWIPICallback(ctx, wipirt.GuestCallback{Procedure: procedure})

	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastResult = result
	switch {
	case err != nil:
		m.state = machinecore.StateFaulted
	case result.Reason == cpu.StopExited:
		m.state = machinecore.StateStopped
	default:
		m.clet.started = true
		m.state = machinecore.StatePaused
	}
	fault := ""
	if err != nil {
		fault = err.Error()
	}
	if serviceErr := m.wipi.FinishServiceExecution(m.state, result.Instructions, fault); serviceErr != nil {
		m.state = machinecore.StateFaulted
		return serviceErr
	}
	if err != nil {
		return fmt.Errorf("execute startClet at 0x%08x: %w", result.PC, err)
	}
	return nil
}

// inputCallback turns a key transition into a handleCletEvent call.
func (c *cletRuntime) inputCallback(event machinecore.InputEvent) (wipirt.GuestCallback, bool) {
	key, ok := guest.InputKeyCode(event.Control)
	if !ok || !c.started || c.entries.HandleEvent == 0 {
		return wipirt.GuestCallback{}, false
	}
	eventType := cletKeyReleaseEvent
	if event.Pressed {
		eventType = cletKeyPressEvent
	}
	return wipirt.GuestCallback{
		Procedure: c.entries.HandleEvent,
		Args:      [4]uint32{eventType, uint32(int32(key)), 0},
	}, true
}
