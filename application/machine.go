// Package application implements ARAM's WIPI native-application machine.
package application

import (
	"context"
	"errors"
	"fmt"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/netauth"
	"image"
	"image/color"
	"image/draw"
	"sort"
	"sync"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/application/internal/minigame"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	DefaultProfileID = guest.DefaultProfileID
	DefaultStackBase = guest.DefaultStackBase
	DefaultStackSize = guest.DefaultStackSize
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
	// KTF Java applications need enough instructions per host video quantum
	// for their cooperative game and paint threads to make visible progress.
	// A 1K slice leaves several real titles in initialization indefinitely at
	// ordinary 60 Hz frontend scheduling.
	ktfTaskSlicesPerQuantumMax = 64
	// guest.WIPIFrameDuration is the guest time one native-WIPI, Raptor, or EADS
	// presentation quantum advances.
)

var (
	ErrUnsupportedSource = errors.New("unsupported WIPI application source")
	ErrInvalidState      = guest.ErrInvalidState
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
	// RaptorNet, when set, services the LGT carrier network/DRM ordinals
	// (106/238) for raptor titles. The composition root (aram-emu) injects an
	// aram-authd backend; leaving it nil keeps the default behavior.
	RaptorNet netauth.Backend
	// FallbackFont selects the embedded handset bitmap font used to render
	// guest text that has no glyphs of its own. Empty inherits the runtime
	// default (galmuri9); "neodgm" selects the softer NeoDunggeunmo look.
	FallbackFont string
}

func NewFactory() Factory {
	return Factory{
		NewCPU:      selectedCPUFactory(),
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
		initialResources: guest.CloneSliceMap(f.Resources),
		frameRunBudget:   frameBudget,
		raptorNet:        f.RaptorNet,
		fallbackFont:     f.FallbackFont,
	}
	if err := machine.Load(ctx, source); err != nil {
		_ = backend.Close()
		return nil, err
	}
	return machine, nil
}

type ImageInfo struct {
	Name       string
	ProfileID  string
	SourceKind loader.Kind
	// ImageSHA256 identifies the loaded executable image rather than the file
	// that delivered it, so it survives re-archiving and repackaging. Cheats
	// and other hash-keyed data bind to it.
	ImageSHA256 string
	EntryPoint  uint32
	Mode        cpu.Mode
	TextAddress uint32
	TextSize    uint32
	BSSAddress  uint32
	BSSSize     uint32
	// CPUBackend is the identity name of the selected CPU backend (see
	// ARAM_CPU / cpu_select.go). It surfaces which core is executing the guest
	// so a swap is observable end-to-end through the product.
	CPUBackend string
}

