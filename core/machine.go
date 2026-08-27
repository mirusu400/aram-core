package core

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"strings"
	"time"
)

var ErrBackendUnavailable = errors.New("emulation backend unavailable")

const MaxControlNameBytes = 255

type State uint8

const (
	StateEmpty State = iota
	StateReady
	StateRunning
	StatePaused
	StateStopped
	StateFaulted
)

func (s State) String() string {
	switch s {
	case StateEmpty:
		return "empty"
	case StateReady:
		return "ready"
	case StateRunning:
		return "running"
	case StatePaused:
		return "paused"
	case StateStopped:
		return "stopped"
	case StateFaulted:
		return "faulted"
	default:
		return "unknown"
	}
}

func (s State) Valid() bool {
	return s >= StateEmpty && s <= StateFaulted
}

type Source struct {
	Name      string
	Path      string
	Format    string
	SHA256    string
	ProfileID string
	ReaderAt  io.ReaderAt
	Size      int64
}

func (s Source) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("source name is empty")
	}
	if s.ReaderAt == nil {
		return fmt.Errorf("source %q has no random-access reader", s.Name)
	}
	if s.Size <= 0 {
		return fmt.Errorf("source %q has invalid size %d", s.Name, s.Size)
	}
	if s.SHA256 != "" {
		if len(s.SHA256) != 64 {
			return fmt.Errorf("source %q SHA-256 must contain 64 hexadecimal characters", s.Name)
		}
		if _, err := hex.DecodeString(s.SHA256); err != nil {
			return fmt.Errorf("source %q has invalid SHA-256: %w", s.Name, err)
		}
	}
	return nil
}

type InputEvent struct {
	Control string
	Pressed bool
	At      time.Duration
}

func (e InputEvent) Validate() error {
	if strings.TrimSpace(e.Control) == "" {
		return fmt.Errorf("input control is empty")
	}
	if len(e.Control) > MaxControlNameBytes {
		return fmt.Errorf("input control exceeds %d bytes", MaxControlNameBytes)
	}
	if strings.IndexByte(e.Control, 0) >= 0 {
		return fmt.Errorf("input control contains NUL")
	}
	if e.At < 0 {
		return fmt.Errorf("input event time %s is negative", e.At)
	}
	return nil
}

type AudioChunk struct {
	SampleRate int
	Channels   int
	PCM16      []int16
	// StartGuestNS is the guest-monotonic time of the first PCM frame.
	StartGuestNS int64
	// StartSample is the first output-frame cursor within Generation. It may
	// jump when the guest produced no audible samples for part of its timeline.
	StartSample uint64
	// Generation changes whenever reset/load-state or another discontinuity
	// invalidates audio already buffered by a host.
	Generation uint64
}

func (c AudioChunk) Validate() error {
	if c.SampleRate == 0 && c.Channels == 0 && len(c.PCM16) == 0 {
		return nil
	}
	if c.SampleRate <= 0 {
		return fmt.Errorf("invalid audio sample rate %d", c.SampleRate)
	}
	if c.Channels <= 0 {
		return fmt.Errorf("invalid audio channel count %d", c.Channels)
	}
	if len(c.PCM16)%c.Channels != 0 {
		return fmt.Errorf(
			"audio sample count %d is not divisible by %d channels",
			len(c.PCM16),
			c.Channels,
		)
	}
	if c.StartGuestNS < 0 {
		return fmt.Errorf("invalid audio guest timestamp %d", c.StartGuestNS)
	}
	return nil
}

// VideoPresentation is an immutable guest frame anchored to the same timeline
// generation used by AudioChunk.
type VideoPresentation struct {
	Image      image.Image
	Sequence   uint64
	GuestNS    int64
	Generation uint64
}

type Machine interface {
	Load(context.Context, Source) error
	State() State
	Start(context.Context) error
	Pause() error
	Resume() error
	Stop() error
	Reset(context.Context) error
	StepFrame(context.Context) error
	QueueInput(InputEvent) error
	Framebuffer() image.Image
	DrainAudio() AudioChunk
	SaveState(io.Writer) error
	LoadState(io.Reader) error
	Close() error
}

type Factory interface {
	Create(context.Context, Source) (Machine, error)
}

type UnavailableFactory struct{}

func (UnavailableFactory) Create(context.Context, Source) (Machine, error) {
	return nil, ErrBackendUnavailable
}
