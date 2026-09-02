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

// HasPendingRepeat answers whether an auto-repeat for this control is still
// waiting to be delivered. A repeat says only "the key is still down", so a
// second one behind an undelivered first carries nothing new - and while the
// guest cannot take input, appending them at the repeat rate is what fills the
// queue to its bound.
func (b *EventBus) HasPendingRepeat(owner OwnerID, control string) bool {
	for _, event := range b.events {
		if event.Kind == EventInputRepeat &&
			event.Owner == owner && event.Control == control {
			return true
		}
	}
	return false
}

// DropPendingLifecycle removes an owner's undelivered lifecycle records and
// answers how many went. Lifecycle is a state, not a log: the newest record is
// the whole truth and nothing reads the ones behind it. A quantum writes a
// running and a paused record, so without this a guest that stops taking
// events - because an undeliverable input sits at the head of the strictly
// ordered queue - fills the queue with them at two per frame.
func (b *EventBus) DropPendingLifecycle(owner OwnerID) int {
	kept := b.events[:0]
	dropped := 0
	for _, event := range b.events {
		if event.Kind == EventLifecycle && event.Owner == owner {
			dropped++
			continue
		}
		kept = append(kept, event)
	}
	for index := len(kept); index < len(b.events); index++ {
		b.events[index] = Event{}
	}
	b.events = kept
	return dropped
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

// SnapshotInto captures the bus into dst for a same-frame rollback, reusing
// dst's backing array so the per-frame transaction in Services.Advance does not
// allocate a fresh event slice every frame (that clone dominated per-frame
// allocation). Advance only enqueues events, never mutates an existing event's
// Data, so the copied Event structs may share Data with the live bus; Restore
// clones the bytes out again. This is for transient rollback only — the
// save-state path uses Snapshot, which deep-copies.
func (b *EventBus) SnapshotInto(dst *EventBusState) {
	dst.MaxEvents = b.maxEvents
	dst.MaxEventData = b.maxEventData
	dst.NextSequence = b.nextSequence
	dst.Events = append(dst.Events[:0], b.events...)
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
