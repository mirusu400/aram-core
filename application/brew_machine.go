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

	"github.com/mirusu400/aram-core/application/internal/brewrt"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

const (
	maxBREWInputEvents = 1024
	brewFrameDuration  = 16 * time.Millisecond
)

// brewMachine is an exact-title host, not a general BREW implementation. It
// executes the authenticated module, applet, timers and researched service
// contracts. Its package splash remains diagnostic-only; published frames come
// from the guest RGB565 surface after IDisplay::Update.
type brewMachine struct {
	mu         sync.Mutex
	state      machinecore.State
	source     machinecore.Source
	pkg        brewrt.Package
	runtime    *brewrt.Runtime
	frame      *image.RGBA
	input      []machinecore.InputEvent
	started    bool
	guestFrame bool
	now        time.Duration
	closed     bool
}

func (f Factory) createBREWMachine(ctx context.Context, source machinecore.Source) (machinecore.Machine, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := source.Validate(); err != nil || source.Size != brewrt.ArchiveSize {
		return nil, false, nil
	}
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, brewrt.ArchiveSHA256) {
		return nil, false, nil
	}
	data, err := io.ReadAll(io.NewSectionReader(source.ReaderAt, 0, source.Size))
	if err != nil {
		return nil, false, nil
	}
	if int64(len(data)) != source.Size {
		return nil, false, nil
	}
	pkg, matched, err := brewrt.Match(data)
	if !matched || err != nil {
		return nil, matched, err
	}
	digest := sha256.Sum256(data)
	actualSHA := hex.EncodeToString(digest[:])
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, actualSHA) {
		return nil, true, fmt.Errorf("load %q: SHA-256 mismatch: expected %s, got %s", source.Name, source.SHA256, actualSHA)
	}
	source.SHA256 = actualSHA
	return newBREWMachine(source, pkg), true, nil
}

func newBREWMachine(source machinecore.Source, pkg brewrt.Package) *brewMachine {
	frame := image.NewRGBA(image.Rect(0, 0, 120, 160))
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	return &brewMachine{state: machinecore.StateReady, source: source, pkg: pkg, frame: frame}
}

func (m *brewMachine) Load(ctx context.Context, source machinecore.Source) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state != machinecore.StateEmpty {
		return fmt.Errorf("load from %s: %w", m.state, ErrInvalidState)
	}
	return fmt.Errorf("load BREW bootstrap directly: %w", ErrInvalidState)
}

func (m *brewMachine) State() machinecore.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *brewMachine) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.state != machinecore.StateReady && m.state != machinecore.StatePaused {
		return fmt.Errorf("start from %s: %w", m.state, ErrInvalidState)
	}
	if m.runtime == nil {
		runtime, err := brewrt.New(m.pkg)
		if err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
		m.runtime = runtime
	}
	m.state = machinecore.StateRunning
	if !m.started {
		if err := m.runtime.Bootstrap(ctx); err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("bootstrap authenticated BREW module: %w", err)
		}
		if err := m.runtime.ProbeAppletBoundary(ctx); err != nil {
			var boundary *brewrt.ExecutionBoundaryError
			if errors.As(err, &boundary) {
				m.state = machinecore.StatePaused
			} else {
				m.state = machinecore.StateFaulted
			}
			return err
		}
		handled, err := m.runtime.DispatchEvent(ctx, 0, 0, 0)
		if err != nil {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("dispatch BREW EVT_APP_START: %w", err)
		}
		if !handled {
			m.state = machinecore.StateFaulted
			return fmt.Errorf("BREW EVT_APP_START was not handled")
		}
		m.started = true
	}
	return m.stepLocked(ctx)
}

func (m *brewMachine) renderSplash() {
	draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	if m.pkg.Splash == nil {
		return
	}
	destination := image.Pt(
		(m.frame.Bounds().Dx()-m.pkg.Splash.Bounds().Dx())/2,
		(m.frame.Bounds().Dy()-m.pkg.Splash.Bounds().Dy())/2,
	)
	draw.Draw(m.frame, m.pkg.Splash.Bounds().Add(destination), m.pkg.Splash, m.pkg.Splash.Bounds().Min, draw.Src)
}

func (m *brewMachine) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state == machinecore.StatePaused {
		return nil
	}
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("pause from %s: %w", m.state, ErrInvalidState)
	}
	m.state = machinecore.StatePaused
	return nil
}

