// Package application implements ARAM's WIPI native-application machine.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/eads"
	"github.com/mirusu400/aram-core/loader/ktf"
	"github.com/mirusu400/aram-core/loader/raptor"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	DefaultProfileID = "wipi-1.2.1/generic"
	DefaultStackBase = uint32(0x7ff00000)
	DefaultStackSize = uint32(0x00100000)
	DefaultRunBudget = uint64(1)
	// DefaultHandsetRunBudget models the application CPU time available on a
	// mid-2000s ARM9 handset during one 60 Hz video quantum. Product adapters
	// use this value while deterministic tools can retain DefaultRunBudget or
	// request another deliberately small slice.
	DefaultHandsetRunBudget = uint64(750_000)
	// DefaultKTFHandsetRunBudget models the application CPU time available on
	// a mid-2000s ARM9 KTF handset during one 60 Hz video quantum. It is kept
	// separate from DefaultRunBudget so deterministic tools can still request
	// deliberately tiny execution slices.
	DefaultKTFHandsetRunBudget = uint64(1_000_000)
	DefaultMemoryLimit         = uint64(512 << 20)
	maxApplicationSize         = int64(512 << 20)
	ktfProfileID               = "wipi-1.2.1/ktf/generic"
	// KTF Java applications need enough instructions per host video quantum
	// for their cooperative game and paint threads to make visible progress.
	// A 1K slice leaves several real titles in initialization indefinitely at
	// ordinary 60 Hz frontend scheduling.
	ktfRunBudgetMin            = uint64(10_000)
	ktfTaskSlicesPerQuantumMax = 64
	ktfFrameDuration           = (time.Second + 30) / 60
	// wipiFrameDuration is the guest time one native-WIPI, Raptor, or EADS
	// presentation quantum advances.
	wipiFrameDuration = 16 * time.Millisecond
)

var (
	ErrUnsupportedSource = errors.New("unsupported WIPI application source")
	ErrInvalidState      = errors.New("invalid application machine state")
)

type CPUFactory func() cpu.Backend

// Factory creates a fresh application machine for each source. The portable
// interpreter is the default so product portability does not depend on CGO or
// executable host memory.
type Factory struct {
	NewCPU    CPUFactory
	RunBudget uint64
	// FrameRunBudget is the generic native-WIPI execution budget for one
	// presentation quantum. Zero inherits RunBudget.
	FrameRunBudget  uint64
	KTFRunBudget    uint64
	MemoryLimit     uint64
	FramebufferSize image.Point
	Resources       map[string][]byte
}

func NewFactory() Factory {
	return Factory{
		NewCPU:      func() cpu.Backend { return interpreter.New() },
		RunBudget:   DefaultRunBudget,
		MemoryLimit: DefaultMemoryLimit,
		FramebufferSize: image.Point{
			X: 240,
			Y: 320,
		},
	}
}

func (f Factory) Create(ctx context.Context, source machinecore.Source) (machinecore.Machine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if machine, matched, err := f.createSKVMMachine(ctx, source); matched || err != nil {
		return machine, err
	}
	newCPU := f.NewCPU
	if newCPU == nil {
		newCPU = func() cpu.Backend { return interpreter.New() }
	}
	backend := newCPU()
	if backend == nil {
		return nil, machinecore.ErrBackendUnavailable
	}
	budget := f.RunBudget
	if budget == 0 {
		budget = DefaultRunBudget
	}
	frameBudget := f.FrameRunBudget
	if frameBudget == 0 {
		frameBudget = budget
	}
	memoryLimit := f.MemoryLimit
	if memoryLimit == 0 {
		memoryLimit = DefaultMemoryLimit
	}
	size := f.FramebufferSize
	if size.X <= 0 || size.Y <= 0 {
		size = image.Pt(240, 320)
	}
	machine := &Machine{
		cpu:              backend,
		state:            machinecore.StateEmpty,
		runBudget:        budget,
		ktfRunBudget:     f.KTFRunBudget,
		memoryLimit:      memoryLimit,
		frame:            image.NewRGBA(image.Rect(0, 0, size.X, size.Y)),
		initialResources: cloneByteMap(f.Resources),
		frameRunBudget:   frameBudget,
	}
	if err := machine.Load(ctx, source); err != nil {
		_ = backend.Close()
		return nil, err
	}
	return machine, nil
}

type ImageInfo struct {
	Name        string
	ProfileID   string
	SourceKind  loader.Kind
	EntryPoint  uint32
	Mode        cpu.Mode
	TextAddress uint32
	TextSize    uint32
	BSSAddress  uint32
	BSSSize     uint32
}

type Machine struct {
	mu               sync.Mutex
	cpu              cpu.Backend
	wipi             *wipiRuntime
	minigame         *minigameRuntime
	ktf              *ktfRuntime
	raptor           *raptorRuntime
	ktfStarted       bool
	state            machinecore.State
	source           machinecore.Source
	info             ImageInfo
	initialText      []byte
	initialContext   []byte
	initialResources map[string][]byte
	lastResult       cpu.Result
	runBudget        uint64
	frameRunBudget   uint64
	ktfRunBudget     uint64
	memoryLimit      uint64
	frame            *image.RGBA
	input            []machinecore.InputEvent
	closed           bool
}

