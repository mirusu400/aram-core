package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"strings"
	"sync"

	"github.com/mirusu400/aram-core/application/internal/gvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/gvm"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/gnex"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	// GVMKernelProfileID explicitly opts into bounded initial-dispatch diagnostics.
	// It is not a playability, rendering, input, timer-delivery or save-state profile.
	GVMKernelProfileID = "gvm-kernel-v1/skt/diagnostic"
	// GVMOperationalProfileID explicitly opts the one qualified SKT corpus into
	// event-dispatch execution and presentation. It is not general GNEX support.
	GVMOperationalProfileID     = "gvm-kernel-v1/skt/operational"
	GVMOperationalSHA256        = "97fe208a02530ca21c6b47d7fa73cd60271aeeddd2305d2a972a217c97a4124f"
	defaultGVMWidth             = int32(240)
	defaultGVMHeight            = int32(240)
	operationalGVMWidth         = int32(120)
	operationalGVMHeight        = int32(80)
	defaultGVMBudget            = uint64(1)
	defaultGVMOperationalBudget = uint64(1_000_000)
)

var (
	ErrGVMInputUnavailable = errors.New("application: GVM input delivery is unavailable")
	ErrGVMStateUnavailable = errors.New("application: GVM save states are unavailable")
	errGVMTimerBoundary    = errors.New("application: GVM timer delivery boundary")
)

type gvmTimerBoundary struct {
	interval int16
	selector uint16
	reached  bool
	terminal bool
}

func (s *gvmTimerBoundary) RequestGVMTimer(interval int16, selector uint16) error {
	s.interval, s.selector = interval, selector
	s.reached = true
	if s.terminal {
		return errGVMTimerBoundary
	}
	return nil
}

type gvmFramePublisher struct {
	mu       sync.Mutex
	latest   image.Image
	presents uint64
}

func (p *gvmFramePublisher) PublishGVMFrame(frame image.Image) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.latest = frame
	p.presents++
	return nil
}

func (p *gvmFramePublisher) snapshot() (image.Image, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.latest, p.presents
}

type gvmDecodedMediaServices struct{}

func (*gvmDecodedMediaServices) LoadGVMMedia(uint16, []byte) error { return nil }
func (*gvmDecodedMediaServices) ResetGVMAudio(int32) error         { return nil }

// GVMDiagnosticBoundary describes the first host-service boundary reached by
// the explicit GVM diagnostic profile. It is diagnostic evidence, not delivery.
type GVMDiagnosticBoundary struct {
	Kind     string
	Interval int16
	Selector uint16
}

type gvmMachine struct {
	mu          sync.Mutex
	state       machinecore.State
	source      machinecore.Source
	packageSGS  []byte
	vm          *gvm.VM
	timer       *gvmTimerBoundary
	budget      uint64
	lastResult  cpu.Result
	operational bool
	eventEntry  uint32
	frames      *gvmFramePublisher
	closed      bool
}

