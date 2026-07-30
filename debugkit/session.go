// Package debugkit provides a headless, scriptable debugger around core.Machine.
//
// It intentionally depends only on the public machine contract so runtime
// implementations can evolve independently from debugging tools.
package debugkit

import (
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

const (
	DefaultFrameDuration = 16 * time.Millisecond
	MaxStepFrames        = 1_000_000
)

type Options struct {
	FrameDuration time.Duration
	// Diagnostics exposes immutable runtime-specific information without
	// coupling debugkit to a concrete machine implementation.
	Diagnostics func() map[string]any
}

type Session struct {
	mu            sync.Mutex
	machine       machinecore.Machine
	frameDuration time.Duration
	diagnostics   func() map[string]any
	frame         uint64
	elapsed       time.Duration
}

type Status struct {
	State     string         `json:"state"`
	Frame     uint64         `json:"frame"`
	ElapsedMS int64          `json:"elapsed_ms"`
	Screen    ScreenReport   `json:"screen"`
	CPU       *CPUReport     `json:"cpu,omitempty"`
	Runtime   map[string]any `json:"runtime,omitempty"`
}

func New(machine machinecore.Machine, options Options) (*Session, error) {
	if machine == nil {
		return nil, fmt.Errorf("debug session machine is nil")
	}
	frameDuration := options.FrameDuration
	if frameDuration == 0 {
		frameDuration = DefaultFrameDuration
	}
	if frameDuration < 0 {
		return nil, fmt.Errorf("debug frame duration %s is negative", frameDuration)
	}
	return &Session{
		machine:       machine,
		frameDuration: frameDuration,
		diagnostics:   options.Diagnostics,
	}, nil
}

func (s *Session) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machine.Start(ctx)
}

func (s *Session) Step(ctx context.Context, count int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stepLocked(ctx, count)
}

func (s *Session) stepLocked(ctx context.Context, count int) error {
	if count < 0 || count > MaxStepFrames {
		return fmt.Errorf("step frame count %d is outside 0..%d", count, MaxStepFrames)
	}
	for index := 0; index < count; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.machine.StepFrame(ctx); err != nil {
			return fmt.Errorf("step frame %d: %w", s.frame+1, err)
		}
		s.frame++
		s.elapsed += s.frameDuration
	}
	return nil
}

func (s *Session) KeyDown(control string) error {
	return s.queueKey(control, true)
}

func (s *Session) KeyUp(control string) error {
	return s.queueKey(control, false)
}

func (s *Session) queueKey(control string, pressed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queueKeyLocked(control, pressed)
}

func (s *Session) queueKeyLocked(control string, pressed bool) error {
	event := machinecore.InputEvent{
		Control: control,
		Pressed: pressed,
		At:      s.elapsed,
	}
	if err := s.machine.QueueInput(event); err != nil {
		action := "release"
		if pressed {
			action = "press"
		}
		return fmt.Errorf("%s key %q: %w", action, control, err)
	}
	return nil
}

// Tap queues a key press, advances holdFrames, queues the release, and advances
// one more frame so runtimes that consume one input event per frame observe both
// edges.
func (s *Session) Tap(ctx context.Context, control string, holdFrames int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if holdFrames < 1 || holdFrames >= MaxStepFrames {
		return fmt.Errorf("tap hold frame count %d is outside 1..%d", holdFrames, MaxStepFrames-1)
	}
	if err := s.queueKeyLocked(control, true); err != nil {
		return err
	}
	if err := s.stepLocked(ctx, holdFrames); err != nil {
		_ = s.queueKeyLocked(control, false)
		return err
	}
	if err := s.queueKeyLocked(control, false); err != nil {
		return err
	}
	return s.stepLocked(ctx, 1)
}

func (s *Session) Reset(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.machine.Reset(ctx); err != nil {
		return err
	}
	s.frame = 0
	s.elapsed = 0
	return nil
}

func (s *Session) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machine.Stop()
}

func (s *Session) Snapshot() image.Image {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machine.Framebuffer()
}

func (s *Session) Screen() ScreenReport {
	return InspectScreen(s.Snapshot())
}

func (s *Session) Pixel(x, y int) (Pixel, error) {
	frame := s.Snapshot()
	if frame == nil {
		return Pixel{}, fmt.Errorf("machine returned a nil framebuffer")
	}
	if !image.Pt(x, y).In(frame.Bounds()) {
		return Pixel{}, fmt.Errorf("pixel (%d,%d) is outside %v", x, y, frame.Bounds())
	}
	return pixelAt(frame, x, y), nil
}

func (s *Session) Screenshot(path string) (ScreenReport, error) {
	frame := s.Snapshot()
	report := InspectScreen(frame)
	if err := WritePNG(path, frame); err != nil {
		return ScreenReport{}, err
	}
	return report, nil
}

func (s *Session) SaveState(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeFile(path, func(output io.Writer) error {
		return s.machine.SaveState(output)
	})
}

func (s *Session) LoadState(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	input, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open state %q: %w", path, err)
	}
	defer input.Close()
	if err := s.machine.LoadState(input); err != nil {
		return fmt.Errorf("load state %q: %w", path, err)
	}
	return nil
}

func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := Status{
		State:     s.machine.State().String(),
		Frame:     s.frame,
		ElapsedMS: s.elapsed.Milliseconds(),
		Screen:    InspectScreen(s.machine.Framebuffer()),
	}
	if report, err := s.cpuLocked(); err == nil {
		status.CPU = &report
	}
	status.Runtime = s.diagnosticsLocked()
	return status
}

func (s *Session) Diagnostics() (map[string]any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	diagnostics := s.diagnosticsLocked()
	return diagnostics, diagnostics != nil
}

func (s *Session) diagnosticsLocked() map[string]any {
	if s.diagnostics == nil {
		return nil
	}
	return s.diagnostics()
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machine.Close()
}

func writeFile(path string, write func(io.Writer) error) error {
	if path == "" {
		return fmt.Errorf("output path is empty")
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create output directory %q: %w", parent, err)
	}
	output, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	writeErr := write(output)
	closeErr := output.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return fmt.Errorf("close %q: %w", path, closeErr)
	}
	return nil
}