func (m *Machine) Load(ctx context.Context, source machinecore.Source) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return cpu.ErrClosed
	}
	if m.state != machinecore.StateEmpty {
		return fmt.Errorf("load from %s: %w", m.state, ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := source.Validate(); err != nil {
		return fmt.Errorf("load application: %w", err)
	}
	if source.Size > maxApplicationSize {
		return fmt.Errorf("load %q: source size %d exceeds limit", source.Name, source.Size)
	}

	data, err := io.ReadAll(io.NewSectionReader(source.ReaderAt, 0, source.Size))
	if err != nil {
		return fmt.Errorf("read application at offset 0x0: %w", err)
	}
	if int64(len(data)) != source.Size {
		return fmt.Errorf("read application at offset 0x%x: %w", len(data), io.ErrUnexpectedEOF)
	}
	digest := sha256.Sum256(data)
	actualSHA256 := hex.EncodeToString(digest[:])
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, actualSHA256) {
		return fmt.Errorf(
			"load %q: SHA-256 mismatch: expected %s, got %s",
			source.Name,
			source.SHA256,
			actualSHA256,
		)
	}
	source.SHA256 = actualSHA256
	ktfPackage, ktfErr := ktf.Inspect(data)
	if ktfErr == nil {
		return m.loadKTF(ctx, source, ktfPackage)
	}
	if errors.Is(ktfErr, ktf.ErrProtectedContent) {
		return fmt.Errorf(
			"%w: inspect KTF WIPI package: %v",
			ErrUnsupportedSource,
			ktfErr,
		)
	}
	if !errors.Is(ktfErr, ktf.ErrNotPackage) {
		var formatErr *ktf.FormatError
		if !errors.As(ktfErr, &formatErr) ||
			formatErr.Path != "archive" ||
			!strings.HasPrefix(formatErr.Reason, "invalid ZIP:") {
			return fmt.Errorf("inspect KTF WIPI package: %w", ktfErr)
		}
	}
	raptorPackage, raptorErr := raptor.Inspect(data)
	if raptorErr == nil {
		return m.loadRaptor(ctx, source, raptorPackage)
	}
	if !errors.Is(raptorErr, raptor.ErrNotPackage) {
		var formatErr *raptor.FormatError
		if !errors.As(raptorErr, &formatErr) ||
			formatErr.Path != "archive" ||
			!strings.HasPrefix(formatErr.Reason, "invalid ZIP:") {
			return fmt.Errorf("inspect Raptor WIPI-C package: %w", raptorErr)
		}
	}
	container, err := loader.InspectContainer(data)
	if err != nil || len(container.Images) == 0 {
		if err == nil {
			err = ErrUnsupportedSource
		}
		return fmt.Errorf("inspect WIPI application: %w", err)
	}
	selected := container.Images[0]
	useMinigameRuntime := selected.Name == "MinigameQVGAOEM" &&
		actualSHA256 == minigameDAT_SHA256
	requiredMemory := uint64(selected.TextSize) +
		uint64(selected.BSSSize) +
		uint64(DefaultStackSize) +
		uint64(systemSize) +
		uint64(trampolineSize) +
		uint64(guestHeapSize)
	if useMinigameRuntime {
		requiredMemory += uint64(eadsImageHeapSize)
	}
	if requiredMemory > m.memoryLimit {
		return fmt.Errorf(
			"load %q: guest memory %d exceeds limit %d",
			source.Name,
			requiredMemory,
			m.memoryLimit,
		)
	}
	text, err := eads.ExtractText(data, selected)
	if err != nil {
		return fmt.Errorf("extract EADS image: %w", err)
	}

	if err := m.cpu.Map(
		selected.TextBase,
		selected.TextSize,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		return fmt.Errorf("map EADS text: %w", err)
	}
	if err := m.cpu.WriteMemory(selected.TextBase, text); err != nil {
		return fmt.Errorf("copy EADS text: %w", err)
	}
	if err := m.cpu.Map(
		selected.DataBase,
		selected.BSSSize,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		return fmt.Errorf("map EADS BSS: %w", err)
	}
	if err := m.cpu.Map(
		DefaultStackBase,
		DefaultStackSize,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		return fmt.Errorf("map application stack: %w", err)
	}
	if err := mapWIPIRuntimeMemory(m.cpu); err != nil {
		return err
	}
	profileID := source.ProfileID
	carrier := "unknown"
	if profileID == "" {
		if useMinigameRuntime {
			profileID = minigameProfileID
			carrier = "skt"
		} else {
			profileID = DefaultProfileID
		}
	}
	publicRuntime, err := newWIPIRuntimeForProfile(
		m.cpu,
		m.frame,
		profileID,
		carrier,
		32,
		"wipi-c",
	)
	if err != nil {
		return fmt.Errorf("initialize public WIPI runtime: %w", err)
	}
	m.wipi = publicRuntime
	publicRuntime.invokeSync = func(
		callbackContext context.Context,
		callback wipiGuestCallback,
	) (uint32, error) {
		_, value, callbackErr := m.invokeWIPICallback(callbackContext, callback)
		return value, callbackErr
	}
	if err := m.installWIPIResources(); err != nil {
		return err
	}
	if useMinigameRuntime {
		runtime, runtimeErr := newMinigameRuntime(
			m.cpu,
			m.frame,
			publicRuntime,
			selected.DataBase,
			selected.BSSSize,
			selected.GuestEntry(),
		)
		if runtimeErr != nil {
			return fmt.Errorf("initialize MinigameQVGAOEM runtime: %w", runtimeErr)
		}
		m.minigame = runtime
	}

	entry := selected.GuestEntry()
	if err := m.cpu.WriteRegister(cpu.RegisterSP, DefaultStackBase+DefaultStackSize); err != nil {
		return fmt.Errorf("initialize stack pointer: %w", err)
	}
	if err := m.cpu.WriteRegister(cpu.RegisterLR, 0); err != nil {
		return fmt.Errorf("initialize link register: %w", err)
	}
	if err := m.cpu.WriteRegister(cpu.RegisterPC, entry&^uint32(1)); err != nil {
		return fmt.Errorf("initialize entry point: %w", err)
	}
	if err := m.cpu.WriteRegister(cpu.RegisterCPSR, cpu.StatusThumb); err != nil {
		return fmt.Errorf("initialize Thumb execution mode: %w", err)
	}
	initialContext, err := m.cpu.SaveContext()
	if err != nil {
		return fmt.Errorf("capture initial CPU context: %w", err)
	}

	source.ProfileID = profileID
	m.source = source
	m.info = ImageInfo{
		Name:        selected.Name,
		ProfileID:   profileID,
		SourceKind:  loader.KindEADS,
		EntryPoint:  entry,
		Mode:        cpu.ModeThumb,
		TextAddress: selected.TextBase,
		TextSize:    selected.TextSize,
		BSSAddress:  selected.DataBase,
		BSSSize:     selected.BSSSize,
	}
	m.initialText = append([]byte(nil), text...)
	m.initialContext = initialContext
	m.state = machinecore.StateReady
	draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	return nil
}

