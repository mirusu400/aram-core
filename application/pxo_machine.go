package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/mirusu400/aram-core/application/internal/guest"
	pxort "github.com/mirusu400/aram-core/application/internal/pxo"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader"
	pxoloader "github.com/mirusu400/aram-core/loader/pxo"
	"github.com/mirusu400/aram-core/profile"
)

const (
	pxoInitBudget  = uint64(100_000_000)
	pxoFrameBudget = uint64(50_000_000)
)

type pxoMachine struct {
	mu         sync.Mutex
	state      machinecore.State
	source     machinecore.Source
	pkg        *pxoloader.Package
	newCPU     CPUFactory
	backend    cpu.Backend
	runtime    *pxort.Runtime
	host       *pxort.Host
	frame      *image.RGBA
	budget     uint64
	gameActive bool
	closed     bool
}

func (f Factory) createPXOMachine(ctx context.Context, source machinecore.Source) (machinecore.Machine, bool, error) {
	if source.Format != string(loader.KindPXO) {
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
	pkg, err := pxoloader.Inspect(data)
	if err != nil {
		return nil, true, err
	}
	newCPU := f.NewCPU
	if newCPU == nil {
		newCPU = func() cpu.Backend { return interpreter.New() }
	}
	backend, runtime, host, err := newPXORuntime(newCPU, pkg)
	if err != nil {
		return nil, true, err
	}
	source.SHA256 = actual
	source.Format = string(loader.KindPXO)
	budget := f.PXOFrameRunBudget
	if budget == 0 {
		budget = pxoFrameBudget
	}
	return &pxoMachine{
		state:   machinecore.StateReady,
		source:  source,
		pkg:     pkg,
		newCPU:  newCPU,
		backend: backend,
		runtime: runtime,
		host:    host,
		budget:  budget,
	}, true, nil
}

func newPXORuntime(newCPU CPUFactory, pkg *pxoloader.Package) (cpu.Backend, *pxort.Runtime, *pxort.Host, error) {
	backend := newCPU()
	if backend == nil {
		return nil, nil, nil, machinecore.ErrBackendUnavailable
	}
	host := pxort.NewHost(backend)
	runtime, err := pxort.NewRuntime(backend, pkg, host)
	if err != nil {
		_ = backend.Close()
		return nil, nil, nil, err
	}
	return backend, runtime, host, nil
}

func (m *pxoMachine) Load(context.Context, machinecore.Source) error {
	return fmt.Errorf("load from %s: %w", m.State(), ErrInvalidState)
}

func (m *pxoMachine) State() machinecore.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *pxoMachine) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("PXO1 machine is closed")
	}
	if m.state != machinecore.StateReady {
		return fmt.Errorf("start from %s: %w", m.state, ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := m.runtime.CallEvent(ctx, 1, 0, 0, pxoInitBudget); err != nil {
		return m.fault("start PXO1 application", err)
	}
	if err := m.callHandler(ctx, 1, []uint32{1, 1, 0}, pxoInitBudget); err != nil {
		return m.fault("initialize PXO1 launcher", err)
	}
	m.drawLauncher()
	m.state = machinecore.StateRunning
	return nil
}

func (m *pxoMachine) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("pause from %s: %w", m.state, ErrInvalidState)
	}
	m.state = machinecore.StatePaused
	return nil
}

func (m *pxoMachine) Resume() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StatePaused {
		return fmt.Errorf("resume from %s: %w", m.state, ErrInvalidState)
	}
	m.state = machinecore.StateRunning
	return nil
}

func (m *pxoMachine) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == machinecore.StateEmpty {
		return fmt.Errorf("stop from %s: %w", m.state, ErrInvalidState)
	}
	m.state = machinecore.StateStopped
	return nil
}

