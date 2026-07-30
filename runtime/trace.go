package runtime

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type TraceEvent struct {
	Sequence   uint64
	At         time.Duration
	Runtime    string
	Task       string
	Category   string
	Name       string
	Location   string
	ServiceID  ServiceID
	Arguments  []int64
	Result     int64
	ErrorClass string
	Data       []byte
}

type TraceState struct {
	Enabled      bool
	MaxEvents    uint32
	MaxData      uint32
	NextSequence uint64
	Dropped      uint64
	Events       []TraceEvent
}

// Trace is observational. Its sequence, capacity, and allocations are wholly
// separate from guest-visible event and service-ID sequences.
type Trace struct {
	enabled      bool
	maxEvents    uint32
	maxData      uint32
	nextSequence uint64
	dropped      uint64
	events       []TraceEvent
}

func NewTrace(maxEvents, maxData uint32) *Trace {
	if maxEvents == 0 {
		maxEvents = DefaultMaxTraceEvents
	}
	if maxData == 0 {
		maxData = DefaultMaxTraceData
	}
	return &Trace{
		maxEvents:    maxEvents,
		maxData:      maxData,
		nextSequence: 1,
	}
}

func (t *Trace) SetEnabled(enabled bool) {
	t.enabled = enabled
}

func (t *Trace) Enabled() bool {
	return t.enabled
}

func (t *Trace) Record(event TraceEvent) {
	if !t.enabled {
		return
	}
	if event.At < 0 ||
		!validTraceString(event.Runtime, 64) ||
		!validTraceString(event.Task, 128) ||
		!validTraceString(event.Category, 64) ||
		!validTraceString(event.Name, 128) ||
		!validTraceString(event.Location, 255) ||
		!validTraceString(event.ErrorClass, 128) {
		t.incrementDropped()
		return
	}
	if len(event.Arguments) > 16 {
		event.Arguments = append([]int64(nil), event.Arguments[:16]...)
	} else {
		event.Arguments = append([]int64(nil), event.Arguments...)
	}
	if len(event.Data) > int(t.maxData) {
		event.Data = cloneBytes(event.Data[:t.maxData])
	} else {
		event.Data = cloneBytes(event.Data)
	}
	if t.nextSequence == 0 || t.nextSequence == math.MaxUint64 {
		t.incrementDropped()
		return
	}
	event.Sequence = t.nextSequence
	t.nextSequence++
	if uint32(len(t.events)) == t.maxEvents {
		copy(t.events, t.events[1:])
		t.events[len(t.events)-1] = event
		t.incrementDropped()
		return
	}
	t.events = append(t.events, event)
}

func (t *Trace) incrementDropped() {
	if t.dropped != math.MaxUint64 {
		t.dropped++
	}
}

func (t *Trace) Events() []TraceEvent {
	events := make([]TraceEvent, len(t.events))
	copy(events, t.events)
	for index := range events {
		events[index].Arguments = append([]int64(nil), events[index].Arguments...)
		events[index].Data = cloneBytes(events[index].Data)
	}
	return events
}

func (t *Trace) Snapshot() TraceState {
	return TraceState{
		Enabled:      t.enabled,
		MaxEvents:    t.maxEvents,
		MaxData:      t.maxData,
		NextSequence: t.nextSequence,
		Dropped:      t.dropped,
		Events:       t.Events(),
	}
}

func (t *Trace) Restore(state TraceState) error {
	if state.MaxEvents == 0 || state.MaxData == 0 ||
		state.NextSequence == 0 || len(state.Events) > int(state.MaxEvents) {
		return fmt.Errorf("%w: invalid trace state limits", ErrInvalidState)
	}
	events := make([]TraceEvent, len(state.Events))
	var previous uint64
	for index, event := range state.Events {
		if event.Sequence == 0 || event.Sequence >= state.NextSequence ||
			(index != 0 && event.Sequence <= previous) ||
			event.At < 0 || len(event.Arguments) > 16 ||
			len(event.Data) > int(state.MaxData) ||
			!validTraceString(event.Runtime, 64) ||
			!validTraceString(event.Task, 128) ||
			!validTraceString(event.Category, 64) ||
			!validTraceString(event.Name, 128) ||
			!validTraceString(event.Location, 255) ||
			!validTraceString(event.ErrorClass, 128) {
			return fmt.Errorf("%w: invalid trace event %d", ErrInvalidState, index)
		}
		event.Arguments = append([]int64(nil), event.Arguments...)
		event.Data = cloneBytes(event.Data)
		events[index] = event
		previous = event.Sequence
	}
	t.enabled = state.Enabled
	t.maxEvents = state.MaxEvents
	t.maxData = state.MaxData
	t.nextSequence = state.NextSequence
	t.dropped = state.Dropped
	t.events = events
	return nil
}

func validTraceString(value string, limit int) bool {
	return len(value) <= limit && strings.IndexByte(value, 0) < 0
}

// TraceScalarMap returns sorted scalar key/value pairs suitable for a bounded
// trace payload without exposing map iteration order.
func TraceScalarMap(values map[string]int64) ([]string, []int64) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]int64, len(keys))
	for index, key := range keys {
		result[index] = values[key]
	}
	return keys, result
}