func (m *Machine) loadRaptor(
	ctx context.Context,
	source machinecore.Source,
	pkg raptor.Package,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	requiredMemory := raptorRequiredMemory(pkg.Image)
	if requiredMemory > m.memoryLimit {
		return fmt.Errorf(
			"load %q: Raptor guest memory %d exceeds limit %d",
			source.Name,
			requiredMemory,
			m.memoryLimit,
		)
	}
	text, bss, err := raptorPrimarySections(pkg.Image)
	if err != nil {
		return fmt.Errorf("load Raptor image: %w", err)
	}
	if err := mapRaptorImage(m.cpu, pkg.Image); err != nil {
		return err
	}
	if err := m.cpu.Map(
		DefaultStackBase,
		DefaultStackSize,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		return fmt.Errorf("map Raptor application stack: %w", err)
	}
	if err := mapWIPIRuntimeMemory(m.cpu); err != nil {
		return err
	}
	profileID := source.ProfileID
	if profileID == "" {
		profileID = raptorProfileID
	}
	publicRuntime, err := newWIPIRuntimeForProfile(
		m.cpu,
		m.frame,
		profileID,
		"lgt",
		16,
		"lgt-raptor",
	)
	if err != nil {
		return fmt.Errorf("initialize public WIPI runtime for Raptor: %w", err)
	}
	m.wipi = publicRuntime
	publicRuntime.invokeSync = func(
		callbackContext context.Context,
		callback wipiGuestCallback,
	) (uint32, error) {
		_, value, callbackErr := m.invokeWIPICallback(callbackContext, callback)
		return value, callbackErr
	}
	m.initialResources = mergeRaptorResources(pkg.Resources, m.initialResources)
	if err := m.installWIPIResources(); err != nil {
		return err
	}
	runtime, err := newRaptorRuntime(m.cpu, publicRuntime, pkg)
	if err != nil {
		return err
	}
	m.raptor = runtime

	for register, value := range map[uint32]uint32{
		cpu.RegisterR0:   raptorKernelBase,
		cpu.RegisterR1:   raptorDletBase,
		cpu.RegisterR2:   raptorWIPICBase,
		cpu.RegisterSP:   DefaultStackBase + DefaultStackSize,
		cpu.RegisterLR:   returnSentinel | 1,
		cpu.RegisterPC:   pkg.Image.Entry,
		cpu.RegisterCPSR: cpu.StatusThumb,
	} {
		if err := m.cpu.WriteRegister(register, value); err != nil {
			return fmt.Errorf("initialize Raptor register %d: %w", register, err)
		}
	}
	initialContext, err := m.cpu.SaveContext()
	if err != nil {
		return fmt.Errorf("capture initial Raptor CPU context: %w", err)
	}
	source.ProfileID = profileID
	m.source = source
	m.info = ImageInfo{
		Name:        pkg.Descriptor.AID,
		ProfileID:   profileID,
		SourceKind:  loader.KindRaptor,
		EntryPoint:  pkg.Image.Entry | 1,
		Mode:        cpu.ModeThumb,
		TextAddress: text.Address,
		TextSize:    text.Size,
		BSSAddress:  bss.Address,
		BSSSize:     bss.Size,
	}
	m.initialText = append([]byte(nil), text.Data...)
	m.initialContext = initialContext
	m.state = machinecore.StateReady
	draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	return nil
}

func (m *Machine) loadKTF(
	ctx context.Context,
	source machinecore.Source,
	pkg ktf.Package,
) error {
	requiredMemory := uint64(len(pkg.Client)) +
		uint64(pkg.BSSSize) +
		uint64(DefaultStackSize) +
		uint64(ktfHostSize) +
		uint64(guestHeapSize)
	if requiredMemory > m.memoryLimit {
		return fmt.Errorf(
			"load %q: KTF guest memory %d exceeds limit %d",
			source.Name,
			requiredMemory,
			m.memoryLimit,
		)
	}
	profileID := source.ProfileID
	if profileID == "" {
		profileID = ktfProfileID
	}
	// KTF descriptors name the handset screen the title was built for. Honour
	// it so a 176x220 title fills its own framebuffer instead of painting into
	// one corner of a larger default and leaving the rest at the clear colour.
	if width, height := pkg.Descriptor.DisplayWidth, pkg.Descriptor.DisplayHeight; width > 0 &&
		height > 0 {
		if bounds := m.frame.Bounds(); bounds.Dx() != width || bounds.Dy() != height {
			m.frame = image.NewRGBA(image.Rect(0, 0, width, height))
		}
	}
	runtime, err := newKTFRuntimeForProfile(
		m.cpu,
		pkg,
		m.frame,
		profileID,
	)
	if err != nil {
		return err
	}
	runtime.deferThreads = true
	if err := runtime.mapImageAndHost(); err != nil {
		return err
	}
	result, executable, err := runtime.bootstrap(ctx)
	if err != nil {
		return fmt.Errorf(
			"bootstrap KTF application at PC 0x%08x after %d instructions: %w",
			result.PC,
			result.Instructions,
			err,
		)
	}
	if err := runtime.initialize(ctx); err != nil {
		return err
	}
	source.ProfileID = profileID
	m.source = source
	m.info = ImageInfo{
		Name:        pkg.Descriptor.AID,
		ProfileID:   profileID,
		SourceKind:  loader.KindKTF,
		EntryPoint:  ktfImageBase | 1,
		Mode:        cpu.ModeThumb,
		TextAddress: ktfImageBase,
		TextSize:    uint32(len(pkg.Client)),
		BSSAddress:  ktfImageBase + uint32(len(pkg.Client)),
		BSSSize:     pkg.BSSSize,
	}
	if runtime.exe.Name != "" {
		m.info.Name = runtime.exe.Name
	}
	if executable == 0 {
		return errors.New("KTF bootstrap returned a null executable")
	}
	m.ktf = runtime
	m.initialText = append([]byte(nil), pkg.Client...)
	m.state = machinecore.StateReady
	draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	return nil
}

func (m *Machine) installWIPIResources() error {
	if m.wipi == nil {
		return nil
	}
	resourceNames := make([]string, 0, len(m.initialResources))
	for name := range m.initialResources {
		resourceNames = append(resourceNames, name)
	}
	sort.Strings(resourceNames)
	for _, name := range resourceNames {
		if result := m.wipi.registerResource(name, m.initialResources[name]); result < 0 {
			return fmt.Errorf("register WIPI resource %q: error %d", name, result)
		}
	}
	return nil
}

func (m *Machine) State() machinecore.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Machine) Start(ctx context.Context) error {
	m.mu.Lock()
	isMinigame := m.minigame != nil
	isKTF := m.ktf != nil
	isRaptor := m.raptor != nil
	m.mu.Unlock()
	if isMinigame {
		return m.RenderFirstFrame(ctx)
	}
	if isKTF {
		return m.runKTFSlice(ctx, 0)
	}
	if isRaptor {
		return m.startRaptor(ctx)
	}
	return m.runSlice(ctx, false)
}

func (m *Machine) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	switch m.state {
	case machinecore.StateRunning:
		if err := m.cpu.Stop(); err != nil {
			return err
		}
		if m.wipi != nil {
			if err := m.wipi.services.Coordinator.Transition(
				m.wipi.serviceOwner,
				shared.LifecyclePaused,
				m.wipi.services.Clock.Monotonic(),
				m.wipi.services.Events,
			); err != nil {
				return err
			}
		}
		if m.ktf != nil {
			if err := m.ktf.services.Coordinator.Transition(
				m.ktf.serviceOwner,
				shared.LifecyclePaused,
				m.ktf.services.Clock.Monotonic(),
				m.ktf.services.Events,
			); err != nil {
				return err
			}
		}
		m.state = machinecore.StatePaused
		return nil
	case machinecore.StatePaused:
		return nil
	default:
		return fmt.Errorf("pause from %s: %w", m.state, ErrInvalidState)
	}
}

func (m *Machine) Resume() error {
	m.mu.Lock()
	isKTF := m.ktf != nil
	isRaptor := m.raptor != nil
	m.mu.Unlock()
	if isKTF {
		return m.runKTFSlice(context.Background(), 0)
	}
	if isRaptor {
		return m.stepRaptorFrame(context.Background())
	}
	return m.runSlice(context.Background(), false)
}

