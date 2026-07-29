package core

import (
	"context"
	"errors"
	"image"
	"io"
	"time"
)

var ErrBackendUnavailable = errors.New("emulation backend unavailable")

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

type Source struct {
	Name      string
	Path      string
	Format    string
	SHA256    string
	ProfileID string
	ReaderAt  io.ReaderAt
	Size      int64
}

type InputEvent struct {
	Control string
	Pressed bool
	At      time.Duration
}

type AudioChunk struct {
	SampleRate int
	Channels   int
	PCM16      []int16
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
