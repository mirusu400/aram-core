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

type HostEventKind string

const (
	HostEventNetworkReceive HostEventKind = "network-receive"
	HostEventSerialReceive  HostEventKind = "serial-receive"
	HostEventSMSReceive     HostEventKind = "sms-receive"
)

// HostEvent carries data produced by a handset-owned provider into a running
// guest. Network receive uses Data; serial and SMS notifications expose Words
// as the guest ABI's four event values.
type HostEvent struct {
	Kind  HostEventKind
	Data  []byte
	Words [4]uint32
}

func (e HostEvent) Validate() error {
	switch e.Kind {
	case HostEventNetworkReceive:
		if e.Words != ([4]uint32{}) {
			return fmt.Errorf("network receive event contains serial/SMS words")
		}
	case HostEventSerialReceive, HostEventSMSReceive:
		if len(e.Data) != 0 {
			return fmt.Errorf("%s event contains network data", e.Kind)
		}
	default:
		return fmt.Errorf("unknown host event kind %q", e.Kind)
	}
	return nil
}

// HostEventReceiver is an optional synchronous ingress interface. Implementers
// serialize delivery with guest execution and copy any data they retain.
type HostEventReceiver interface {
	ReceiveHostEvent(HostEvent) error
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
	// An empty PCM16 with a nonzero Generation is a discontinuity marker: the
	// host discards buffered audio from previous generations even when no new
	// samples are available. Its format and guest timestamp are valid. Producers
	// report a pending change once, either in new PCM or in an empty marker.
	// The zero-value chunk means that no audio or control change is available.
	Generation uint64
}

func (c AudioChunk) Validate() error {
	if c.SampleRate == 0 && c.Channels == 0 && len(c.PCM16) == 0 && c.Generation == 0 {
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

// VideoPresentationMachine is an optional zero-copy presentation interface.
// Sequence identifies immutable pixel content, while GuestNS and Generation
// let a frontend synchronize it with AudioChunk and discard stale timelines.
type VideoPresentationMachine interface {
	VideoPresentation() VideoPresentation
}

// SaveDataMachine exposes restart-persistent guest data independently from a
// complete volatile save state. Import is intended before guest execution
// resumes; implementations reject lifecycle states where an atomic replacement
// cannot be guaranteed.
type SaveDataMachine interface {
	ExportSaveData() ([]byte, error)
	ImportSaveData([]byte) error
}

// VibrationMachine exposes guest-requested haptic feedback to a host without
// making it part of the mandatory machine lifecycle.
type VibrationMachine interface {
	Vibration() (level uint8, remaining time.Duration)
}

type ExternalRequestKind string

const (
	ExternalRequestPhone   ExternalRequestKind = "phone"
	ExternalRequestSMS     ExternalRequestKind = "sms"
	ExternalRequestBrowser ExternalRequestKind = "browser"
)

// ExternalRequest is a guest-requested action that must be acknowledged by the
// host after it has been handled or deliberately ignored.
type ExternalRequest struct {
	// Sequence is an opaque, non-zero acknowledgement token. Hosts must return
	// a token obtained from ExternalRequests rather than synthesizing one.
	Sequence uint64
	GuestNS  int64
	Kind     ExternalRequestKind
	Target   string
	Data     []byte
}

// ExternalRequestMachine exposes an ordered, persistent host-action outbox.
// ExternalRequests returns a detached snapshot; ClearExternalRequests
// acknowledges every request through an observed Sequence token.
type ExternalRequestMachine interface {
	ExternalRequests() []ExternalRequest
	ClearExternalRequests(through uint64)
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