func (m *Machine) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state == machinecore.StateEmpty {
		return fmt.Errorf("stop from %s: %w", m.state, ErrInvalidState)
	}
	if err := m.cpu.Stop(); err != nil {
		return err
	}
	if m.wipi != nil {
		if err := m.wipi.services.Coordinator.Transition(
			m.wipi.serviceOwner,
			shared.LifecycleStopped,
			m.wipi.services.Clock.Monotonic(),
			m.wipi.services.Events,
		); err != nil {
			return err
		}
	}
	if m.ktf != nil {
		if err := m.ktf.services.Coordinator.Transition(
			m.ktf.serviceOwner,
			shared.LifecycleStopped,
			m.ktf.services.Clock.Monotonic(),
			m.ktf.services.Events,
		); err != nil {
			return err
		}
	}
	m.state = machinecore.StateStopped
	return nil
}

func (m *Machine) Reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state == machinecore.StateRunning {
		return fmt.Errorf("reset from %s: %w", m.state, ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.ktf != nil {
		persistence, err := m.ktf.capturePersistentState()
		if err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("capture KTF persistence for reset: %w", err)
		}
		pkg := m.ktf.pkg
		profileID := m.info.ProfileID
		runtime, err := newKTFRuntimeForProfile(
			m.cpu,
			pkg,
			m.frame,
			profileID,
		)
		if err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("reset KTF services: %w", err)
		}
		if err := runtime.restorePersistentState(persistence); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		runtime.deferThreads = true
		if err := runtime.resetMappedMemory(); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		result, executable, err := runtime.bootstrap(ctx)
		if err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf(
				"reset KTF bootstrap at PC 0x%08x after %d instructions: %w",
				result.PC,
				result.Instructions,
				err,
			)
		}
		if executable == 0 {
			m.state = machinecore.StateFaulted
			return errors.New("reset KTF bootstrap returned a null executable")
		}
		if err := runtime.initialize(ctx); err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("reset KTF runtime: %w", err)
		}
		m.ktf = runtime
		m.ktfStarted = false
		m.lastResult = cpu.Result{}
		m.input = nil
		draw.Draw(
			m.frame,
			m.frame.Bounds(),
			image.NewUniform(color.Black),
			image.Point{},
			draw.Src,
		)
		m.state = machinecore.StateReady
		return nil
	}
	if len(m.initialContext) == 0 {
		return fmt.Errorf("reset without initial context: %w", ErrInvalidState)
	}
	var persistence wipiPersistentState
	if m.wipi != nil {
		var err error
		persistence, err = m.wipi.capturePersistentState()
		if err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf(
				"capture public WIPI persistence for reset: %w",
				err,
			)
		}
	}
	if m.raptor != nil {
		if err := m.raptor.restoreImage(); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		if err := zeroGuestMemory(m.cpu, DefaultStackBase, DefaultStackSize); err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("clear Raptor stack: %w", err)
		}
		if err := m.wipi.reset(); err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("reset public WIPI runtime: %w", err)
		}
		if err := m.wipi.restorePersistentState(persistence); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		if err := m.installWIPIResources(); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		if err := m.raptor.installInterfaces(); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		if err := m.cpu.RestoreContext(m.initialContext); err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("restore initial Raptor CPU context: %w", err)
		}
		m.lastResult = cpu.Result{}
		m.input = nil
		draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
		m.state = machinecore.StateReady
		return nil
	}
	if err := m.cpu.WriteMemory(m.info.TextAddress, m.initialText); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("restore initial text: %w", err)
	}
	if err := zeroGuestMemory(m.cpu, m.info.BSSAddress, m.info.BSSSize); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("clear BSS: %w", err)
	}
	if err := zeroGuestMemory(m.cpu, DefaultStackBase, DefaultStackSize); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("clear stack: %w", err)
	}
	if m.wipi != nil {
		if err := m.wipi.reset(); err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("reset public WIPI runtime: %w", err)
		}
		if err := m.wipi.restorePersistentState(persistence); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		if err := m.installWIPIResources(); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
	}
	if m.minigame != nil {
		if err := m.minigame.reset(); err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("reset MinigameQVGAOEM runtime: %w", err)
		}
	}
	if err := m.cpu.RestoreContext(m.initialContext); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("restore initial CPU context: %w", err)
	}
	m.lastResult = cpu.Result{}
	m.input = nil
	draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	m.state = machinecore.StateReady
	return nil
}

func (m *Machine) StepFrame(ctx context.Context) error {
	m.mu.Lock()
	isMinigame := m.minigame != nil
	isKTF := m.ktf != nil
	isRaptor := m.raptor != nil
	m.mu.Unlock()
	if isMinigame {
		return m.stepMinigameFrame(ctx)
	}
	if isKTF {
		return m.runKTFSlice(ctx, ktfFrameDuration)
	}
	if isRaptor {
		return m.stepRaptorFrame(ctx)
	}
	_, stopped, err := m.pumpWIPICallbacks(ctx, wipiFrameDuration)
	if err != nil || stopped {
		return err
	}
	if err := m.runSlice(ctx, true); err != nil {
		return err
	}
	m.mu.Lock()
	pumpPending := m.state == machinecore.StatePaused &&
		len(m.wipi.pendingCallbacks) != 0
	m.mu.Unlock()
	if pumpPending {
		_, _, err = m.pumpWIPICallbacks(ctx, 0)
	}
	return err
}

func (m *Machine) runKTFSlice(ctx context.Context, elapsed time.Duration) error {
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
		return fmt.Errorf("execute KTF application from %s: %w", state, ErrInvalidState)
	}
	if elapsed < 0 {
		m.mu.Unlock()
		return fmt.Errorf("advance KTF clock: negative elapsed time %s", elapsed)
	}
	runtime := m.ktf
	started := m.ktfStarted
	budget := m.runBudget
	if m.ktfRunBudget != 0 {
		budget = m.ktfRunBudget
	}
	if budget < ktfRunBudgetMin {
		budget = ktfRunBudgetMin
	}
	if _, err := runtime.services.Coordinator.BeginQuantum(); err != nil {
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		m.mu.Unlock()
		return err
	}
	if err := runtime.services.Coordinator.Transition(
		runtime.serviceOwner,
		shared.LifecycleRunning,
		runtime.services.Clock.Monotonic(),
		runtime.services.Events,
	); err != nil {
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		m.mu.Unlock()
		return err
	}
	m.state = machinecore.StateRunning
	m.mu.Unlock()

	if !started {
		if err := runtime.startMainClass(ctx); err != nil {
			_ = runtime.services.Coordinator.Fault(
				runtime.serviceOwner,
				err.Error(),
				runtime.services.Clock.Monotonic(),
				runtime.services.Events,
			)
			m.mu.Lock()
			m.state = machinecore.StateFaulted
			m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
			m.mu.Unlock()
			return err
		}
		m.mu.Lock()
		m.ktfStarted = true
		m.mu.Unlock()
	}
	if err := runtime.services.Advance(
		runtime.serviceOwner,
		elapsed,
	); err != nil {
		_ = runtime.services.Coordinator.Fault(
			runtime.serviceOwner,
			err.Error(),
			runtime.services.Clock.Monotonic(),
			runtime.services.Events,
		)
		m.mu.Lock()
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		m.mu.Unlock()
		return err
	}
	runtime.tickMS = uint64(
		runtime.services.Clock.Monotonic() / time.Millisecond,
	)
	if err := m.queueKTFInput(runtime); err != nil {
		_ = runtime.services.Coordinator.Fault(
			runtime.serviceOwner,
			err.Error(),
			runtime.services.Clock.Monotonic(),
			runtime.services.Events,
		)
		m.mu.Lock()
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		m.mu.Unlock()
		return err
	}
	result := cpu.Result{Reason: cpu.StopBudget}
	var instructions uint64
	var consumeErr error
	runtime.paintStalled = false
