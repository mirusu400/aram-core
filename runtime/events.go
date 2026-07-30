package runtime

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type EventKind string

const (
	EventInputPress      EventKind = "input.press"
	EventInputRelease    EventKind = "input.release"
	EventInputRepeat     EventKind = "input.repeat"
	EventTimer           EventKind = "timer"
	EventRepaint         EventKind = "repaint"
	EventAudioComplete   EventKind = "audio.complete"
	EventNetworkReady    EventKind = "network.ready"
	EventStorageComplete EventKind = "storage.complete"
	EventSerialComplete  EventKind = "serial.complete"
	EventLifecycle       EventKind = "lifecycle"
	EventApplication     EventKind = "application"
)

func (k EventKind) Validate() error {
	switch k {
	case EventInputPress,
		EventInputRelease,
		EventInputRepeat,
		EventTimer,
		EventRepaint,
		EventAudioComplete,
		EventNetworkReady,
		EventStorageComplete,
		EventSerialComplete,
		EventLifecycle,
		EventApplication:
		return nil
	default:
		return fmt.Errorf("%w: invalid event kind %q", ErrInvalidArgument, k)
	}
}

// Event contains guest-neutral values only. An adapter translates Control,
// Name, and scalar fields into its own callback ABI or Java method call.
type Event struct {
	Sequence  uint64
	At        time.Duration
	Kind      EventKind
	Owner     OwnerID
	ServiceID ServiceID
	Control   string
	Name      string
	Value     int64
	Data      []byte
}

func (e Event) Validate(maxData int) error {
	if err := e.Kind.Validate(); err != nil {
		return err
	}
	if e.At < 0 || len(e.Control) > 255 || len(e.Name) > 255 ||
		strings.IndexByte(e.Control, 0) >= 0 ||
		strings.IndexByte(e.Name, 0) >= 0 {
		return fmt.Errorf("%w: invalid event fields", ErrInvalidArgument)
	}
	if len(e.Data) > maxData {
		return fmt.Errorf("%w: event payload is %d bytes, limit %d", ErrLimitExceeded, len(e.Data), maxData)
	}
	return nil
}

type EventBusState struct {
	MaxEvents    uint32
	MaxEventData uint32
	NextSequence uint64
	Events       []Event
}

// EventBus orders events by virtual timestamp and then enqueue sequence.
type EventBus struct {
	maxEvents    uint32
	maxEventData uint32
	nextSequence uint64
	events       []Event
}

func NewEventBus(maxEvents, maxEventData uint32) *EventBus {
	if maxEvents == 0 {
		maxEvents = DefaultMaxEvents
	}
	if maxEventData == 0 {
		maxEventData = DefaultMaxEventData
	}
	return &EventBus{
		maxEvents:    maxEvents,
		maxEventData: maxEventData,
		nextSequence: 1,
	}
}

func (b *EventBus) Len() int {
	return len(b.events)
}

func (b *EventBus) Enqueue(event Event) (uint64, error) {
	if err := event.Validate(int(b.maxEventData)); err != nil {
		return 0, err
	}
	if uint32(len(b.events)) >= b.maxEvents {
		return 0, fmt.Errorf("%w: event queue reached %d", ErrLimitExceeded, b.maxEvents)
	}
	if b.nextSequence == 0 || b.nextSequence == math.MaxUint64 {
		return 0, fmt.Errorf("%w: event sequence exhausted", ErrLimitExceeded)
	}
	event.Sequence = b.nextSequence
	b.nextSequence++
	event.Data = cloneBytes(event.Data)
	index := sort.Search(len(b.events), func(index int) bool {
		current := b.events[index]
		return current.At > event.At ||
			current.At == event.At && current.Sequence > event.Sequence
	})
	b.events = append(b.events, Event{})
	copy(b.events[index+1:], b.events[index:])
	b.events[index] = event
	return event.Sequence, nil
}

func (b *EventBus) Peek() (Event, bool) {
	if len(b.events) == 0 {
		return Event{}, false
	}
	event := b.events[0]
	event.Data = cloneBytes(event.Data)
	return event, true
}

