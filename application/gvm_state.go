package application

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"io"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/application/internal/gvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/gvm"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	gvmMachineStateMagic = "ARAMGVM\x00"
	// Version 2 dropped the fixed-timer flag (opcode94 is vibration) and
	// added the palette blink phase. Version 1 states still load.
	gvmMachineStateVersion  = uint32(2)
	gvmMachineStateVersion1 = uint32(1)
	maxGVMMachineStateBytes = shared.MaxServicesStateBytes
)

type gvmTimerSlotState struct {
	Interval int16
	Selector uint16
	NextTick uint64
	Active   bool
}

type gvmTimerState struct {
	Mode     uint8
	Interval int16
	Selector uint16
	Reached  bool
	Frame    uint64
	Slots    [3]gvmTimerSlotState
}

func (s *gvmTimerBoundary) snapshotState() gvmTimerState {
	state := gvmTimerState{
		Mode: s.mode, Interval: s.interval, Selector: s.selector,
		Reached: s.reached, Frame: s.frame,
	}
	for index, slot := range s.slots {
		state.Slots[index] = gvmTimerSlotState{
			Interval: slot.interval, Selector: slot.selector,
			NextTick: slot.nextTick, Active: slot.active,
		}
	}
	return state
}

func (s *gvmTimerBoundary) restoreState(state gvmTimerState) error {
	if state.Mode >= uint8(len(state.Slots)) {
		return fmt.Errorf("invalid GVM timer mode %d", state.Mode)
	}
	for index, slot := range state.Slots {
		if slot.Active && slot.Interval < 10 {
			return fmt.Errorf("invalid active GVM timer slot %d", index)
		}
	}
	s.mode, s.interval, s.selector = state.Mode, state.Interval, state.Selector
	s.reached, s.frame = state.Reached, state.Frame
	for index, slot := range state.Slots {
		s.slots[index] = gvmTimerSlot{
			interval: slot.Interval, selector: slot.Selector,
			nextTick: slot.NextTick, active: slot.Active,
		}
	}
	return nil
}

type gvmDecodedMediaState struct {
	Registry        shared.RegistryState
	Media           shared.MediaState
	Events          shared.EventBusState
	Owner           shared.OwnerID
	AudioType       int32
	Revision        uint64
	RevisionEpochNS int64
}

func (s *gvmDecodedMediaServices) snapshotState() (gvmDecodedMediaState, error) {
	if s == nil || s.clock == nil || s.registry == nil || s.media == nil || s.events == nil {
		return gvmDecodedMediaState{}, fmt.Errorf("invalid GVM media service")
	}
	return gvmDecodedMediaState{
		Registry:        s.registry.Snapshot(),
		Media:           s.media.Snapshot(),
		Events:          s.events.Snapshot(),
		Owner:           s.owner,
		AudioType:       s.audioType,
		Revision:        s.revision,
		RevisionEpochNS: int64(s.revisionEpoch),
	}, nil
}

func (s *gvmDecodedMediaServices) restoreState(state gvmDecodedMediaState) error {
	if s == nil || s.clock == nil || state.Owner == 0 ||
		(state.AudioType != 5 && state.AudioType != 6) || state.RevisionEpochNS < 0 ||
		time.Duration(state.RevisionEpochNS) > s.clock.Monotonic() {
		return fmt.Errorf("invalid GVM media state")
	}
	registry := shared.NewRegistry(state.Registry.Limit)
	if err := registry.Restore(state.Registry); err != nil {
		return fmt.Errorf("restore GVM media registry: %w", err)
	}
	media, err := shared.NewMedia(registry, state.Media.Limits)
	if err != nil {
		return fmt.Errorf("restore GVM media: %w", err)
	}
	if err := media.Restore(state.Media); err != nil {
		return fmt.Errorf("restore GVM media: %w", err)
	}
	events := shared.NewEventBus(state.Events.MaxEvents, state.Events.MaxEventData)
	if err := events.Restore(state.Events); err != nil {
		return fmt.Errorf("restore GVM media events: %w", err)
	}
	s.registry, s.media, s.events = registry, media, events
	s.owner, s.audioType, s.revision = state.Owner, state.AudioType, state.Revision
	s.revisionEpoch = time.Duration(state.RevisionEpochNS)
	return nil
}

type gvmFrameState struct {
	Width    uint32
	Height   uint32
	RGBA     []byte
	Presents uint64
}