taskLoop:
	for slices := 0; slices < ktfTaskSlicesPerQuantumMax &&
		instructions < budget; slices++ {
		remaining := budget - instructions
		presentations := runtime.presentCount
		sliceResult := runtime.runTaskSlice(ctx, remaining)
		if sliceResult.Instructions > remaining {
			sliceResult = cpu.Result{
				Reason:       cpu.StopFault,
				PC:           sliceResult.PC,
				Instructions: remaining,
				Err: fmt.Errorf(
					"KTF task exceeded quantum budget: used %d, remaining %d",
					sliceResult.Instructions,
					remaining,
				),
			}
		}
		instructions += sliceResult.Instructions
		if err := runtime.services.Coordinator.Consume(
			runtime.serviceOwner,
			sliceResult.Instructions,
		); err != nil {
			consumeErr = err
			result = cpu.Result{
				Reason:       cpu.StopFault,
				PC:           sliceResult.PC,
				Instructions: instructions,
				Err:          err,
			}
			break
		}
		sliceResult.Instructions = instructions
		result = sliceResult
		if sliceResult.Err != nil {
			break
		}
		if runtime.presentCount != presentations {
			// StepFrame is a presentation quantum. Once the guest submits a
			// frame, return it to the frontend instead of allowing an
			// uncapped paint loop to render many invisible intermediate
			// frames in one host update.
			break
		}
		if runtime.paintStalled {
			// The card the guest keeps asking to repaint is waiting on a
			// paint task that is inside Thread.sleep, and the virtual clock
			// does not move until this quantum ends. Spending the remaining
			// slices re-running the guest's event loop cannot produce a
			// frame, so return the quantum and let time advance instead.
			break
		}
		switch sliceResult.Reason {
		case cpu.StopBudget:
			// A Java task can yield or return before consuming the CPU
			// quantum. Continue with the next runnable task without advancing
			// virtual time, matching the handset's cooperative scheduler.
			continue
		default:
			break taskLoop
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastResult = result
	if consumeErr != nil {
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{
			Reason: cpu.StopFault,
			PC:     result.PC, Instructions: result.Instructions, Err: consumeErr,
		}
		_ = runtime.services.Coordinator.Fault(
			runtime.serviceOwner,
			consumeErr.Error(),
			runtime.services.Clock.Monotonic(),
			runtime.services.Events,
		)
		return consumeErr
	}
	switch result.Reason {
	case cpu.StopBudget, cpu.StopBreakpoint:
		m.state = machinecore.StatePaused
	case cpu.StopExited:
		if runtime.canAwaitEvents() {
			m.state = machinecore.StatePaused
		} else {
			m.state = machinecore.StateStopped
		}
	case cpu.StopRequested:
		m.state = machinecore.StatePaused
	default:
		m.state = machinecore.StateFaulted
	}
	if m.state == machinecore.StateFaulted {
		message := "KTF guest execution fault"
		if result.Err != nil {
			message = result.Err.Error()
		}
		_ = runtime.services.Coordinator.Fault(
			runtime.serviceOwner,
			message,
			runtime.services.Clock.Monotonic(),
			runtime.services.Events,
		)
	} else {
		target := shared.LifecyclePaused
		if m.state == machinecore.StateStopped {
			target = shared.LifecycleStopped
		}
		if err := runtime.services.Coordinator.Transition(
			runtime.serviceOwner,
			target,
			runtime.services.Clock.Monotonic(),
			runtime.services.Events,
		); err != nil {
			m.state = machinecore.StateFaulted
			m.lastResult.Err = err
			return err
		}
	}
	if result.Err != nil && !errors.Is(result.Err, cpu.ErrStopped) {
		return fmt.Errorf("execute KTF guest at 0x%08x: %w", result.PC, result.Err)
	}
	return nil
}

func (m *Machine) queueKTFInput(runtime *ktfRuntime) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Duration(runtime.tickMS) * time.Millisecond
	for index, event := range m.input {
		if event.At > now {
			continue
		}
		if _, known := inputKeyCode(event.Control); known &&
			(runtime.displayCards[runtime.defaultDisplay] == 0 ||
				!runtime.hasJavaTaskCapacity()) {
			break
		}
		if err := runtime.services.QueueInput(
			runtime.serviceOwner,
			event.Control,
			event.Pressed,
			event.At,
		); err != nil {
			return fmt.Errorf("queue shared KTF input %q: %w", event.Control, err)
		}
		m.input = append(m.input[:index], m.input[index+1:]...)
		break
	}
	return runtime.drainServiceEvents(now)
}

func (m *Machine) QueueInput(event machinecore.InputEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if len(m.input) >= 1024 {
		return fmt.Errorf("input queue is full")
	}
	index := sort.Search(len(m.input), func(index int) bool {
		return m.input[index].At > event.At
	})
	m.input = append(m.input, machinecore.InputEvent{})
	copy(m.input[index+1:], m.input[index:])
	m.input[index] = event
	return nil
}

func (m *Machine) Framebuffer() image.Image {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ktf != nil && m.ktf.services != nil {
		// KTF Java paint callbacks draw cooperatively across several host
		// quanta. Exposing their shared working buffer here lets frontends see
		// partially cleared or half-drawn screens before recordPresentation
		// commits them. The graphics service keeps an immutable copy of the
		// most recently submitted frame for exactly this boundary.
		presented := m.ktf.services.Graphics.LastFrame()
		if presented.Sequence != 0 {
			return presented.Image()
		}
		blank := image.NewRGBA(m.frame.Bounds())
		draw.Draw(
			blank,
			blank.Bounds(),
			image.NewUniform(color.Black),
			image.Point{},
			draw.Src,
		)
		return blank
	}
	snapshot := image.NewRGBA(m.frame.Bounds())
	copy(snapshot.Pix, m.frame.Pix)
	return snapshot
}

func (m *Machine) DrainAudio() machinecore.AudioChunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	var audio shared.AudioBuffer
	switch {
	case m.ktf != nil && m.ktf.services != nil:
		audio = m.ktf.services.Media.Drain()
	case m.wipi != nil && m.wipi.services != nil:
		audio = m.wipi.services.Media.Drain()
	default:
		return machinecore.AudioChunk{}
	}
	return machinecore.AudioChunk{
		SampleRate: audio.SampleRate,
		Channels:   audio.Channels,
		PCM16:      audio.PCM16,
	}
}