type Machine struct {
	mu               sync.Mutex
	cpu              cpu.Backend
	wipi             *wipirt.Runtime
	minigame         *minigame.Runtime
	ktf              *ktfrt.Runtime
	raptor           *raptorrt.Runtime
	raptorNet        netauth.Backend
	fallbackFont     string
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
	presentation     framePresentationCache
	input            []machinecore.InputEvent
	closed           bool
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
			if err := m.wipi.Services.Coordinator.Transition(
				m.wipi.ServiceOwner,
				shared.LifecyclePaused,
				m.wipi.Services.Clock.Monotonic(),
				m.wipi.Services.Events,
			); err != nil {
				return err
			}
		}
		if m.ktf != nil {
			if err := m.ktf.Services.Coordinator.Transition(
				m.ktf.ServiceOwner,
				shared.LifecyclePaused,
				m.ktf.Services.Clock.Monotonic(),
				m.ktf.Services.Events,
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
		if err := m.wipi.Services.Coordinator.Transition(
			m.wipi.ServiceOwner,
			shared.LifecycleStopped,
			m.wipi.Services.Clock.Monotonic(),
			m.wipi.Services.Events,
		); err != nil {
			return err
		}
	}
	if m.ktf != nil {
		if err := m.ktf.Services.Coordinator.Transition(
			m.ktf.ServiceOwner,
			shared.LifecycleStopped,
			m.ktf.Services.Clock.Monotonic(),
			m.ktf.Services.Events,
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
		persistence, err := m.ktf.CapturePersistentState()
		if err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("capture KTF persistence for reset: %w", err)
		}
		pkg := m.ktf.Pkg
		profileID := m.info.ProfileID
		runtime, err := ktfrt.NewRuntimeForProfile(
			m.cpu,
			pkg,
			m.frame,
			profileID,
			m.fallbackFont,
		)
		if err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("reset KTF services: %w", err)
		}
		if err := runtime.RestorePersistentState(persistence); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		runtime.DeferThreads = true
		if err := runtime.ResetMappedMemory(); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		result, executable, err := runtime.Bootstrap(ctx)
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
		if err := runtime.Initialize(ctx); err != nil {
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
	var persistence wipirt.PersistentState
	if m.wipi != nil {
		var err error
		persistence, err = m.wipi.CapturePersistentState()
		if err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf(
				"capture public WIPI persistence for reset: %w",
				err,
			)
		}
	}
	if m.raptor != nil {
		if err := m.raptor.RestoreImage(); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		if err := guest.ZeroMemory(m.cpu, DefaultStackBase, DefaultStackSize); err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("clear Raptor stack: %w", err)
		}
		if err := m.wipi.Reset(); err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("reset public WIPI runtime: %w", err)
		}
		if err := m.wipi.RestorePersistentState(persistence); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		if err := m.installWIPIResources(); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		if err := m.raptor.InstallInterfaces(); err != nil {
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
	if err := guest.ZeroMemory(m.cpu, m.info.BSSAddress, m.info.BSSSize); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("clear BSS: %w", err)
	}
	if err := guest.ZeroMemory(m.cpu, DefaultStackBase, DefaultStackSize); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("clear stack: %w", err)
	}
	if m.wipi != nil {
		if err := m.wipi.Reset(); err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("reset public WIPI runtime: %w", err)
		}
		if err := m.wipi.RestorePersistentState(persistence); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		if err := m.installWIPIResources(); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
	}
	if m.minigame != nil {
		if err := m.minigame.Reset(); err != nil {
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
		return m.runKTFSlice(ctx, ktfrt.FrameDuration)
	}
	if isRaptor {
		return m.stepRaptorFrame(ctx)
	}
	_, stopped, err := m.pumpWIPICallbacks(ctx, guest.WIPIFrameDuration)
	if err != nil || stopped {
		return err
	}
	if err := m.runSlice(ctx, true); err != nil {
		return err
	}
	m.mu.Lock()
	pumpPending := m.state == machinecore.StatePaused &&
		len(m.wipi.PendingCallbacks) != 0
	m.mu.Unlock()
	if pumpPending {
		_, _, err = m.pumpWIPICallbacks(ctx, 0)
	}
	return err
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
	if m.ktf != nil && m.ktf.Services != nil {
		// KTF Java paint callbacks draw cooperatively across several host
		// quanta. Exposing their shared working buffer here lets frontends see
		// partially cleared or half-drawn screens before recordPresentation
		// commits them. The graphics service keeps an immutable copy of the
		// most recently submitted frame for exactly this boundary.
		presented := m.ktf.Services.Graphics.LastFrame()
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
	case m.ktf != nil && m.ktf.Services != nil:
		audio = m.ktf.Services.Media.Drain()
	case m.wipi != nil && m.wipi.Services != nil:
		audio = m.wipi.Services.Media.Drain()
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
	var javaErr error
	if m.raptor != nil {
		javaErr = m.raptor.DestroyRaptorJava()
	}
	err := errors.Join(m.cpu.Close(), javaErr)

	m.mu.Lock()
	if m.wipi != nil {
		if adapter, adapterErr := m.wipi.Services.Coordinator.Adapter(
			m.wipi.ServiceOwner,
		); adapterErr == nil && adapter.Lifecycle != shared.LifecycleDestroyed {
			_ = m.wipi.Services.Coordinator.Transition(
				m.wipi.ServiceOwner,
				shared.LifecycleDestroyed,
				m.wipi.Services.Clock.Monotonic(),
				nil,
			)
		}
	}
	if m.ktf != nil {
		if adapter, adapterErr := m.ktf.Services.Coordinator.Adapter(
			m.ktf.ServiceOwner,
		); adapterErr == nil && adapter.Lifecycle != shared.LifecycleDestroyed {
			_ = m.ktf.Services.Coordinator.Transition(
				m.ktf.ServiceOwner,
				shared.LifecycleDestroyed,
				m.ktf.Services.Clock.Monotonic(),
				nil,
			)
		}
	}
	m.state = machinecore.StateStopped
	m.mu.Unlock()
	return err
}

var _ machinecore.Factory = Factory{}
var _ machinecore.Machine = (*Machine)(nil)