func (f Factory) createGVMMachine(ctx context.Context, source machinecore.Source) (machinecore.Machine, bool, error) {
	if source.ProfileID != GVMKernelProfileID && source.ProfileID != GVMOperationalProfileID {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if err := source.Validate(); err != nil {
		return nil, true, err
	}
	if source.Size > maxApplicationSize {
		return nil, true, fmt.Errorf("load %q: source size %d exceeds limit", source.Name, source.Size)
	}
	data, err := io.ReadAll(io.NewSectionReader(source.ReaderAt, 0, source.Size))
	if err != nil {
		return nil, true, fmt.Errorf("read GVM application: %w", err)
	}
	if int64(len(data)) != source.Size {
		return nil, true, fmt.Errorf("read GVM application: %w", io.ErrUnexpectedEOF)
	}
	pkg, err := gnex.Inspect(data)
	if err != nil {
		return nil, true, fmt.Errorf("inspect GVM application: %w", err)
	}
	digest := sha256.Sum256(data)
	actualSHA256 := hex.EncodeToString(digest[:])
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, actualSHA256) {
		return nil, true, fmt.Errorf("load %q: SHA-256 mismatch: expected %s, got %s", source.Name, source.SHA256, actualSHA256)
	}
	source.SHA256 = actualSHA256
	operational := source.ProfileID == GVMOperationalProfileID
	if operational && actualSHA256 != GVMOperationalSHA256 {
		return nil, true, fmt.Errorf("load %q: operational GVM profile requires outer SHA-256 %s, got %s", source.Name, GVMOperationalSHA256, actualSHA256)
	}
	source.Format = string(loader.KindGNEX)
	budget := f.FrameRunBudget
	if budget == 0 {
		budget = f.RunBudget
	}
	if budget == 0 {
		budget = defaultGVMBudget
		if operational {
			budget = defaultGVMOperationalBudget
		}
	}
	machine := &gvmMachine{
		state:       machinecore.StateReady,
		source:      source,
		packageSGS:  bytes.Clone(pkg.SGS),
		budget:      budget,
		operational: operational,
	}
	if operational {
		if len(pkg.SGS) < 0x22 {
			return nil, true, fmt.Errorf("initialize operational GVM: SGS event entry at 0x20 is unavailable")
		}
		machine.eventEntry = uint32(binary.LittleEndian.Uint16(pkg.SGS[0x20:0x22]))
	}
	if err := machine.resetVMLocked(); err != nil {
		return nil, true, err
	}
	return machine, true, nil
}

func gvmAddressSpace(image gnex.ExecutionImage) gvm.AddressSpace {
	space := gvm.AddressSpace{
		FileStart:  uint32(image.SymbolFileStart),
		FileLength: uint32(image.SymbolFileEnd - image.SymbolFileStart),
		RAM:        image.SymbolRAM,
		Symbols:    make([]gvm.AddressSymbol, 0, len(image.Symbols)),
	}
	for _, symbol := range image.Symbols {
		binding := gvm.AddressSymbol{
			Region: gvm.AddressFile,
			Offset: uint32(symbol.BufferOffset - image.SymbolFileStart),
			Length: uint32(len(symbol.Data)),
		}
		if symbol.Mutable {
			binding.Region = gvm.AddressRAM
			binding.Offset = uint32(symbol.RAMOffset)
		}
		space.Symbols = append(space.Symbols, binding)
	}
	return space
}

func (m *gvmMachine) resetVMLocked() error {
	image, err := gnex.DecodeExecutionImage(m.packageSGS)
	if err != nil {
		return fmt.Errorf("decode GVM execution image: %w", err)
	}
	clock := new(shared.Clock)
	if err := clock.Restore(shared.ClockState{WallEpochMillis: 0, TimezoneOffsetMins: 540, Locale: "ko-KR"}); err != nil {
		return fmt.Errorf("initialize GVM diagnostic clock: %w", err)
	}
	random := shared.NewRandom(1, 4)
	if err := random.SetLCG214013Seed("gvm", 1); err != nil {
		return fmt.Errorf("initialize GVM diagnostic random stream: %w", err)
	}
	timer := &gvmTimerBoundary{terminal: !m.operational}
	width, height := defaultGVMWidth, defaultGVMHeight
	var display *gvmhost.DisplayAdapter
	var mediaServices *gvmDecodedMediaServices
	var media []gvm.MediaResource
	if m.operational {
		width, height = operationalGVMWidth, operationalGVMHeight
		m.frames = new(gvmFramePublisher)
		display, err = gvmhost.NewDisplayAdapter(gvmhost.DisplayConfig{
			Width: int(width), Height: int(height),
			Orientation: gvmhost.DisplayOrientationDefault,
			Palette:     gvmhost.SKTCompatibilityPalette{}, Publisher: m.frames,
		})
		if err != nil {
			return fmt.Errorf("initialize operational GVM display: %w", err)
		}
		mediaServices = new(gvmDecodedMediaServices)
		media = make([]gvm.MediaResource, len(image.Media))
		for i := range image.Media {
			media[i] = gvm.MediaResource{Data: image.Media[i].Data}
		}
	} else {
		m.frames = nil
	}
	services := &gvm.ServiceConfig{
		DeviceQuery: &gvm.DeviceQueryProfile{Width: width, Height: height, AudioType: 5},
		Clock:       clock, ClockPolicy: gvm.FixedOffsetNoDST,
		Random: random, RandomStream: "gvm",
		Timer: timer,
	}
	if m.operational {
		services.DisplayClear = display
		services.DisplayFill = display
		services.DisplayPresent = display
		services.DisplayCopy = display
		services.MappingSelect = display
		services.ColorSelect = display
		services.RectangleFill = display
		services.SpriteDraw = display
		services.AudioReset = mediaServices
		services.Media = media
		services.MediaLoad = mediaServices
	}
	vm, err := gvm.NewWithAddressSpaceAndServices(
		image.Buffer,
		uint32(image.Entry),
		gvmAddressSpace(image),
		services,
	)
	if err != nil {
		return fmt.Errorf("initialize GVM kernel: %w", err)
	}
	m.vm = vm
	m.timer = timer
	m.lastResult = cpu.Result{}
	return nil
}

