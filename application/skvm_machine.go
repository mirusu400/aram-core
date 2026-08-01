package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/ktf"
	"github.com/mirusu400/aram-core/loader/raptor"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
	"github.com/mirusu400/aram-core/profile"
	shared "github.com/mirusu400/aram-core/runtime"
	skengine "github.com/mirusu400/aram-core/skvm"
)

const (
	skvmProfileID            = "wipi-1.2.1/skt/generic"
	skvmMachineStateMagic    = "ARAMSKM\x00"
	skvmMachineStateVersion  = uint32(1)
	maxSKVMMachineStateBytes = uint64(1024 << 20)
	maxSKVMPendingInputs     = 1024
	defaultSKVMRunBudget     = uint64(10_000_000)
)

type skvmMachine struct {
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
	closed       bool
}

func (f Factory) createSKVMMachine(
	ctx context.Context,
	source machinecore.Source,
) (machinecore.Machine, bool, error) {
	if err := source.Validate(); err != nil {
		return nil, false, nil
	}
	if source.Size > maxApplicationSize {
		return nil, false, nil
	}
	data, err := io.ReadAll(io.NewSectionReader(source.ReaderAt, 0, source.Size))
	if err != nil || int64(len(data)) != source.Size {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, true, fmt.Errorf("read SKVM application: %w", err)
	}
	// Preserve the established native-package precedence. Exact KTF or Raptor
	// packages continue through Machine.Load; only an unclaimed ZIP reaches
	// the SKVM probe.
	if _, inspectErr := ktf.Inspect(data); inspectErr == nil ||
		nativeKTFDiagnostic(inspectErr) {
		return nil, false, nil
	}
	if _, inspectErr := raptor.Inspect(data); inspectErr == nil ||
		nativeRaptorDiagnostic(inspectErr) {
		return nil, false, nil
	}
	pkg, err := skloader.Inspect(data)
	if errors.Is(err, skloader.ErrNotPackage) {
		return nil, false, nil
	}
	var formatErr *skloader.FormatError
	if errors.As(err, &formatErr) &&
		formatErr.Path == "archive" &&
		strings.HasPrefix(formatErr.Reason, "invalid ZIP:") {
		// A corrupt outer ZIP has not supplied enough evidence to claim the
		// package as SKVM. Preserve the established native/generic probing
		// result so an ordinary or unsupported Java archive is not
		// misclassified as a malformed SKVM distribution.
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("inspect SKVM package: %w", err)
	}
	digest := sha256.Sum256(data)
	actualSHA256 := hex.EncodeToString(digest[:])
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, actualSHA256) {
		return nil, true, fmt.Errorf(
			"load %q: SHA-256 mismatch: expected %s, got %s",
			source.Name,
			source.SHA256,
			actualSHA256,
		)
	}
	source.SHA256 = actualSHA256
	machine, err := newSKVMMachine(ctx, source, pkg, f)
	return machine, true, err
}

func nativeKTFDiagnostic(err error) bool {
	if err == nil || errors.Is(err, ktf.ErrNotPackage) {
		return false
	}
	var formatErr *ktf.FormatError
	return !errors.As(err, &formatErr) ||
		formatErr.Path != "archive" ||
		!strings.HasPrefix(formatErr.Reason, "invalid ZIP:")
}

func nativeRaptorDiagnostic(err error) bool {
	if err == nil || errors.Is(err, raptor.ErrNotPackage) {
		return false
	}
	var formatErr *raptor.FormatError
	return !errors.As(err, &formatErr) ||
		formatErr.Path != "archive" ||
		!strings.HasPrefix(formatErr.Reason, "invalid ZIP:")
}

func newSKVMMachine(
	ctx context.Context,
	source machinecore.Source,
	pkg skloader.Package,
	factory Factory,
) (*skvmMachine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	size := factory.FramebufferSize
	if size.X <= 0 || size.Y <= 0 {
		size = image.Pt(240, 320)
	}
	config := shared.DefaultConfig()
	config.Device.ProfileID = skvmProfileID
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
	machine := &skvmMachine{
		state:     machinecore.StateReady,
		source:    source,
		mainClass: pkg.Descriptor.MainClass,
		classData: classData,
		vm:        vm,
		services:  services,
		owner:     owner,
	}
	machine.initialState, err = vm.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("capture initial SKVM state: %w", err)
	}
	return machine, nil
}

func (m *skvmMachine) Load(context.Context, machinecore.Source) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	return fmt.Errorf("load from %s: %w", m.state, ErrInvalidState)
}

