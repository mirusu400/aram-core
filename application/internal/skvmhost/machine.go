package skvmhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
	"github.com/mirusu400/aram-core/profile"
	shared "github.com/mirusu400/aram-core/runtime"
	skengine "github.com/mirusu400/aram-core/skvm"
	"image"
	"sort"
	"sync"
	"time"
)

const (
	ProfileID                = "wipi-1.2.1/skt/generic"
	skvmMachineStateMagic    = "ARAMSKM\x00"
	skvmMachineStateVersion  = uint32(1)
	maxSKVMMachineStateBytes = uint64(1024 << 20)
	maxSKVMPendingInputs     = 1024
	defaultSKVMRunBudget     = uint64(10_000_000)
)

type Machine struct {
	mu           sync.Mutex
	state        machinecore.State
	source       machinecore.Source
	mainClass    string
	classData    map[string][]byte
	vm           *skengine.VM
	services     *shared.Services
	owner        shared.OwnerID
	started      bool
	midlet       uint32
	input        []machinecore.InputEvent
	initialState []byte
	frameQuantum time.Duration
	closed       bool
}

func New(
	ctx context.Context,
	source machinecore.Source,
	pkg skloader.Package,
	framebufferSize image.Point,
) (*Machine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	size := framebufferSize
	if size.X <= 0 || size.Y <= 0 {
		size = image.Pt(240, 320)
	}
	size = inferSKVMFramebufferSize(size, pkg.Resources)
	config := shared.DefaultConfig()
	config.Device.ProfileID = ProfileID
	config.Device.Carrier = "skt"
	config.Device.ScreenWidth = int32(size.X)
	config.Device.ScreenHeight = int32(size.Y)
	config.Device.ScreenFormat = shared.PixelRGBA8888
	config.Device.Capabilities = []shared.DeviceCapability{
		{Name: "audio", Enabled: true},
		{Name: "backlight", Enabled: true},
		{Name: "browser", Enabled: true},
		{Name: "graphics", Enabled: true},
		{Name: "images", Enabled: true},
		{Name: "text", Enabled: true},
		{Name: "vibration", Enabled: true},
	}
	if source.ProfileID != "" {
		config.Device.ProfileID = source.ProfileID
	}
	applySKVMTitleCompatibility(&config, source, pkg, size)
	services, err := shared.NewServices(config)
	if err != nil {
		return nil, fmt.Errorf("initialize SKVM shared services: %w", err)
	}
	budget := defaultSKVMRunBudget
	owner, err := services.Coordinator.Register("skvm", budget)
	if err != nil {
		return nil, fmt.Errorf("register SKVM adapter: %w", err)
	}
	for _, packaged := range pkg.RecordStores {
		store, err := services.Storage.CreateRecordStore(owner, packaged.Name)
		if err != nil {
			return nil, fmt.Errorf(
				"install SKVM record store %q: %w",
				packaged.Name,
				err,
			)
		}
		records := make(map[uint32][]byte, len(packaged.Records))
		for _, record := range packaged.Records {
			records[record.ID] = record.Data
		}
		if err := services.Storage.ReplaceRecords(
			owner,
			store,
			packaged.NextID,
			records,
		); err != nil {
			return nil, fmt.Errorf(
				"install SKVM record store %q records: %w",
				packaged.Name,
				err,
			)
		}
	}
	classData := make(map[string][]byte, len(pkg.Classes))
	for name, class := range pkg.Classes {
		classData[name] = append([]byte(nil), class.Data...)
	}
	vm, err := skengine.NewWithServices(classData, services, owner)
	if err != nil {
		return nil, err
	}
	vm.InstructionLimit = skengine.DefaultInstructionLimit
	if err := vm.SetResourcesChecked(pkg.Resources); err != nil {
		return nil, fmt.Errorf("mount SKVM resources: %w", err)
	}
	vm.SetProperties(pkg.Descriptor.Raw)
	if err := services.Coordinator.Transition(
		owner,
		shared.LifecycleReady,
		services.Clock.Monotonic(),
		services.Events,
	); err != nil {
		return nil, fmt.Errorf("ready SKVM adapter: %w", err)
	}
	machine := &Machine{
		state:        machinecore.StateReady,
		source:       source,
		mainClass:    pkg.Descriptor.MainClass,
		classData:    classData,
		vm:           vm,
		services:     services,
		owner:        owner,
		frameQuantum: config.FrameDuration,
	}
	machine.initialState, err = vm.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("capture initial SKVM state: %w", err)
	}
	return machine, nil
}

func (m *Machine) Load(context.Context, machinecore.Source) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	return fmt.Errorf("load from %s: %w", m.state, guest.ErrInvalidState)
}