func (m *gvmMachine) Load(context.Context, machinecore.Source) error {
	return fmt.Errorf("load from %s: %w", m.State(), ErrInvalidState)
}

func (m *gvmMachine) State() machinecore.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *gvmMachine) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state != machinecore.StateReady {
		return fmt.Errorf("start from %s: %w", m.state, ErrInvalidState)
	}
	if m.operational {
		return m.runOperationalLocked(ctx, false)
	}
	return m.runLocked(ctx)
}

func (m *gvmMachine) runOperationalLocked(ctx context.Context, dispatchEvent bool) error {
	m.state = machinecore.StateRunning
	if dispatchEvent && m.vm.Halted() {
		started, err := m.vm.BeginDispatch(m.eventEntry)
		if err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("begin operational GVM event dispatch at offset %d: %w", m.eventEntry, err)
		}
		if !started {
			m.lastResult = cpu.Result{Reason: cpu.StopExited, PC: uint32(m.vm.PC())}
			return nil
		}
	}
	var completed uint64
	for completed < m.budget {
		if err := ctx.Err(); err != nil {
			m.lastResult = cpu.Result{Reason: cpu.StopRequested, Instructions: completed, PC: uint32(m.vm.PC()), Err: err}
			return err
		}
		if err := m.vm.Step(); err != nil {
			m.lastResult = cpu.Result{Reason: cpu.StopFault, Instructions: completed, PC: uint32(m.vm.PC()), Err: err}
			m.state = machinecore.StateFaulted
			return fmt.Errorf("execute operational GVM at offset %d after %d instructions: %w", m.vm.PC(), completed, err)
		}
		completed++
		if m.vm.Halted() {
			m.lastResult = cpu.Result{Reason: cpu.StopExited, Instructions: completed, PC: uint32(m.vm.PC())}
			return nil
		}
	}
	err := fmt.Errorf("operational GVM dispatch did not halt within %d instructions", m.budget)
	m.lastResult = cpu.Result{Reason: cpu.StopBudget, Instructions: completed, PC: uint32(m.vm.PC()), Err: err}
	m.state = machinecore.StateFaulted
	return err
}