func (m *brewMachine) Resume() error { return m.Start(context.Background()) }

func (m *brewMachine) Stop() error {
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

func (m *brewMachine) Reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.runtime != nil {
		if err := m.runtime.Close(); err != nil {
			return err
		}
		m.runtime = nil
	}
	m.input = nil
	m.started = false
	m.guestFrame = false
	m.now = 0
	draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	m.state = machinecore.StateReady
	return nil
}

func (m *brewMachine) StepFrame(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.state != machinecore.StateRunning && m.state != machinecore.StatePaused {
		return fmt.Errorf("step frame from %s: %w", m.state, ErrInvalidState)
	}
	m.state = machinecore.StateRunning
	return m.stepLocked(ctx)
}

func (m *brewMachine) stepLocked(ctx context.Context) error {
	m.now += brewFrameDuration
	if err := m.runtime.RunCallbacks(ctx, brewFrameDuration); err != nil {
		return m.executionErrorLocked("run BREW timer callback", err)
	}
	due := dueBREWInputCount(m.input, m.now)
	for _, event := range m.input[:due] {
		key, ok := brewKeyCode(event.Control)
		if !ok {
			return fmt.Errorf("unsupported BREW control %q", event.Control)
		}
		kind := uint32(0x102)
		if event.Pressed {
			kind = 0x101
		}
		if _, err := m.runtime.DispatchEvent(ctx, kind, key, 0); err != nil {
			return m.executionErrorLocked(fmt.Sprintf("dispatch BREW input %q", event.Control), err)
		}
	}
	m.input = append(m.input[:0], m.input[due:]...)
	frame, presented, err := m.runtime.Framebuffer()
	if err != nil {
		m.state = machinecore.StateFaulted
		return err
	}
	if presented {
		m.frame = frame
		m.guestFrame = true
	}
	return nil
}

func dueBREWInputCount(input []machinecore.InputEvent, elapsed time.Duration) int {
	due := 0
	for due < len(input) && input[due].At <= elapsed {
		due++
	}
	return due
}

func (m *brewMachine) executionErrorLocked(operation string, err error) error {
	var boundary *brewrt.ExecutionBoundaryError
	if errors.As(err, &boundary) {
		m.state = machinecore.StatePaused
	} else {
		m.state = machinecore.StateFaulted
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func brewKeyCode(control string) (uint32, bool) {
	switch control {
	case "up":
		return 2, true
	case "down":
		return 3, true
	case "left":
		return 4, true
	case "right":
		return 5, true
	case "fire", "select":
		return 8, true
	default:
		return 0, false
	}
}

func (m *brewMachine) QueueInput(event machinecore.InputEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if len(m.input) >= maxBREWInputEvents {
		return fmt.Errorf("input queue is full")
	}
	index := sort.Search(len(m.input), func(index int) bool { return m.input[index].At > event.At })
	m.input = append(m.input, machinecore.InputEvent{})
	copy(m.input[index+1:], m.input[index:])
	m.input[index] = event
	return nil
}

func (m *brewMachine) Framebuffer() image.Image {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := image.NewRGBA(m.frame.Bounds())
	copy(snapshot.Pix, m.frame.Pix)
	return snapshot
}

// BREWFrameStats exposes guest presentation state to integration tooling
// without exposing the private runtime or treating a diagnostic image as a
// successful guest frame.
func (m *brewMachine) BREWFrameStats() (BREWFrameStats, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime == nil {
		return BREWFrameStats{}, false
	}
	presentCount, frameValid := m.runtime.FrameStats()
	return BREWFrameStats{PresentCount: presentCount, FrameValid: frameValid}, true
}

func (m *brewMachine) DrainAudio() machinecore.AudioChunk { return machinecore.AudioChunk{} }

func (m *brewMachine) SaveState(io.Writer) error {
	return fmt.Errorf("BREW bootstrap save state: %w", ErrUnsupportedSource)
}

func (m *brewMachine) LoadState(io.Reader) error {
	return fmt.Errorf("BREW bootstrap load state: %w", ErrUnsupportedSource)
}

func (m *brewMachine) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	if m.runtime != nil {
		return m.runtime.Close()
	}
	return nil
}

var _ machinecore.Machine = (*brewMachine)(nil)