func (m *Machine) State() machinecore.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Machine) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state != machinecore.StateReady {
		return fmt.Errorf("start from %s: %w", m.state, guest.ErrInvalidState)
	}
	before, err := m.beginExecutionLocked()
	if err != nil {
		return err
	}
	m.state = machinecore.StateRunning
	if err := m.services.Coordinator.Transition(
		m.owner,
		shared.LifecycleRunning,
		m.services.Clock.Monotonic(),
		m.services.Events,
	); err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	reference, err := m.vm.Start(ctx, m.mainClass)
	if err == nil {
		m.midlet = reference
		m.started = true
		err = m.pumpAndPaintLocked(ctx, 0)
	}
	if err == nil {
		err = m.consumeInstructionsLocked(before)
	}
	if err != nil {
		return m.faultLocked(err)
	}
	if err := m.services.Coordinator.Transition(
		m.owner,
		shared.LifecyclePaused,
		m.services.Clock.Monotonic(),
		m.services.Events,
	); err != nil {
		return m.faultLocked(err)
	}
	m.state = machinecore.StatePaused
	return nil
}

func (m *Machine) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state == machinecore.StatePaused {
		return nil
	}
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("pause from %s: %w", m.state, guest.ErrInvalidState)
	}
	before, err := m.beginExecutionLocked()
	if err != nil {
		return m.faultLocked(err)
	}
	if m.started && m.midlet != 0 {
		_, _, err = m.vm.InvokeVirtual(
			context.Background(),
			m.midlet,
			"pauseApp",
			"()V",
		)
		if err != nil && !errors.Is(err, skengine.ErrMethodNotFound) {
			return m.faultLocked(err)
		}
	}
	if err := m.consumeInstructionsLocked(before); err != nil {
		return m.faultLocked(err)
	}
	if err := m.services.Coordinator.Transition(
		m.owner,
		shared.LifecyclePaused,
		m.services.Clock.Monotonic(),
		m.services.Events,
	); err != nil {
		return m.faultLocked(err)
	}
	m.state = machinecore.StatePaused
	return nil
}

func (m *Machine) Resume() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state != machinecore.StatePaused {
		return fmt.Errorf("resume from %s: %w", m.state, guest.ErrInvalidState)
	}
	before, err := m.beginExecutionLocked()
	if err != nil {
		return err
	}
	m.state = machinecore.StateRunning
	if err := m.services.Coordinator.Transition(
		m.owner,
		shared.LifecycleRunning,
		m.services.Clock.Monotonic(),
		m.services.Events,
	); err != nil {
		return m.faultLocked(err)
	}
	if m.started && m.midlet != 0 {
		_, _, err = m.vm.InvokeVirtual(
			context.Background(),
			m.midlet,
			"startApp",
			"()V",
		)
		if err != nil && !errors.Is(err, skengine.ErrMethodNotFound) {
			return m.faultLocked(err)
		}
	}
	if err := m.pumpAndPaintLocked(context.Background(), 0); err != nil {
		return m.faultLocked(err)
	}
	if err := m.consumeInstructionsLocked(before); err != nil {
		return m.faultLocked(err)
	}
	if err := m.services.Coordinator.Transition(
		m.owner,
		shared.LifecyclePaused,
		m.services.Clock.Monotonic(),
		m.services.Events,
	); err != nil {
		return m.faultLocked(err)
	}
	m.state = machinecore.StatePaused
	return nil
}

func (m *Machine) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state == machinecore.StateStopped {
		return nil
	}
	if m.state == machinecore.StateEmpty || m.state == machinecore.StateRunning {
		return fmt.Errorf("stop from %s: %w", m.state, guest.ErrInvalidState)
	}
	adapter, err := m.services.Coordinator.Adapter(m.owner)
	if err != nil {
		return err
	}
	var before uint64
	if m.started && m.midlet != 0 {
		before, err = m.beginExecutionLocked()
		if err != nil {
			return m.faultLocked(err)
		}
		if adapter.Lifecycle != shared.LifecycleRunning {
			if err := m.services.Coordinator.Transition(
				m.owner,
				shared.LifecycleRunning,
				m.services.Clock.Monotonic(),
				m.services.Events,
			); err != nil {
				return m.faultLocked(err)
			}
		}
		_, _, err := m.vm.InvokeVirtual(
			context.Background(),
			m.midlet,
			"destroyApp",
			"(Z)V",
			skengine.IntValue(1),
		)
		if err != nil && !errors.Is(err, skengine.ErrMethodNotFound) {
			return m.faultLocked(err)
		}
		if err := m.consumeInstructionsLocked(before); err != nil {
			return m.faultLocked(err)
		}
	}
	if err := m.services.Coordinator.Transition(
		m.owner,
		shared.LifecycleStopped,
		m.services.Clock.Monotonic(),
		m.services.Events,
	); err != nil {
		return m.faultLocked(err)
	}
	m.state = machinecore.StateStopped
	return nil
}

