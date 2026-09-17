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

	"github.com/mirusu400/aram-core/application/internal/brewrt"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

const maxBREWInputEvents = 1024

// brewMachine is intentionally a bootstrap machine, not a general BREW host.
// It executes the authenticated module and applet factories to the first exact
// ABI boundary. Its package splash is diagnostic output, never claimed as a
// guest-rendered game frame. Input transitions are retained for that future ABI.
type brewMachine struct {
	mu      sync.Mutex
	state   machinecore.State
	source  machinecore.Source
	pkg     brewrt.Package
	runtime *brewrt.Runtime
	frame   *image.RGBA
	input   []machinecore.InputEvent
	closed  bool
}

func (f Factory) createBREWMachine(ctx context.Context, source machinecore.Source) (machinecore.Machine, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := source.Validate(); err != nil || source.Size > maxApplicationSize {
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
	frame := image.NewRGBA(image.Rect(0, 0, 240, 320))
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
	if err := m.runtime.Bootstrap(ctx); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("bootstrap authenticated BREW module: %w", err)
	}
	m.renderSplash()
	m.state = machinecore.StatePaused
	if err := m.runtime.ProbeAppletBoundary(ctx); err != nil {
		var boundary *brewrt.ExecutionBoundaryError
		if !errors.As(err, &boundary) {
			m.state = machinecore.StateFaulted
		}
		return err
	}
	return fmt.Errorf("BREW applet probe stopped without an execution boundary")
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
	draw.Draw(m.frame, m.frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	m.state = machinecore.StateReady
	return nil
}

func (m *brewMachine) StepFrame(ctx context.Context) error {
	return m.Start(ctx)
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