func (m *gvmMachine) runLocked(ctx context.Context) error {
	m.state = machinecore.StateRunning
	var completed uint64
	for completed < m.budget {
		if err := ctx.Err(); err != nil {
			m.state = machinecore.StatePaused
			m.lastResult = cpu.Result{Reason: cpu.StopRequested, Instructions: completed, PC: uint32(m.vm.PC()), Err: err}
			return err
		}
		err := m.vm.Step()
		if err != nil {
			if errors.Is(err, errGVMTimerBoundary) {
				m.lastResult = cpu.Result{Reason: cpu.StopBreakpoint, Instructions: completed, PC: uint32(m.vm.PC())}
				// The diagnostic profile deliberately has no timer delivery or guest
				// redispatch. This boundary is terminal until an explicit reset.
				m.state = machinecore.StateStopped
				return nil
			}
			m.lastResult = cpu.Result{Reason: cpu.StopFault, Instructions: completed, PC: uint32(m.vm.PC()), Err: err}
			m.state = machinecore.StateFaulted
			return fmt.Errorf("execute GVM at offset %d after %d instructions: %w", m.vm.PC(), completed, err)
		}
		completed++
		if m.vm.Halted() {
			m.lastResult = cpu.Result{Reason: cpu.StopExited, Instructions: completed, PC: uint32(m.vm.PC())}
			m.state = machinecore.StateStopped
			return nil
		}
	}
	m.lastResult = cpu.Result{Reason: cpu.StopBudget, Instructions: completed, PC: uint32(m.vm.PC())}
	m.state = machinecore.StatePaused
	return nil
}

func (m *gvmMachine) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state == machinecore.StatePaused {
		return nil
	}
	return fmt.Errorf("pause from %s: %w", m.state, ErrInvalidState)
}

func (m *gvmMachine) Resume() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state != machinecore.StatePaused {
		return fmt.Errorf("resume from %s: %w", m.state, ErrInvalidState)
	}
	return m.runLocked(context.Background())
}

func (m *gvmMachine) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state == machinecore.StateEmpty {
		return fmt.Errorf("stop from %s: %w", m.state, ErrInvalidState)
	}
	m.state = machinecore.StateStopped
	return nil
}

func (m *gvmMachine) Reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state == machinecore.StateRunning && !(m.operational && m.vm != nil && m.vm.Halted()) {
		return fmt.Errorf("reset from %s: %w", m.state, ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.resetVMLocked(); err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	m.state = machinecore.StateReady
	return nil
}

func (m *gvmMachine) StepFrame(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.operational {
		if m.state != machinecore.StateRunning {
			return fmt.Errorf("step frame from %s: %w", m.state, ErrInvalidState)
		}
		return m.runOperationalLocked(ctx, true)
	}
	if m.state != machinecore.StatePaused && m.state != machinecore.StateReady {
		return fmt.Errorf("step frame from %s: %w", m.state, ErrInvalidState)
	}
	return m.runLocked(ctx)
}

func (m *gvmMachine) QueueInput(event machinecore.InputEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	return ErrGVMInputUnavailable
}

func (m *gvmMachine) Framebuffer() image.Image {
	m.mu.Lock()
	frames := m.frames
	m.mu.Unlock()
	if frames == nil {
		return nil
	}
	frame, _ := frames.snapshot()
	return frame
}

// GVMPresentDiagnostics is an optional, narrow operational-profile diagnostic.
type GVMPresentDiagnostics interface{ GVMPresentCount() uint64 }

func (m *gvmMachine) GVMPresentCount() uint64 {
	m.mu.Lock()
	frames := m.frames
	m.mu.Unlock()
	if frames == nil {
		return 0
	}
	_, count := frames.snapshot()
	return count
}

func (*gvmMachine) DrainAudio() machinecore.AudioChunk { return machinecore.AudioChunk{} }
func (*gvmMachine) SaveState(io.Writer) error          { return ErrGVMStateUnavailable }
func (*gvmMachine) LoadState(io.Reader) error          { return ErrGVMStateUnavailable }

func (m *gvmMachine) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	m.vm = nil
	m.state = machinecore.StateStopped
	return nil
}

func (m *gvmMachine) SourceInfo() machinecore.Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.source
}

func (m *gvmMachine) LastResult() cpu.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastResult
}

func (m *gvmMachine) GVMDiagnosticBoundary() (GVMDiagnosticBoundary, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer == nil || !m.timer.reached {
		return GVMDiagnosticBoundary{}, false
	}
	return GVMDiagnosticBoundary{
		Kind:     "timer-request",
		Interval: m.timer.interval,
		Selector: m.timer.selector,
	}, true
}