func (m *Machine) Reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state == machinecore.StateRunning {
		return fmt.Errorf("reset from %s: %w", m.state, guest.ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	persistence := m.services.Storage.ExportPersistence()
	for _, store := range persistence.RecordStores {
		if store.Owner != m.owner {
			m.state = machinecore.StateFaulted
			return fmt.Errorf(
				"capture SKVM persistence: record store %q belongs to owner %d, want %d",
				store.Name,
				store.Owner,
				m.owner,
			)
		}
	}
	if err := m.vm.UnmarshalBinary(m.initialState); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("reset SKVM state: %w", err)
	}
	m.services = m.vm.Services()
	for index := range persistence.RecordStores {
		persistence.RecordStores[index].Owner = m.owner
	}
	if err := m.services.Storage.ImportPersistence(persistence); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("restore SKVM persistence after reset: %w", err)
	}
	m.input = nil
	m.started = false
	m.midlet = 0
	m.frameQuantum = m.services.Config.FrameDuration
	m.state = machinecore.StateReady
	return nil
}

func (m *Machine) StepFrame(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state != machinecore.StatePaused {
		return fmt.Errorf("step from %s: %w", m.state, guest.ErrInvalidState)
	}
	before, err := m.beginExecutionLocked()
	if err != nil {
		return m.faultLocked(err)
	}
	m.state = machinecore.StateRunning
	if err := m.services.Coordinator.Transition(
		m.owner,
		shared.LifecycleRunning,
		m.services.Clock.Monotonic(),
		m.services.Events,
	); err != nil {
		return m.faultLocked(err)
	}
	frameStartedAt := m.services.Clock.Monotonic()
	if err := m.pumpAndPaintLocked(ctx, m.services.Config.FrameDuration); err != nil {
		return m.faultLocked(err)
	}
	frameFinishedAt := m.services.Clock.Monotonic()
	if frameFinishedAt <= frameStartedAt {
		return m.faultLocked(fmt.Errorf("SKVM frame did not advance virtual time"))
	}
	m.frameQuantum = frameFinishedAt - frameStartedAt
	if err := m.consumeInstructionsLocked(before); err != nil {
		return m.faultLocked(err)
	}
	if err := m.services.Coordinator.Transition(
		m.owner,
		shared.LifecyclePaused,
		m.services.Clock.Monotonic(),
		m.services.Events,
	); err != nil {
		return m.faultLocked(err)
	}
	m.state = machinecore.StatePaused
	return nil
}

func (m *Machine) pumpAndPaintLocked(
	ctx context.Context,
	delta time.Duration,
) error {
	start := m.services.Clock.Monotonic()
	if delta < 0 || delta > time.Duration(^uint64(0)>>1)-start {
		return fmt.Errorf("invalid SKVM frame duration %s", delta)
	}
	target := start + delta
	for len(m.input) != 0 && m.input[0].At <= target {
		event := m.input[0]
		m.input = m.input[1:]
		now := m.services.Clock.Monotonic()
		if event.At > now {
			if err := m.vm.Advance(
				ctx,
				event.At-now,
				func(ready shared.Event) error {
					return m.handleEventLocked(ctx, ready)
				},
			); err != nil {
				return err
			}
		}
		if err := m.services.QueueInput(
			m.owner,
			event.Control,
			event.Pressed,
			event.At,
		); err != nil {
			return err
		}
		if err := m.vm.Advance(
			ctx,
			0,
			func(ready shared.Event) error {
				return m.handleEventLocked(ctx, ready)
			},
		); err != nil {
			return err
		}
	}
	now := m.services.Clock.Monotonic()
	if now < target {
		if err := m.vm.Advance(
			ctx,
			target-now,
			func(ready shared.Event) error {
				return m.handleEventLocked(ctx, ready)
			},
		); err != nil {
			return err
		}
	} else if delta == 0 {
		if err := m.vm.Advance(
			ctx,
			0,
			func(ready shared.Event) error {
				return m.handleEventLocked(ctx, ready)
			},
		); err != nil {
			return err
		}
	}
	if m.vm.CurrentDisplay() != 0 {
		if err := m.vm.PaintCurrent(ctx); err != nil &&
			!errors.Is(err, skengine.ErrMethodNotFound) {
			return err
		}
	}
	_, err := m.services.Graphics.Present(
		m.owner,
		m.vm.ScreenSurface(),
		shared.Rectangle{},
	)
	return err
}