func (b *EventBus) PopReady(now time.Duration) (Event, bool) {
	if len(b.events) == 0 || b.events[0].At > now {
		return Event{}, false
	}
	event := b.events[0]
	copy(b.events, b.events[1:])
	b.events = b.events[:len(b.events)-1]
	event.Data = cloneBytes(event.Data)
	return event, true
}

func (b *EventBus) RemoveService(id ServiceID) int {
	kept := b.events[:0]
	removed := 0
	for _, event := range b.events {
		if event.ServiceID == id {
			removed++
			continue
		}
		kept = append(kept, event)
	}
	clear(b.events[len(kept):])
	b.events = kept
	return removed
}

func (b *EventBus) Snapshot() EventBusState {
	state := EventBusState{
		MaxEvents:    b.maxEvents,
		MaxEventData: b.maxEventData,
		NextSequence: b.nextSequence,
		Events:       make([]Event, len(b.events)),
	}
	copy(state.Events, b.events)
	for index := range state.Events {
		state.Events[index].Data = cloneBytes(state.Events[index].Data)
	}
	return state
}

func (b *EventBus) Restore(state EventBusState) error {
	if state.MaxEvents == 0 || state.MaxEventData == 0 ||
		state.NextSequence == 0 || len(state.Events) > int(state.MaxEvents) {
		return fmt.Errorf("%w: invalid event bus limits", ErrInvalidState)
	}
	events := make([]Event, len(state.Events))
	sequences := make(map[uint64]struct{}, len(state.Events))
	var previous Event
	for index, event := range state.Events {
		_, duplicateSequence := sequences[event.Sequence]
		if event.Sequence == 0 || event.Sequence >= state.NextSequence ||
			duplicateSequence ||
			event.Validate(int(state.MaxEventData)) != nil ||
			(index != 0 && (event.At < previous.At ||
				event.At == previous.At && event.Sequence <= previous.Sequence)) {
			return fmt.Errorf("%w: invalid queued event %d", ErrInvalidState, index)
		}
		sequences[event.Sequence] = struct{}{}
		event.Data = cloneBytes(event.Data)
		events[index] = event
		previous = event
	}
	b.maxEvents = state.MaxEvents
	b.maxEventData = state.MaxEventData
	b.nextSequence = state.NextSequence
	b.events = events
	return nil
}

type InputState struct {
	MaxControls    uint32
	RepeatDelayNS  int64
	RepeatPeriodNS int64
	Focused        bool
	Controls       []InputControlState
}

type InputControlState struct {
	Name       string
	Pressed    bool
	NextRepeat int64
}

type inputControl struct {
	pressed    bool
	nextRepeat time.Duration
}

// Input tracks held controls and emits normalized press/release/repeat events.
type Input struct {
	maxControls  uint32
	repeatDelay  time.Duration
	repeatPeriod time.Duration
	focused      bool
	controls     map[string]inputControl
}

func NewInput(maxControls uint32, repeatDelay, repeatPeriod time.Duration) *Input {
	if maxControls == 0 {
		maxControls = 128
	}
	if repeatDelay <= 0 {
		repeatDelay = 500 * time.Millisecond
	}
	if repeatPeriod <= 0 {
		repeatPeriod = 50 * time.Millisecond
	}
	return &Input{
		maxControls:  maxControls,
		repeatDelay:  repeatDelay,
		repeatPeriod: repeatPeriod,
		focused:      true,
		controls:     make(map[string]inputControl),
	}
}

func (i *Input) SetFocus(focused bool) {
	i.focused = focused
}

func (i *Input) Held(control string) bool {
	return i.controls[control].pressed
}

