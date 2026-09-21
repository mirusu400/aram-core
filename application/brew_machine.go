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
	maxBREWArchiveSize = int64(128 << 20)

	// Qualcomm AEEVCodes.h starts handset virtual keys at AEE_AVK_BASE
	// (0xe020). Keep these values explicit: applets compare EVT_KEY wParam
	// against the AVK constants, not small keypad ordinals.
	brewAVK0      = uint32(0xe021)
	brewAVKStar   = uint32(0xe02b)
	brewAVKPound  = uint32(0xe02c)
	brewAVKEnd    = uint32(0xe02e)
	brewAVKSend   = uint32(0xe02f)
	brewAVKClear  = uint32(0xe030)
	brewAVKUp     = uint32(0xe031)
	brewAVKDown   = uint32(0xe032)
	brewAVKLeft   = uint32(0xe033)
	brewAVKRight  = uint32(0xe034)
	brewAVKSelect = uint32(0xe035)
	brewAVKSoft1  = uint32(0xe036)
	brewAVKSoft2  = uint32(0xe037)
	brewAVKMenu   = uint32(0xe03e)
)

// brewMachine executes bounded BREW packages through the portable runtime.
// Package artwork remains diagnostic-only; published frames come from the guest
// RGB565 surface after IDisplay::Update.
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
	if err := source.Validate(); err != nil || source.Size <= 0 || source.Size > maxBREWArchiveSize {
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
	if !pkg.Authenticated && !f.AllowUntrustedBREW {
		return nil, false, nil
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
			return fmt.Errorf("bootstrap BREW module: %w", err)
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
		if !event.Pressed {
			// BREW delivers EVT_KEY after a key is pressed/held and again
			// immediately before EVT_KEY_RELEASE. Some applets handle only
			// EVT_KEY, so a short tap must still produce that event.
			if _, err := m.runtime.DispatchEvent(ctx, 0x100, key, 0); err != nil {
				return m.executionErrorLocked(fmt.Sprintf("dispatch BREW key %q", event.Control), err)
			}
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
		return brewAVKUp, true
	case "down":
		return brewAVKDown, true
	case "left":
		return brewAVKLeft, true
	case "right":
		return brewAVKRight, true
	case "fire", "select", "ok":
		return brewAVKSelect, true
	case "soft-left":
		return brewAVKSoft1, true
	case "soft-right":
		return brewAVKSoft2, true
	case "menu":
		return brewAVKMenu, true
	case "back", "clear":
		return brewAVKClear, true
	case "send":
		return brewAVKSend, true
	case "end":
		return brewAVKEnd, true
	case "star":
		return brewAVKStar, true
	case "hash", "pound":
		return brewAVKPound, true
	case "num0", "num1", "num2", "num3", "num4", "num5", "num6", "num7", "num8", "num9":
		return brewAVK0 + uint32(control[len(control)-1]-'0'), true
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