func (m *Machine) handleEventLocked(
	ctx context.Context,
	event shared.Event,
) error {
	if event.Owner != m.owner {
		return nil
	}
	switch event.Kind {
	case shared.EventInputPress, shared.EventInputRelease, shared.EventInputRepeat:
		key, ok := guest.InputKeyCode(event.Control)
		if !ok || m.vm.CurrentDisplay() == 0 {
			return nil
		}
		pressed := event.Kind != shared.EventInputRelease
		return m.vm.KeyEvent(ctx, skvmKeyCode(key), pressed)
	default:
		return nil
	}
}

func skvmKeyCode(key profile.KeyCode) int32 {
	switch key {
	case profile.KeyUp:
		return 141
	case profile.KeyLeft:
		return 142
	case profile.KeyRight:
		return 145
	case profile.KeyDown:
		return 146
	case profile.KeySelect:
		return 148
	default:
		return int32(key)
	}
}

func inferSKVMFramebufferSize(
	fallback image.Point,
	resources map[string][]byte,
) image.Point {
	// SKT descriptors do not consistently declare a display size. Full-screen
	// images commonly omit only a small handset status/soft-key strip, so use
	// those package assets to select the matching legacy canvas geometry.
	candidates := [...]image.Point{
		image.Pt(120, 160),
		image.Pt(128, 160),
		image.Pt(176, 208),
		image.Pt(176, 220),
		image.Pt(240, 320),
	}
	scores := make([]uint64, len(candidates))
	for _, data := range resources {
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			continue
		}
		for index, candidate := range candidates {
			if config.Width == candidate.X &&
				config.Height <= candidate.Y &&
				config.Height >= candidate.Y-16 {
				scores[index] += uint64(config.Width * config.Height)
			}
		}
	}
	best := -1
	var bestScore uint64
	for index, score := range scores {
		if score > bestScore {
			best = index
			bestScore = score
		}
	}
	if best < 0 {
		return fallback
	}
	return candidates[best]
}

func (m *Machine) consumeInstructionsLocked(before uint64) error {
	if m.vm.Instructions < before {
		return fmt.Errorf("SKVM instruction counter moved backwards")
	}
	return m.services.Coordinator.Consume(
		m.owner,
		m.vm.Instructions-before,
	)
}

func (m *Machine) beginExecutionLocked() (uint64, error) {
	if _, err := m.services.Coordinator.BeginQuantum(); err != nil {
		return 0, err
	}
	adapter, err := m.services.Coordinator.Adapter(m.owner)
	if err != nil {
		return 0, err
	}
	before := m.vm.Instructions
	if before > ^uint64(0)-adapter.RunBudget {
		return 0, fmt.Errorf("SKVM instruction counter exhausted")
	}
	m.vm.InstructionLimit = before + adapter.RunBudget
	return before, nil
}

func (m *Machine) QueueInput(event machinecore.InputEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if len(m.input) >= maxSKVMPendingInputs {
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
	frame := m.services.Graphics.LastFrame()
	if frame.Sequence != 0 {
		result := image.NewNRGBA(image.Rect(0, 0, int(frame.Width), int(frame.Height)))
		copy(result.Pix, frame.RGBA)
		return result
	}
	pixels := m.vm.FrameRGBA()
	result := image.NewNRGBA(image.Rect(
		0,
		0,
		m.vm.ScreenWidth,
		m.vm.ScreenHeight,
	))
	copy(result.Pix, pixels)
	return result
}

func (m *Machine) DrainAudio() machinecore.AudioChunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	audio := m.services.Media.Drain()
	return machinecore.AudioChunk{
		SampleRate: audio.SampleRate,
		Channels:   audio.Channels,
		PCM16:      audio.PCM16,
	}
}

func (m *Machine) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	if adapter, err := m.services.Coordinator.Adapter(m.owner); err == nil &&
		adapter.Lifecycle != shared.LifecycleDestroyed {
		_ = m.services.Coordinator.Transition(
			m.owner,
			shared.LifecycleDestroyed,
			m.services.Clock.Monotonic(),
			nil,
		)
	}
	m.closed = true
	m.state = machinecore.StateStopped
	return nil
}

func (m *Machine) faultLocked(cause error) error {
	m.state = machinecore.StateFaulted
	_ = m.services.Coordinator.Fault(
		m.owner,
		cause.Error(),
		m.services.Clock.Monotonic(),
		nil,
	)
	return cause
}

// Services exposes the shared service container for tests and diagnostics.
func (m *Machine) Services() *shared.Services { return m.services }

// Owner exposes the machine's shared-service owner ID for tests.
func (m *Machine) Owner() shared.OwnerID { return m.owner }

// VM exposes the underlying SKVM interpreter for tests and diagnostics.
func (m *Machine) VM() *skengine.VM { return m.vm }