func (m *Machine) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	running := m.state == machinecore.StateRunning
	m.mu.Unlock()

	if running {
		_ = m.cpu.Stop()
	}
	err := m.cpu.Close()

	m.mu.Lock()
	if m.wipi != nil {
		if adapter, adapterErr := m.wipi.services.Coordinator.Adapter(
			m.wipi.serviceOwner,
		); adapterErr == nil && adapter.Lifecycle != shared.LifecycleDestroyed {
			_ = m.wipi.services.Coordinator.Transition(
				m.wipi.serviceOwner,
				shared.LifecycleDestroyed,
				m.wipi.services.Clock.Monotonic(),
				nil,
			)
		}
	}
	if m.ktf != nil {
		if adapter, adapterErr := m.ktf.services.Coordinator.Adapter(
			m.ktf.serviceOwner,
		); adapterErr == nil && adapter.Lifecycle != shared.LifecycleDestroyed {
			_ = m.ktf.services.Coordinator.Transition(
				m.ktf.serviceOwner,
				shared.LifecycleDestroyed,
				m.ktf.services.Clock.Monotonic(),
				nil,
			)
		}
	}
	m.state = machinecore.StateStopped
	m.mu.Unlock()
	return err
}

func (m *Machine) ImageInfo() ImageInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.info
}

func (m *Machine) LastResult() cpu.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastResult
}

// EADSFrameStats returns a copy of the recovered OEM lifecycle trace. The
// boolean is false for applications that do not use the MinigameQVGAOEM
// service profile.
func (m *Machine) EADSFrameStats() (EADSFrameStats, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.minigame == nil || m.state == machinecore.StateRunning {
		return EADSFrameStats{}, false
	}
	stats := m.minigame.stats
	stats.Events = append([]EADSEventResult(nil), stats.Events...)
	return stats, true
}

// WIPIFrameStats returns standard public WIPI-C API and presentation activity.
func (m *Machine) WIPIFrameStats() (WIPIFrameStats, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ktf != nil && m.state != machinecore.StateRunning {
		calls := uint64(len(m.ktf.hostTrace))
		var unimplemented uint64
		for _, count := range m.ktf.unimplementedJava {
			unimplemented += count
		}
		implemented := calls
		if unimplemented < implemented {
			implemented -= unimplemented
		} else {
			implemented = 0
		}
		return WIPIFrameStats{
			PresentCount:       m.ktf.presentCount,
			APICalls:           calls,
			ImplementedCalls:   implemented,
			UnimplementedCalls: unimplemented,
			LastAPI:            m.ktf.lastJavaMethod,
			LastUnimplemented:  m.ktf.lastUnimplementedJava,
		}, true
	}
	if m.wipi == nil || m.state == machinecore.StateRunning {
		return WIPIFrameStats{}, false
	}
	return m.wipi.stats, true
}

// WIPIAPICoverage reports the recovered ABI size, semantically modeled subset,
// and selectors observed in this machine.
func (m *Machine) WIPIAPICoverage() (WIPIAPICoverage, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ktf != nil && m.state != machinecore.StateRunning {
		return WIPIAPICoverage{}, true
	}
	if m.wipi == nil || m.state == machinecore.StateRunning {
		return WIPIAPICoverage{}, false
	}
	return m.wipi.coverage(), true
}

// WIPIUnimplementedAPIs returns the sorted selectors actually reached without
// a semantic implementation. Catalog presence alone never counts as support.
func (m *Machine) WIPIUnimplementedAPIs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ktf != nil {
		names := make([]string, 0, len(m.ktf.unimplementedJava))
		for name := range m.ktf.unimplementedJava {
			names = append(names, name)
		}
		sort.Strings(names)
		return names
	}
	if m.wipi == nil {
		return nil
	}
	return m.wipi.unimplementedNames()
}

// RenderFirstFrame executes the recovered bootstrap, setup, start, preload,
// and first visible frame event sequence for MinigameQVGAOEM.
func (m *Machine) RenderFirstFrame(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return cpu.ErrClosed
	}
	if m.minigame == nil {
		m.mu.Unlock()
		return fmt.Errorf("render first frame: title runtime is unavailable")
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused:
	default:
		state := m.state
		m.mu.Unlock()
		return fmt.Errorf("execute from %s: %w", state, ErrInvalidState)
	}
	runtime := m.minigame
	m.state = machinecore.StateRunning
	if m.wipi != nil {
		if err := m.wipi.beginServiceExecution(); err != nil {
			m.state = machinecore.StateFaulted
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Unlock()

	err := runtime.renderFirstFrame(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		if errors.Is(err, cpu.ErrStopped) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			m.state = machinecore.StatePaused
			m.lastResult = cpu.Result{
				Reason: cpu.StopRequested,
				Err:    err,
			}
		} else {
			m.state = machinecore.StateFaulted
			m.lastResult = cpu.Result{
				Reason: cpu.StopFault,
				Err:    err,
			}
		}
		if m.wipi != nil {
			_ = m.wipi.finishServiceExecution(
				m.state,
				m.lastResult.Instructions,
				err.Error(),
			)
		}
		return err
	}
	last := runtime.stats.Events[len(runtime.stats.Events)-1]
	runtime.syncFrame()
	m.lastResult = cpu.Result{
		Reason:       cpu.StopBreakpoint,
		Instructions: last.Instructions,
		PC:           returnSentinel,
	}
	m.state = machinecore.StatePaused
	if m.wipi != nil {
		if err := m.wipi.finishServiceExecution(
			m.state,
			m.lastResult.Instructions,
			"",
		); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
	}
	return nil
}

func (m *Machine) stepMinigameFrame(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return cpu.ErrClosed
	}
	if m.minigame == nil {
		m.mu.Unlock()
		return fmt.Errorf("step EADS frame: title runtime is unavailable")
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused:
	default:
		state := m.state
		m.mu.Unlock()
		return fmt.Errorf("execute from %s: %w", state, ErrInvalidState)
	}
	runtime := m.minigame
	m.state = machinecore.StateRunning
	if m.wipi != nil {
		if err := m.wipi.beginServiceExecution(); err != nil {
			m.state = machinecore.StateFaulted
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Unlock()

	result, err := runtime.stepFrame(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		if errors.Is(err, cpu.ErrStopped) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			m.state = machinecore.StatePaused
			m.lastResult = cpu.Result{Reason: cpu.StopRequested, Err: err}
		} else {
			m.state = machinecore.StateFaulted
			m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		}
		if m.wipi != nil {
			_ = m.wipi.finishServiceExecution(
				m.state,
				m.lastResult.Instructions,
				err.Error(),
			)
		}
		return err
	}
	runtime.syncFrame()
	m.lastResult = cpu.Result{
		Reason:       cpu.StopBreakpoint,
		Instructions: result.Instructions,
		PC:           returnSentinel,
	}
	m.state = machinecore.StatePaused
	if m.wipi != nil {
		if err := m.wipi.finishServiceExecution(
			m.state,
			m.lastResult.Instructions,
			"",
		); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
	}
	return nil
}

