package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"io"
	"strings"
	"sync"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/application/internal/gvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/gnex"
	"github.com/mirusu400/aram-core/profile"
)

const (
	GNEX32VirusSHA256    = "bf9cd39b1ae14ba2a5f005d66fde5390e53cb400bd36940d4b09dcbfe9f03883"
	GNEX32VirusProfileID = "gnex32/skt/virus"
)

var gnex32VirusIdentity = gnex32.Identity{MIN: "0111234567", UserID: "98B4970F"}

type gnex32Machine struct {
	mu      sync.Mutex
	state   machinecore.State
	source  machinecore.Source
	image   gnex.GNEX32Image
	runtime *gnex32.Runtime
	budget  uint64
	closed  bool
}

func (f Factory) createGNEX32Machine(ctx context.Context, source machinecore.Source) (machinecore.Machine, bool, error) {
	if source.Format != string(loader.KindGNEX) && !strings.EqualFold(source.SHA256, GNEX32VirusSHA256) {
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
		return nil, true, err
	}
	if int64(len(data)) != source.Size {
		return nil, true, io.ErrUnexpectedEOF
	}
	digest := sha256.Sum256(data)
	actual := hex.EncodeToString(digest[:])
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, actual) {
		return nil, true, fmt.Errorf("load %q: SHA-256 mismatch: expected %s, got %s", source.Name, source.SHA256, actual)
	}
	if actual != GNEX32VirusSHA256 {
		return nil, false, nil
	}
	pkg, err := gnex.Inspect(data)
	if err != nil {
		return nil, true, err
	}
	image, err := gnex.DecodeGNEX32Image(pkg.SGS)
	if err != nil {
		return nil, true, err
	}
	runtime, err := gnex32.NewRuntime(image, gnex32VirusIdentity)
	if err != nil {
		return nil, true, err
	}
	source.SHA256 = actual
	source.Format = string(loader.KindGNEX)
	source.ProfileID = GNEX32VirusProfileID
	budget := f.FrameRunBudget
	if budget == 0 {
		budget = 1_000_000
	}
	return &gnex32Machine{state: machinecore.StateReady, source: source, image: image, runtime: runtime, budget: budget}, true, nil
}

func (m *gnex32Machine) Load(context.Context, machinecore.Source) error {
	return fmt.Errorf("load from %s: %w", m.State(), ErrInvalidState)
}

func (m *gnex32Machine) State() machinecore.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *gnex32Machine) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("GNEX32 machine is closed")
	}
	if m.state != machinecore.StateReady {
		return fmt.Errorf("start from %s: %w", m.state, ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.runtime.RunMain(m.budget); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("start GNEX32 image at code offset %d: %w", m.runtime.PC(), err)
	}
	m.state = machinecore.StateRunning
	return nil
}

func (m *gnex32Machine) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("pause from %s: %w", m.state, ErrInvalidState)
	}
	m.state = machinecore.StatePaused
	return nil
}

func (m *gnex32Machine) Resume() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StatePaused {
		return fmt.Errorf("resume from %s: %w", m.state, ErrInvalidState)
	}
	m.state = machinecore.StateRunning
	return nil
}

func (m *gnex32Machine) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == machinecore.StateEmpty {
		return fmt.Errorf("stop from %s: %w", m.state, ErrInvalidState)
	}
	m.state = machinecore.StateStopped
	return nil
}

func (m *gnex32Machine) Reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("GNEX32 machine is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime, err := gnex32.NewRuntime(m.image, gnex32VirusIdentity)
	if err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	m.runtime = runtime
	m.state = machinecore.StateReady
	return nil
}

func (m *gnex32Machine) StepFrame(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("step frame from %s: %w", m.state, ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.runtime.StepFrame(m.budget); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("step GNEX32 timer at code offset %d: %w", m.runtime.PC(), err)
	}
	return nil
}

func (m *gnex32Machine) QueueInput(event machinecore.InputEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("GNEX32 input from %s: %w", m.state, ErrInvalidState)
	}
	key, known := guest.InputKeyCode(event.Control)
	if !known {
		return ErrGVMInputUnavailable
	}
	code, supported := gnex32GuestCode(key)
	if !supported {
		return ErrGVMInputUnavailable
	}
	if !event.Pressed {
		code = 0xff
	}
	if err := m.runtime.KeyPressed(code, m.budget); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("deliver GNEX32 key %q at code offset %d: %w", event.Control, m.runtime.PC(), err)
	}
	return nil
}

func gnex32GuestCode(key profile.KeyCode) (uint16, bool) {
	switch key {
	case profile.KeyLeft:
		return 0x10, true
	case profile.KeyRight:
		return 0x11, true
	case profile.KeyUp:
		return 0x12, true
	case profile.KeyDown:
		return 0x13, true
	case profile.KeySelect:
		return 0x14, true
	case profile.KeyClear:
		return 0x0d, true
	case profile.KeySend:
		return 0x0e, true
	case profile.KeyEnd:
		return 0x0f, true
	case profile.KeySoft1:
		return 0x15, true
	case profile.KeySoft2:
		return 0x16, true
	default:
		return gvmhost.SKTNumericGuestCode(key)
	}
}

func (m *gnex32Machine) Framebuffer() image.Image {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtime.Frame()
}

func (m *gnex32Machine) GNEX32PresentCount() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtime.PresentCount()
}

func (*gnex32Machine) DrainAudio() machinecore.AudioChunk { return machinecore.AudioChunk{} }
func (*gnex32Machine) SaveState(io.Writer) error          { return ErrGVMStateUnavailable }
func (*gnex32Machine) LoadState(io.Reader) error          { return ErrGVMStateUnavailable }

func (m *gnex32Machine) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.state = machinecore.StateStopped
	m.runtime = nil
	return nil
}

func (m *gnex32Machine) SourceInfo() machinecore.Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.source
}