func (m *skvmMachine) State() machinecore.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *skvmMachine) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state != machinecore.StateReady {
		return fmt.Errorf("start from %s: %w", m.state, ErrInvalidState)
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

func (m *skvmMachine) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state == machinecore.StatePaused {
		return nil
	}
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("pause from %s: %w", m.state, ErrInvalidState)
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

func (m *skvmMachine) Resume() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state != machinecore.StatePaused {
		return fmt.Errorf("resume from %s: %w", m.state, ErrInvalidState)
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

func (m *skvmMachine) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state == machinecore.StateStopped {
		return nil
	}
	if m.state == machinecore.StateEmpty || m.state == machinecore.StateRunning {
		return fmt.Errorf("stop from %s: %w", m.state, ErrInvalidState)
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

func (m *skvmMachine) Reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state == machinecore.StateRunning {
		return fmt.Errorf("reset from %s: %w", m.state, ErrInvalidState)
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
	m.state = machinecore.StateReady
	return nil
}

func (m *skvmMachine) StepFrame(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state != machinecore.StatePaused {
		return fmt.Errorf("step from %s: %w", m.state, ErrInvalidState)
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
	if err := m.pumpAndPaintLocked(ctx, m.services.Config.FrameDuration); err != nil {
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

func (m *skvmMachine) pumpAndPaintLocked(
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

func (m *skvmMachine) handleEventLocked(
	ctx context.Context,
	event shared.Event,
) error {
	if event.Owner != m.owner {
		return nil
	}
	switch event.Kind {
	case shared.EventInputPress, shared.EventInputRelease, shared.EventInputRepeat:
		key, ok := inputKeyCode(event.Control)
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

func (m *skvmMachine) consumeInstructionsLocked(before uint64) error {
	if m.vm.Instructions < before {
		return fmt.Errorf("SKVM instruction counter moved backwards")
	}
	return m.services.Coordinator.Consume(
		m.owner,
		m.vm.Instructions-before,
	)
}

func (m *skvmMachine) beginExecutionLocked() (uint64, error) {
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

func (m *skvmMachine) QueueInput(event machinecore.InputEvent) error {
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

func (m *skvmMachine) Framebuffer() image.Image {
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

func (m *skvmMachine) DrainAudio() machinecore.AudioChunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	audio := m.services.Media.Drain()
	return machinecore.AudioChunk{
		SampleRate: audio.SampleRate,
		Channels:   audio.Channels,
		PCM16:      audio.PCM16,
	}
}

func (m *skvmMachine) SaveState(output io.Writer) error {
	if output == nil {
		return fmt.Errorf("save SKVM state: writer is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
	default:
		return fmt.Errorf("save from %s: %w", m.state, ErrInvalidState)
	}
	if err := validateSKVMMachineCoordinator(
		m.services,
		m.owner,
		m.state,
	); err != nil {
		return fmt.Errorf("save SKVM state: %w", err)
	}
	vmState, err := m.vm.MarshalBinary()
	if err != nil {
		return err
	}
	sourceDigest, err := hex.DecodeString(m.source.SHA256)
	if err != nil || len(sourceDigest) != sha256.Size {
		return fmt.Errorf("save SKVM state: source SHA-256 is unavailable")
	}
	var payload bytes.Buffer
	payload.WriteString(skvmMachineStateMagic)
	writeSKVMU32(&payload, skvmMachineStateVersion)
	payload.WriteByte(byte(m.state))
	if m.started {
		payload.WriteByte(1)
	} else {
		payload.WriteByte(0)
	}
	payload.Write([]byte{0, 0})
	payload.Write(sourceDigest)
	writeSKVMString(&payload, m.mainClass)
	writeSKVMU32(&payload, m.midlet)
	writeSKVMU32(&payload, uint32(len(m.input)))
	for _, event := range m.input {
		writeSKVMString(&payload, event.Control)
		if event.Pressed {
			payload.WriteByte(1)
		} else {
			payload.WriteByte(0)
		}
		payload.Write(make([]byte, 7))
		writeSKVMU64(&payload, uint64(event.At))
	}
	writeSKVMU64(&payload, uint64(len(vmState)))
	payload.Write(vmState)
	digest := sha256.Sum256(payload.Bytes())
	payload.Write(digest[:])
	if uint64(payload.Len()) > maxSKVMMachineStateBytes {
		return fmt.Errorf("save SKVM state: state exceeds byte limit")
	}
	return writeFull(output, payload.Bytes())
}

func (m *skvmMachine) LoadState(input io.Reader) error {
	if input == nil {
		return fmt.Errorf("load SKVM state: reader is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state == machinecore.StateRunning || m.state == machinecore.StateEmpty {
		return fmt.Errorf("load from %s: %w", m.state, ErrInvalidState)
	}
	data, err := io.ReadAll(io.LimitReader(input, int64(maxSKVMMachineStateBytes)+1))
	if err != nil {
		return fmt.Errorf("read SKVM state: %w", err)
	}
	if uint64(len(data)) > maxSKVMMachineStateBytes {
		return fmt.Errorf("read SKVM state: state exceeds byte limit")
	}
	parsed, err := m.parseMachineState(data)
	if err != nil {
		return err
	}
	candidateServices, err := shared.NewServices(m.services.Config)
	if err != nil {
		return err
	}
	candidate, err := skengine.NewWithServices(m.classData, candidateServices, m.owner)
	if err != nil {
		return err
	}
	if err := candidate.UnmarshalBinary(parsed.vm); err != nil {
		return err
	}
	if err := validateSKVMMachineCoordinator(
		candidateServices,
		m.owner,
		parsed.state,
	); err != nil {
		return fmt.Errorf("load SKVM state: %w", err)
	}
	if parsed.midlet != 0 {
		if _, ok := candidate.Object(parsed.midlet); !ok {
			return fmt.Errorf("load SKVM state: MIDlet reference is missing")
		}
	}
	if err := m.vm.UnmarshalBinary(parsed.vm); err != nil {
		return err
	}
	m.services = m.vm.Services()
	m.state = parsed.state
	m.started = parsed.started
	m.midlet = parsed.midlet
	m.input = parsed.input
	return nil
}

type parsedSKVMMachineState struct {
	state   machinecore.State
	started bool
	midlet  uint32
	input   []machinecore.InputEvent
	vm      []byte
}

func (m *skvmMachine) parseMachineState(data []byte) (parsedSKVMMachineState, error) {
	if len(data) < len(skvmMachineStateMagic)+4+4+sha256.Size+sha256.Size {
		return parsedSKVMMachineState{}, fmt.Errorf("load SKVM state: truncated header")
	}
	payload := data[:len(data)-sha256.Size]
	expected := data[len(payload):]
	actual := sha256.Sum256(payload)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return parsedSKVMMachineState{}, fmt.Errorf("load SKVM state: checksum mismatch")
	}
	decoder := skvmMachineDecoder{reader: bytes.NewReader(payload)}
	if magic := decoder.bytes(len(skvmMachineStateMagic)); string(magic) != skvmMachineStateMagic {
		return parsedSKVMMachineState{}, decoder.fail("magic mismatch")
	}
	if version := decoder.u32(); version != skvmMachineStateVersion {
		return parsedSKVMMachineState{}, decoder.fail(
			fmt.Sprintf("unsupported version %d", version),
		)
	}
	state := machinecore.State(decoder.u8())
	started := decoder.u8() != 0
	decoder.reserved(2)
	sourceDigest := decoder.bytes(sha256.Size)
	mainClass := decoder.string()
	midlet := decoder.u32()
	inputCount := decoder.u32()
	if decoder.err != nil {
		return parsedSKVMMachineState{}, decoder.err
	}
	expectedDigest, err := hex.DecodeString(m.source.SHA256)
	if err != nil || subtle.ConstantTimeCompare(sourceDigest, expectedDigest) != 1 {
		return parsedSKVMMachineState{}, decoder.fail("source SHA-256 mismatch")
	}
	if mainClass != m.mainClass {
		return parsedSKVMMachineState{}, decoder.fail("main class mismatch")
	}
	switch state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
	default:
		return parsedSKVMMachineState{}, decoder.fail("invalid machine state")
	}
	if (!started && midlet != 0) || (started && midlet == 0) ||
		(state == machinecore.StateReady && started) ||
		inputCount > maxSKVMPendingInputs {
		return parsedSKVMMachineState{}, decoder.fail("invalid lifecycle or input state")
	}
	inputs := make([]machinecore.InputEvent, 0, inputCount)
	var previousAt time.Duration
	for index := uint32(0); index < inputCount; index++ {
		control := decoder.string()
		pressed := decoder.u8()
		event := machinecore.InputEvent{
			Control: control,
			Pressed: pressed != 0,
		}
		decoder.reserved(7)
		event.At = time.Duration(int64(decoder.u64()))
		if decoder.err != nil {
			return parsedSKVMMachineState{}, decoder.err
		}
		if pressed > 1 ||
			event.Validate() != nil ||
			(index != 0 && event.At < previousAt) {
			return parsedSKVMMachineState{}, decoder.fail(
				fmt.Sprintf("invalid input event %d", index),
			)
		}
		inputs = append(inputs, event)
		previousAt = event.At
	}
	vmSize := decoder.u64()
	if vmSize > uint64(decoder.reader.Len()) || vmSize > uint64(maxHostInt()) {
		return parsedSKVMMachineState{}, decoder.fail("invalid VM state size")
	}
	vmState := append([]byte(nil), decoder.bytes(int(vmSize))...)
	if decoder.err != nil {
		return parsedSKVMMachineState{}, decoder.err
	}
	if decoder.reader.Len() != 0 {
		return parsedSKVMMachineState{}, decoder.fail(
			fmt.Sprintf("%d trailing bytes", decoder.reader.Len()),
		)
	}
	return parsedSKVMMachineState{
		state: state, started: started, midlet: midlet,
		input: inputs, vm: vmState,
	}, nil
}

func validateSKVMMachineCoordinator(
	services *shared.Services,
	owner shared.OwnerID,
	state machinecore.State,
) error {
	if services == nil || services.Coordinator == nil {
		return fmt.Errorf("shared coordinator is missing")
	}
	expected := shared.LifecycleState(0)
	switch state {
	case machinecore.StateReady:
		expected = shared.LifecycleReady
	case machinecore.StatePaused:
		expected = shared.LifecyclePaused
	case machinecore.StateStopped:
		expected = shared.LifecycleStopped
	default:
		return fmt.Errorf("machine lifecycle %s is not serializable", state)
	}
	snapshot := services.Coordinator.Snapshot()
	if len(snapshot.Adapters) != 1 ||
		snapshot.Adapters[0].Owner != owner ||
		snapshot.Adapters[0].Name != "skvm" ||
		snapshot.Adapters[0].Lifecycle != expected ||
		snapshot.ForegroundOwner != owner ||
		snapshot.PresentationOwner != owner {
		return fmt.Errorf("shared coordinator lifecycle does not match machine state")
	}
	return nil
}

func (m *skvmMachine) Close() error {
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

func (m *skvmMachine) faultLocked(cause error) error {
	m.state = machinecore.StateFaulted
	_ = m.services.Coordinator.Fault(
		m.owner,
		cause.Error(),
		m.services.Clock.Monotonic(),
		nil,
	)
	return cause
}

func writeSKVMString(output *bytes.Buffer, value string) {
	writeSKVMU32(output, uint32(len(value)))
	output.WriteString(value)
}

func writeSKVMU32(output *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	output.Write(encoded[:])
}

func writeSKVMU64(output *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	output.Write(encoded[:])
}

type skvmMachineDecoder struct {
	reader *bytes.Reader
	offset uint64
	err    error
}

func (d *skvmMachineDecoder) bytes(size int) []byte {
	if d.err != nil || size < 0 || size > d.reader.Len() {
		if d.err == nil {
			d.err = d.fail("truncated data")
		}
		return nil
	}
	result := make([]byte, size)
	if _, err := io.ReadFull(d.reader, result); err != nil {
		d.err = d.fail(err.Error())
		return nil
	}
	d.offset += uint64(size)
	return result
}

func (d *skvmMachineDecoder) reserved(size int) {
	for _, value := range d.bytes(size) {
		if value != 0 && d.err == nil {
			d.err = d.fail("nonzero reserved field")
		}
	}
}

func (d *skvmMachineDecoder) u8() uint8 {
	data := d.bytes(1)
	if len(data) != 1 {
		return 0
	}
	return data[0]
}

func (d *skvmMachineDecoder) u32() uint32 {
	data := d.bytes(4)
	if len(data) != 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(data)
}

func (d *skvmMachineDecoder) u64() uint64 {
	data := d.bytes(8)
	if len(data) != 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(data)
}

func (d *skvmMachineDecoder) string() string {
	size := d.u32()
	if size > uint32(d.reader.Len()) {
		d.err = d.fail("truncated string")
		return ""
	}
	return string(d.bytes(int(size)))
}

func (d *skvmMachineDecoder) fail(reason string) error {
	return fmt.Errorf("load SKVM state at offset 0x%x: %s", d.offset, reason)
}

func maxHostInt() int {
	return int(^uint(0) >> 1)
}