func (m *pxoMachine) Reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("PXO1 machine is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backend, runtime, host, err := newPXORuntime(m.newCPU, m.pkg)
	if err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	old := m.backend
	m.backend, m.runtime, m.host = backend, runtime, host
	m.frame = nil
	m.gameActive = false
	m.state = machinecore.StateReady
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (m *pxoMachine) StepFrame(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("step frame from %s: %w", m.state, ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !m.gameActive {
		m.drawLauncher()
		return nil
	}
	if err := m.callHandler(ctx, 2, []uint32{5, 1, 1}, m.budget); err != nil {
		return m.fault("step PXO1 game", err)
	}
	if err := m.syncFramebuffer(); err != nil {
		return m.fault("read PXO1 framebuffer", err)
	}
	return nil
}

func (m *pxoMachine) QueueInput(event machinecore.InputEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("PXO1 input from %s: %w", m.state, ErrInvalidState)
	}
	if !event.Pressed {
		return nil
	}
	key, known := guest.InputKeyCode(event.Control)
	if !known {
		return ErrGVMInputUnavailable
	}
	code, supported := pxoGuestCode(key)
	if !supported {
		return ErrGVMInputUnavailable
	}
	ctx := context.Background()
	handler := uint32(1)
	if m.gameActive {
		handler = 2
	}
	if err := m.callHandler(ctx, handler, []uint32{3, code, 0}, m.budget); err != nil {
		return m.fault(fmt.Sprintf("deliver PXO1 key %q", event.Control), err)
	}
	if !m.gameActive && m.host.Active() == 2 {
		if err := m.callHandler(ctx, 2, []uint32{1, 1, 0}, pxoInitBudget); err != nil {
			return m.fault("initialize PXO1 game", err)
		}
		m.gameActive = true
		if err := m.callHandler(ctx, 2, []uint32{5, 1, 1}, pxoInitBudget); err != nil {
			return m.fault("render first PXO1 game frame", err)
		}
		if err := m.syncFramebuffer(); err != nil {
			return m.fault("read first PXO1 framebuffer", err)
		}
		return nil
	}
	if m.gameActive {
		if err := m.syncFramebuffer(); err != nil {
			return m.fault("read PXO1 framebuffer after input", err)
		}
	} else {
		m.drawLauncher()
	}
	return nil
}

func pxoGuestCode(key profile.KeyCode) (uint32, bool) {
	switch key {
	case profile.KeyLeft:
		return 0, true
	case profile.KeyRight:
		return 1, true
	case profile.KeyUp:
		return 2, true
	case profile.KeyDown:
		return 3, true
	case profile.KeySelect:
		return 0x14, true
	case profile.KeyEnd:
		return 0x0e, true
	case profile.KeyClear:
		return 0x0f, true
	case profile.KeySoft1:
		return 0x1c, true
	case profile.KeySoft2:
		return 0x1d, true
	case profile.Key0, profile.Key1, profile.Key2, profile.Key3, profile.Key4,
		profile.Key5, profile.Key6, profile.Key7, profile.Key8, profile.Key9:
		return uint32(key), true
	case profile.KeyAsterisk, profile.KeyPound:
		return uint32(key), true
	default:
		return 0, false
	}
}

func (m *pxoMachine) callHandler(ctx context.Context, id uint32, args []uint32, budget uint64) error {
	callback := m.host.Handler(id)
	if callback == 0 {
		return fmt.Errorf("PXO1 handler %d is not registered", id)
	}
	_, err := m.runtime.CallFunction(ctx, callback, args, budget)
	return err
}

func (m *pxoMachine) syncFramebuffer() error {
	if !m.host.FramebufferMapped() {
		return fmt.Errorf("PXO1 game did not map a framebuffer")
	}
	pixels := make([]byte, pxort.FramebufferByteSize)
	if err := m.backend.ReadMemory(pxort.FramebufferBase, pixels); err != nil {
		return err
	}
	frame := image.NewRGBA(image.Rect(0, 0, pxort.FramebufferWidth, pxort.FramebufferHeight))
	for index := 0; index < pxort.FramebufferWidth*pxort.FramebufferHeight; index++ {
		value := uint16(pixels[index*2]) | uint16(pixels[index*2+1])<<8
		red := uint8((value >> 11) & 0x1f)
		green := uint8((value >> 5) & 0x3f)
		blue := uint8(value & 0x1f)
		frame.Pix[index*4+0] = red<<3 | red>>2
		frame.Pix[index*4+1] = green<<2 | green>>4
		frame.Pix[index*4+2] = blue<<3 | blue>>2
		frame.Pix[index*4+3] = 0xff
	}
	m.frame = frame
	return nil
}

func (m *pxoMachine) drawLauncher() {
	frame := image.NewRGBA(image.Rect(0, 0, pxort.FramebufferWidth, pxort.FramebufferHeight))
	draw.Draw(frame, frame.Bounds(), &image.Uniform{C: color.RGBA{R: 8, G: 18, B: 44, A: 255}}, image.Point{}, draw.Src)
	drawer := &font.Drawer{Dst: frame, Src: image.White, Face: basicfont.Face7x13}
	drawer.Dot = fixed.P(30, 45)
	drawer.DrawString("LG C260 GAMES")
	items := []string{"SANAKAN", "3D BLOCK"}
	for index, item := range items {
		prefix := "  "
		if uint32(index) == m.host.Selection() {
			prefix = "> "
		}
		drawer.Dot = fixed.P(42, 90+index*30)
		drawer.DrawString(prefix + item)
	}
	drawer.Dot = fixed.P(28, 190)
	drawer.DrawString("UP/DOWN + SELECT")
	m.frame = frame
}

func (m *pxoMachine) fault(operation string, err error) error {
	m.state = machinecore.StateFaulted
	return fmt.Errorf("%s: %w", operation, err)
}

func (m *pxoMachine) Framebuffer() image.Image {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.frame
}

func (m *pxoMachine) PXO1PresentCount() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Product diagnostics read the machine outside the backend lock, so a
	// concurrent Close may already have released the host.
	if m.host == nil {
		return 0
	}
	return m.host.Presents()
}

func (*pxoMachine) DrainAudio() machinecore.AudioChunk { return machinecore.AudioChunk{} }
func (*pxoMachine) SaveState(io.Writer) error          { return ErrGVMStateUnavailable }
func (*pxoMachine) LoadState(io.Reader) error          { return ErrGVMStateUnavailable }

func (m *pxoMachine) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	m.state = machinecore.StateStopped
	m.runtime = nil
	m.host = nil
	if m.backend != nil {
		err := m.backend.Close()
		m.backend = nil
		return err
	}
	return nil
}

func (m *pxoMachine) SourceInfo() machinecore.Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.source
}