func (m *Machine) ReadRegister(id uint32) (uint32, error) {
	return m.cpu.ReadRegister(id)
}

func (m *Machine) runSlice(ctx context.Context, frame bool) error {
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
		return fmt.Errorf("execute from %s: %w", state, ErrInvalidState)
	}
	pc, err := m.cpu.ReadRegister(cpu.RegisterPC)
	if err != nil {
		m.state = machinecore.StateFaulted
		m.mu.Unlock()
		return err
	}
	cpsr, err := m.cpu.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		m.state = machinecore.StateFaulted
		m.mu.Unlock()
		return err
	}
	mode := cpu.ModeARM
	if cpsr&cpu.StatusThumb != 0 {
		mode = cpu.ModeThumb
	}
	budget := m.runBudget
	if frame {
		budget = m.frameRunBudget
	}
	m.state = machinecore.StateRunning
	if m.wipi != nil {
		if err := m.wipi.beginServiceExecution(); err != nil {
			m.state = machinecore.StateFaulted
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Unlock()

	result := m.runWIPISlice(ctx, pc, mode, budget, true)

	m.mu.Lock()
	defer m.mu.Unlock()
	requestedState := m.state
	if result.Err == nil && result.PC == 0 {
		result.Reason = cpu.StopExited
	}
	m.lastResult = result
	switch result.Reason {
	case cpu.StopBudget, cpu.StopBreakpoint:
		m.state = machinecore.StatePaused
	case cpu.StopExited:
		m.state = machinecore.StateStopped
	case cpu.StopRequested:
		switch {
		case m.closed || requestedState == machinecore.StateStopped:
			m.state = machinecore.StateStopped
		case requestedState == machinecore.StatePaused:
			m.state = machinecore.StatePaused
		case errors.Is(result.Err, cpu.ErrStopped):
			m.state = machinecore.StateStopped
		default:
			m.state = machinecore.StatePaused
		}
	case cpu.StopFault:
		m.state = machinecore.StateFaulted
	default:
		m.state = machinecore.StateFaulted
	}
	if m.wipi != nil {
		fault := ""
		if result.Err != nil {
			fault = result.Err.Error()
		}
		if err := m.wipi.finishServiceExecution(
			m.state,
			result.Instructions,
			fault,
		); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
	}
	if result.Err != nil && !errors.Is(result.Err, cpu.ErrStopped) {
		return fmt.Errorf("execute guest at 0x%08x: %w", result.PC, result.Err)
	}
	return nil
}

func (m *Machine) runWIPISlice(
	ctx context.Context,
	pc uint32,
	mode cpu.Mode,
	budget uint64,
	stopOnPresent bool,
) cpu.Result {
	var instructions uint64
	presentations := uint32(0)
	stopOnPresent = stopOnPresent && m.wipi != nil
	if stopOnPresent {
		presentations = m.wipi.stats.PresentCount
	}
	for instructions < budget {
		run := m.cpu.Run(ctx, pc, mode, budget-instructions)
		instructions += run.Instructions
		run.Instructions = instructions
		if run.Err != nil || run.Reason != cpu.StopBreakpoint || m.wipi == nil {
			return run
		}
		if run.PC < 2 {
			return run
		}
		trap := run.PC - 2
		var handled bool
		var err error
		if m.raptor != nil {
			handled, err = m.raptor.dispatchTrap(ctx, trap)
		}
		if err == nil && !handled {
			handled, err = m.wipi.dispatchTrap(ctx, trap)
		}
		if err != nil {
			run.Reason = cpu.StopFault
			run.Err = err
			return run
		}
		if !handled {
			return run
		}
		if m.wipi.exitRequested {
			run.Reason = cpu.StopExited
			run.PC = 0
			return run
		}
		nextPC, err := m.cpu.ReadRegister(cpu.RegisterPC)
		if err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           run.PC,
				Err:          err,
			}
		}
		if stopOnPresent && m.wipi.stats.PresentCount != presentations {
			// A frontend frame is a presentation quantum. Yield immediately
			// after the guest submits visible output instead of running the
			// remainder of the handset budget and hiding intermediate frames.
			return cpu.Result{
				Reason:       cpu.StopBudget,
				Instructions: instructions,
				PC:           nextPC,
			}
		}
		if instructions >= budget {
			return cpu.Result{
				Reason:       cpu.StopBudget,
				Instructions: instructions,
				PC:           nextPC,
			}
		}
		cpsr, err := m.cpu.ReadRegister(cpu.RegisterCPSR)
		if err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           nextPC,
				Err:          err,
			}
		}
		mode = cpu.ModeARM
		if cpsr&cpu.StatusThumb != 0 {
			mode = cpu.ModeThumb
		}
		pc = nextPC
	}
	return cpu.Result{
		Reason:       cpu.StopBudget,
		Instructions: instructions,
		PC:           pc,
	}
}

const (
	wipiCallbackInstructionLimit   = uint64(2_000_000)
	raptorCallbackInstructionLimit = uint64(64_000_000)
)

type wipiGuestCallback struct {
	procedure uint32
	args      [4]uint32
}