func (i *Input) Change(bus *EventBus, owner OwnerID, control string, pressed bool, at time.Duration) error {
	if bus == nil || strings.TrimSpace(control) == "" || len(control) > 255 ||
		strings.IndexByte(control, 0) >= 0 || at < 0 {
		return fmt.Errorf("%w: invalid input change", ErrInvalidArgument)
	}
	current, exists := i.controls[control]
	if !exists && uint32(len(i.controls)) >= i.maxControls {
		return fmt.Errorf("%w: input controls reached %d", ErrLimitExceeded, i.maxControls)
	}
	if current.pressed == pressed {
		return nil
	}
	current.pressed = pressed
	if pressed {
		if at > time.Duration(math.MaxInt64-int64(i.repeatDelay)) {
			return fmt.Errorf("%w: input repeat deadline overflow", ErrLimitExceeded)
		}
		current.nextRepeat = at + i.repeatDelay
	} else {
		current.nextRepeat = 0
	}
	if !i.focused {
		i.controls[control] = current
		return nil
	}
	kind := EventInputRelease
	if pressed {
		kind = EventInputPress
	}
	if _, err := bus.Enqueue(Event{
		At:      at,
		Kind:    kind,
		Owner:   owner,
		Control: control,
	}); err != nil {
		return err
	}
	i.controls[control] = current
	return nil
}

func (i *Input) Advance(bus *EventBus, owner OwnerID, now time.Duration) error {
	if bus == nil || now < 0 {
		return fmt.Errorf("%w: invalid input advance", ErrInvalidArgument)
	}
	if !i.focused {
		return nil
	}
	busBefore := bus.Snapshot()
	inputBefore := i.Snapshot()
	rollback := func(err error) error {
		_ = bus.Restore(busBefore)
		_ = i.Restore(inputBefore)
		return err
	}
	names := make([]string, 0, len(i.controls))
	for name, control := range i.controls {
		if control.pressed && control.nextRepeat <= now {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		control := i.controls[name]
		for control.nextRepeat <= now {
			if _, err := bus.Enqueue(Event{
				At:      control.nextRepeat,
				Kind:    EventInputRepeat,
				Owner:   owner,
				Control: name,
			}); err != nil {
				return rollback(err)
			}
			if control.nextRepeat > time.Duration(math.MaxInt64-int64(i.repeatPeriod)) {
				return rollback(fmt.Errorf(
					"%w: input repeat deadline overflow",
					ErrLimitExceeded,
				))
			}
			control.nextRepeat += i.repeatPeriod
		}
		i.controls[name] = control
	}
	return nil
}

func (i *Input) Snapshot() InputState {
	state := InputState{
		MaxControls:    i.maxControls,
		RepeatDelayNS:  int64(i.repeatDelay),
		RepeatPeriodNS: int64(i.repeatPeriod),
		Focused:        i.focused,
	}
	names := make([]string, 0, len(i.controls))
	for name := range i.controls {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		control := i.controls[name]
		state.Controls = append(state.Controls, InputControlState{
			Name:       name,
			Pressed:    control.pressed,
			NextRepeat: int64(control.nextRepeat),
		})
	}
	return state
}

func (i *Input) Restore(state InputState) error {
	if state.MaxControls == 0 ||
		state.RepeatDelayNS <= 0 ||
		state.RepeatPeriodNS <= 0 ||
		len(state.Controls) > int(state.MaxControls) {
		return fmt.Errorf("%w: invalid input state limits", ErrInvalidState)
	}
	controls := make(map[string]inputControl, len(state.Controls))
	previous := ""
	for index, saved := range state.Controls {
		if strings.TrimSpace(saved.Name) == "" || len(saved.Name) > 255 ||
			strings.IndexByte(saved.Name, 0) >= 0 ||
			(index != 0 && saved.Name <= previous) ||
			saved.NextRepeat < 0 ||
			(saved.Pressed && saved.NextRepeat == 0) ||
			(!saved.Pressed && saved.NextRepeat != 0) {
			return fmt.Errorf("%w: invalid input control %d", ErrInvalidState, index)
		}
		controls[saved.Name] = inputControl{
			pressed:    saved.Pressed,
			nextRepeat: time.Duration(saved.NextRepeat),
		}
		previous = saved.Name
	}
	i.maxControls = state.MaxControls
	i.repeatDelay = time.Duration(state.RepeatDelayNS)
	i.repeatPeriod = time.Duration(state.RepeatPeriodNS)
	i.focused = state.Focused
	i.controls = controls
	return nil
}