func (p *gvmFramePublisher) snapshotState() (gvmFrameState, error) {
	frame, presents := p.snapshot()
	if frame == nil {
		if presents != 0 {
			return gvmFrameState{}, fmt.Errorf("GVM frame count without a frame")
		}
		return gvmFrameState{}, nil
	}
	bounds := frame.Bounds()
	if bounds.Min != (image.Point{}) || bounds.Dx() <= 0 || bounds.Dy() <= 0 ||
		bounds.Dx() > 256 || bounds.Dy() > 256 {
		return gvmFrameState{}, fmt.Errorf("invalid GVM frame bounds %v", bounds)
	}
	state := gvmFrameState{
		Width: uint32(bounds.Dx()), Height: uint32(bounds.Dy()),
		RGBA: make([]byte, bounds.Dx()*bounds.Dy()*4), Presents: presents,
	}
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			pixel := color.RGBAModel.Convert(frame.At(x, y)).(color.RGBA)
			offset := (y*bounds.Dx() + x) * 4
			state.RGBA[offset], state.RGBA[offset+1] = pixel.R, pixel.G
			state.RGBA[offset+2], state.RGBA[offset+3] = pixel.B, pixel.A
		}
	}
	return state, nil
}

func (p *gvmFramePublisher) restoreState(state gvmFrameState, width, height int32) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if state.Width == 0 || state.Height == 0 {
		if state.Width != 0 || state.Height != 0 || state.RGBA != nil || state.Presents != 0 {
			return fmt.Errorf("invalid empty GVM frame state")
		}
		p.latest, p.presents = nil, 0
		return nil
	}
	if state.Width != uint32(width) || state.Height != uint32(height) ||
		uint64(len(state.RGBA)) != uint64(state.Width)*uint64(state.Height)*4 ||
		state.Presents == 0 {
		return fmt.Errorf("invalid GVM frame state")
	}
	pixels := make([]color.RGBA, int(state.Width*state.Height))
	for index := range pixels {
		offset := index * 4
		pixels[index] = color.RGBA{
			R: state.RGBA[offset], G: state.RGBA[offset+1],
			B: state.RGBA[offset+2], A: state.RGBA[offset+3],
		}
	}
	p.latest = immutableGVMStateFrame{width: int(state.Width), height: int(state.Height), pixels: pixels}
	p.presents = state.Presents
	return nil
}

type immutableGVMStateFrame struct {
	width  int
	height int
	pixels []color.RGBA
}

func (f immutableGVMStateFrame) ColorModel() color.Model { return color.RGBAModel }
func (f immutableGVMStateFrame) Bounds() image.Rectangle {
	return image.Rect(0, 0, f.width, f.height)
}
func (f immutableGVMStateFrame) At(x, y int) color.Color {
	if x < 0 || x >= f.width || y < 0 || y >= f.height {
		return color.RGBA{}
	}
	return f.pixels[y*f.width+x]
}

type gvmVolatileState struct {
	Lifecycle       machinecore.State
	VM              gvm.State
	Clock           shared.ClockState
	Random          shared.RandomState
	Timer           gvmTimerState
	Display         gvmhost.DisplayState
	Media           gvmDecodedMediaState
	Frame           gvmFrameState
	AudioGeneration uint64
	InputDispatches uint64
	LastInputCode   uint16
}