func (m *Machine) pumpWIPICallbacks(
	ctx context.Context,
	elapsed time.Duration,
) (cpu.Result, bool, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return cpu.Result{}, false, cpu.ErrClosed
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused:
	default:
		state := m.state
		m.mu.Unlock()
		return cpu.Result{}, false, fmt.Errorf(
			"pump WIPI callbacks from %s: %w",
			state,
			ErrInvalidState,
		)
	}
	previousState := m.state
	m.state = machinecore.StateRunning
	if err := m.wipi.beginServiceExecution(); err != nil {
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		m.mu.Unlock()
		return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
	}
	callbacks := append([]wipiGuestCallback(nil), m.wipi.pendingCallbacks...)
	m.wipi.pendingCallbacks = nil
	target := m.wipi.services.Clock.Monotonic() + elapsed
	pendingInput := m.input[:0]
	for _, event := range m.input {
		if event.At > target {
			pendingInput = append(pendingInput, event)
			continue
		}
		if err := m.wipi.services.QueueInput(
			m.wipi.serviceOwner,
			event.Control,
			event.Pressed,
			event.At,
		); err != nil {
			m.state = machinecore.StateFaulted
			m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
			_ = m.wipi.finishServiceExecution(
				m.state,
				0,
				err.Error(),
			)
			m.mu.Unlock()
			return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
		}
	}
	m.input = pendingInput
	if err := m.wipi.services.Advance(
		m.wipi.serviceOwner,
		elapsed,
	); err != nil {
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		_ = m.wipi.finishServiceExecution(m.state, 0, err.Error())
		m.mu.Unlock()
		return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
	}
	m.wipi.tickMS = uint64(m.wipi.services.Clock.Monotonic() / time.Millisecond)
	for {
		event, ready := m.wipi.services.Events.PopReady(
			m.wipi.services.Clock.Monotonic(),
		)
		if !ready {
			break
		}
		switch event.Kind {
		case shared.EventInputPress, shared.EventInputRelease, shared.EventInputRepeat:
			if m.raptor == nil || !m.raptor.started ||
				m.raptor.clet.HandleEvent == 0 {
				continue
			}
			callback, ok := raptorInputCallback(
				m.raptor.clet.HandleEvent,
				machinecore.InputEvent{
					Control: event.Control,
					Pressed: event.Kind != shared.EventInputRelease,
					At:      event.At,
				},
			)
			if ok {
				callbacks = append(callbacks, callback)
			}
		case shared.EventTimer:
			address := uint32(event.Value)
			timer, active := m.wipi.timers[address]
			if !active || m.wipi.timerServices[address] != event.ServiceID {
				continue
			}
			delete(m.wipi.timers, address)
			// Public WIPI timers expose an active field at +24. LGT Raptor's
			// MCTimer is only a callback word, so adjacent application memory
			// must remain untouched when the timer fires.
			if address != 0 && m.raptor == nil {
				if err := m.wipi.writeU32(address+24, 0); err != nil {
					m.state = machinecore.StateFaulted
					m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
					_ = m.wipi.finishServiceExecution(
						m.state,
						0,
						err.Error(),
					)
					m.mu.Unlock()
					return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
				}
			}
			if timer.callback != 0 {
				callbacks = append(callbacks, wipiGuestCallback{
					procedure: timer.callback,
					args:      [4]uint32{address, timer.parameter},
				})
			}
		case shared.EventAudioComplete:
			for handle, serviceID := range m.wipi.mediaServices {
				if serviceID != event.ServiceID {
					continue
				}
				if clip := m.wipi.mediaClips[handle]; clip != nil {
					clip.state = 0
					clip.repeat = false
					m.wipi.enqueueCallback(clip.callback, handle, 0)
				}
				break
			}
		}
	}
	callbacks = append(callbacks, m.wipi.pendingCallbacks...)
	m.wipi.pendingCallbacks = nil
	m.mu.Unlock()

	var callbackResult cpu.Result
	var callbackErr error
	for _, callback := range callbacks {
		result, _, err := m.invokeWIPICallback(ctx, callback)
		callbackResult.Instructions += result.Instructions
		callbackResult.PC = result.PC
		callbackResult.Reason = result.Reason
		callbackResult.Err = result.Err
		callbackErr = err
		if callbackErr != nil || result.Reason == cpu.StopExited {
			break
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if callbackErr != nil {
		m.lastResult = callbackResult
		if errors.Is(callbackErr, cpu.ErrStopped) ||
			errors.Is(callbackErr, context.Canceled) ||
			errors.Is(callbackErr, context.DeadlineExceeded) {
			m.state = machinecore.StatePaused
		} else {
			m.state = machinecore.StateFaulted
		}
		if serviceErr := m.wipi.finishServiceExecution(
			m.state,
			callbackResult.Instructions,
			callbackErr.Error(),
		); serviceErr != nil && m.state != machinecore.StateFaulted {
			m.state = machinecore.StateFaulted
			return callbackResult, false, serviceErr
		}
		return callbackResult, false, callbackErr
	}
	if callbackResult.Reason == cpu.StopExited {
		m.lastResult = callbackResult
		m.state = machinecore.StateStopped
		if err := m.wipi.finishServiceExecution(
			m.state,
			callbackResult.Instructions,
			"",
		); err != nil {
			m.state = machinecore.StateFaulted
			return callbackResult, false, err
		}
		return callbackResult, true, nil
	}
	if m.closed {
		m.state = machinecore.StateStopped
		_ = m.wipi.finishServiceExecution(
			m.state,
			callbackResult.Instructions,
			"",
		)
		return callbackResult, true, cpu.ErrClosed
	}
	if m.state == machinecore.StateRunning {
		m.state = previousState
	}
	if err := m.wipi.finishServiceExecution(
		m.state,
		callbackResult.Instructions,
		"",
	); err != nil {
		m.state = machinecore.StateFaulted
		return callbackResult, false, err
	}
	return callbackResult, m.state == machinecore.StateStopped, nil
}

func (m *Machine) invokeWIPICallback(
	ctx context.Context,
	callback wipiGuestCallback,
) (result cpu.Result, returnValue uint32, returnedErr error) {
	savedContext, err := m.cpu.SaveContext()
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	defer func() {
		if restoreErr := m.cpu.RestoreContext(savedContext); restoreErr != nil && returnedErr == nil {
			result = cpu.Result{Reason: cpu.StopFault, Err: restoreErr}
			returnValue = 0
			returnedErr = restoreErr
		}
	}()

	for register := cpu.RegisterR0; register <= cpu.RegisterR3; register++ {
		if err := m.cpu.WriteRegister(register, callback.args[register]); err != nil {
			return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
		}
	}
	if err := m.cpu.WriteRegister(cpu.RegisterLR, returnSentinel|1); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	if err := m.cpu.WriteRegister(cpu.RegisterPC, callback.procedure&^1); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	cpsr, err := m.cpu.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	mode := cpu.ModeARM
	if callback.procedure&1 != 0 {
		cpsr |= cpu.StatusThumb
		mode = cpu.ModeThumb
	} else {
		cpsr &^= cpu.StatusThumb
	}
	if err := m.cpu.WriteRegister(cpu.RegisterCPSR, cpsr); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	instructionLimit := wipiCallbackInstructionLimit
	if m.raptor != nil {
		instructionLimit = raptorCallbackInstructionLimit
	}
	result = m.runWIPISlice(
		ctx,
		callback.procedure&^1,
		mode,
		instructionLimit,
		false,
	)
	if result.Err != nil {
		return result, 0, result.Err
	}
	if result.Reason == cpu.StopExited {
		return result, 0, nil
	}
	if result.Reason != cpu.StopBreakpoint || result.PC < 2 ||
		result.PC-2 != returnSentinel {
		err := fmt.Errorf(
			"WIPI callback 0x%08x did not return within %d instructions (stop %d at 0x%08x)",
			callback.procedure,
			instructionLimit,
			result.Reason,
			result.PC,
		)
		result.Reason = cpu.StopFault
		result.Err = err
		return result, 0, err
	}
	returnValue, err = m.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		result.Reason = cpu.StopFault
		result.Err = err
		return result, 0, err
	}
	return result, returnValue, nil
}

var _ machinecore.Factory = Factory{}
var _ machinecore.Machine = (*Machine)(nil)

func zeroGuestMemory(backend cpu.Backend, address, size uint32) error {
	zeros := make([]byte, min(uint32(64<<10), size))
	var offset uint32
	for offset < size {
		count := min(uint32(len(zeros)), size-offset)
		if err := backend.WriteMemory(address+offset, zeros[:count]); err != nil {
			return err
		}
		offset += count
	}
	return nil
}