func (m *gvmMachine) SaveState(output io.Writer) error {
	if output == nil {
		return fmt.Errorf("save GVM state: writer is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if !m.operational {
		return ErrGVMStateUnavailable
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
	default:
		return fmt.Errorf("save GVM state from %s: %w", m.state, ErrInvalidState)
	}
	digest, err := hex.DecodeString(m.source.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("save GVM state: source SHA-256 is unavailable")
	}
	vmState, err := m.vm.Snapshot()
	if err != nil {
		return fmt.Errorf("save GVM VM: %w", err)
	}
	mediaState, err := m.mediaServices.snapshotState()
	if err != nil {
		return fmt.Errorf("save GVM audio: %w", err)
	}
	frameState, err := m.frames.snapshotState()
	if err != nil {
		return fmt.Errorf("save GVM frame: %w", err)
	}
	state := gvmVolatileState{
		Lifecycle: m.state, VM: vmState,
		Clock: m.clock.Snapshot(), Random: m.random.Snapshot(),
		Timer: m.timer.snapshotState(), Display: m.display.Snapshot(),
		Media: mediaState, Frame: frameState,
		AudioGeneration: m.audioGeneration,
		InputDispatches: m.inputDispatches, LastInputCode: m.lastInputCode,
	}
	payload, err := shared.MarshalStateComponent(state)
	if err != nil {
		return fmt.Errorf("encode GVM state: %w", err)
	}
	var encoded bytes.Buffer
	encoded.WriteString(gvmMachineStateMagic)
	var scalar [8]byte
	binary.LittleEndian.PutUint32(scalar[:4], gvmMachineStateVersion)
	encoded.Write(scalar[:4])
	encoded.Write(digest)
	binary.LittleEndian.PutUint64(scalar[:], uint64(len(payload)))
	encoded.Write(scalar[:])
	encoded.Write(payload)
	checksum := sha256.Sum256(encoded.Bytes())
	encoded.Write(checksum[:])
	if uint64(encoded.Len()) > maxGVMMachineStateBytes {
		return fmt.Errorf("save GVM state: state exceeds byte limit")
	}
	return guest.WriteFull(output, encoded.Bytes())
}

func (m *gvmMachine) LoadState(input io.Reader) error {
	if input == nil {
		return fmt.Errorf("load GVM state: reader is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if !m.operational {
		return ErrGVMStateUnavailable
	}
	if m.state == machinecore.StateRunning || m.state == machinecore.StateEmpty {
		return fmt.Errorf("load GVM state from %s: %w", m.state, ErrInvalidState)
	}
	data, err := io.ReadAll(io.LimitReader(input, int64(maxGVMMachineStateBytes)+1))
	if err != nil {
		return fmt.Errorf("read GVM state: %w", err)
	}
	if uint64(len(data)) > maxGVMMachineStateBytes {
		return fmt.Errorf("read GVM state: state exceeds byte limit")
	}
	state, err := m.parseGVMState(data)
	if err != nil {
		return err
	}
	candidate, err := m.newGVMStateCandidateLocked()
	if err != nil {
		return err
	}
	if err := candidate.clock.Restore(state.Clock); err != nil {
		return fmt.Errorf("load GVM clock: %w", err)
	}
	if err := candidate.random.Restore(state.Random); err != nil {
		return fmt.Errorf("load GVM random: %w", err)
	}
	if err := candidate.timer.restoreState(state.Timer); err != nil {
		return fmt.Errorf("load GVM timer: %w", err)
	}
	if err := candidate.display.Restore(state.Display); err != nil {
		return fmt.Errorf("load GVM display: %w", err)
	}
	if err := candidate.mediaServices.restoreState(state.Media); err != nil {
		return fmt.Errorf("load GVM audio: %w", err)
	}
	if err := candidate.vm.Restore(state.VM); err != nil {
		return fmt.Errorf("load GVM VM: %w", err)
	}
	if err := candidate.frames.restoreState(state.Frame, candidate.width, candidate.height); err != nil {
		return fmt.Errorf("load GVM frame: %w", err)
	}

	m.vm, m.timer = candidate.vm, candidate.timer
	m.clock, m.random = candidate.clock, candidate.random
	m.display, m.mediaServices, m.frames = candidate.display, candidate.mediaServices, candidate.frames
	m.hostRequests = candidate.hostRequests
	m.requestIDs.nextSession()
	m.state = state.Lifecycle
	m.inputDispatches, m.lastInputCode = state.InputDispatches, state.LastInputCode
	m.lastResult, m.lastInputResult = cpu.Result{}, cpu.Result{}
	m.audioGeneration = state.AudioGeneration
	m.mediaOutputRevision = m.mediaServices.revision
	m.nextGVMAudioGenerationLocked(m.clock.Monotonic())
	return nil
}

func (m *gvmMachine) parseGVMState(data []byte) (gvmVolatileState, error) {
	minimum := len(gvmMachineStateMagic) + 4 + sha256.Size + 8 + sha256.Size
	if len(data) < minimum {
		return gvmVolatileState{}, fmt.Errorf("load GVM state: truncated header")
	}
	payloadWithHeader := data[:len(data)-sha256.Size]
	wantChecksum := data[len(payloadWithHeader):]
	checksum := sha256.Sum256(payloadWithHeader)
	if subtle.ConstantTimeCompare(wantChecksum, checksum[:]) != 1 {
		return gvmVolatileState{}, fmt.Errorf("load GVM state: checksum mismatch")
	}
	reader := bytes.NewReader(payloadWithHeader)
	magic := make([]byte, len(gvmMachineStateMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != gvmMachineStateMagic {
		return gvmVolatileState{}, fmt.Errorf("load GVM state: magic mismatch")
	}
	var scalar [8]byte
	if _, err := io.ReadFull(reader, scalar[:4]); err != nil {
		return gvmVolatileState{}, fmt.Errorf("load GVM state: unsupported version")
	}
	version := binary.LittleEndian.Uint32(scalar[:4])
	if version != gvmMachineStateVersion && version != gvmMachineStateVersion1 {
		return gvmVolatileState{}, fmt.Errorf("load GVM state: unsupported version")
	}
	sourceDigest := make([]byte, sha256.Size)
	if _, err := io.ReadFull(reader, sourceDigest); err != nil {
		return gvmVolatileState{}, fmt.Errorf("load GVM state: truncated source digest")
	}
	wantDigest, err := hex.DecodeString(m.source.SHA256)
	if err != nil || subtle.ConstantTimeCompare(sourceDigest, wantDigest) != 1 {
		return gvmVolatileState{}, fmt.Errorf("load GVM state: source SHA-256 mismatch")
	}
	if _, err := io.ReadFull(reader, scalar[:]); err != nil {
		return gvmVolatileState{}, fmt.Errorf("load GVM state: truncated payload size")
	}
	payloadSize := binary.LittleEndian.Uint64(scalar[:])
	if payloadSize > uint64(reader.Len()) || payloadSize != uint64(reader.Len()) {
		return gvmVolatileState{}, fmt.Errorf("load GVM state: invalid payload size")
	}
	payload := make([]byte, int(payloadSize))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return gvmVolatileState{}, fmt.Errorf("load GVM state: truncated payload")
	}
	var state gvmVolatileState
	if version == gvmMachineStateVersion1 {
		var legacy gvmVolatileStateV1
		if err := shared.UnmarshalStateComponent(payload, &legacy); err != nil {
			return gvmVolatileState{}, fmt.Errorf("decode GVM state: %w", err)
		}
		state = legacy.upgrade()
	} else if err := shared.UnmarshalStateComponent(payload, &state); err != nil {
		return gvmVolatileState{}, fmt.Errorf("decode GVM state: %w", err)
	}
	switch state.Lifecycle {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
	default:
		return gvmVolatileState{}, fmt.Errorf("load GVM state: invalid lifecycle %s", state.Lifecycle)
	}
	if state.AudioGeneration == 0 {
		return gvmVolatileState{}, fmt.Errorf("load GVM state: invalid audio state")
	}
	return state, nil
}

func (m *gvmMachine) newGVMStateCandidateLocked() (*gvmMachine, error) {
	candidate := &gvmMachine{
		state: machinecore.StateReady, source: m.source,
		packageSGS: bytes.Clone(m.packageSGS), budget: m.budget,
		operational: true, endEntry: m.endEntry,
		eventEntry: m.eventEntry, inputEntry: m.inputEntry,
		persistent: m.persistent,
		width:      m.width, height: m.height,
		northMediaSuffix: m.northMediaSuffix, nomMediaTail: m.nomMediaTail,
	}
	if err := candidate.resetVMLocked(); err != nil {
		return nil, fmt.Errorf("initialize GVM state candidate: %w", err)
	}
	return candidate, nil
}

// gvmVolatileStateV1 is the version 1 layout. Its fixed-timer flag recorded
// opcode94, which is vibration and never fed the guest timers, so upgrading
// drops it; the palette starts at the steady phase.
type gvmVolatileStateV1 struct {
	Lifecycle machinecore.State
	VM        gvm.State
	Clock     shared.ClockState
	Random    shared.RandomState
	Timer     struct {
		Mode     uint8
		Interval int16
		Selector uint16
		Reached  bool
		Fixed    bool
		Frame    uint64
		Slots    [3]gvmTimerSlotState
	}
	Display struct {
		Width     uint32
		Height    uint32
		Drawing   []byte
		Auxiliary []byte
		Mapping   uint8
		Selector  uint8
	}
	Media           gvmDecodedMediaState
	Frame           gvmFrameState
	AudioGeneration uint64
	InputDispatches uint64
	LastInputCode   uint16
}

func (s gvmVolatileStateV1) upgrade() gvmVolatileState {
	return gvmVolatileState{
		Lifecycle: s.Lifecycle, VM: s.VM, Clock: s.Clock, Random: s.Random,
		Timer: gvmTimerState{
			Mode: s.Timer.Mode, Interval: s.Timer.Interval, Selector: s.Timer.Selector,
			Reached: s.Timer.Reached, Frame: s.Timer.Frame, Slots: s.Timer.Slots,
		},
		Display: gvmhost.DisplayState{
			Width: s.Display.Width, Height: s.Display.Height,
			Drawing: s.Display.Drawing, Auxiliary: s.Display.Auxiliary,
			Mapping: s.Display.Mapping, Selector: s.Display.Selector,
		},
		Media: s.Media, Frame: s.Frame, AudioGeneration: s.AudioGeneration,
		InputDispatches: s.InputDispatches, LastInputCode: s.LastInputCode,
	}
}
