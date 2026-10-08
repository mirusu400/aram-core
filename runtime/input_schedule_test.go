package runtime

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestServicesFutureReleasePreservesHeldStateAndRepeats(t *testing.T) {
	services, err := NewServices(Config{})
	check(t, err)
	check(t, services.QueueInput(1, "fire", true, 0))
	services.Events.PopReady(0)
	check(t, services.QueueInput(1, "fire", false, time.Second))
	if !services.Input.Held("fire") {
		t.Fatal("future release changed present held state")
	}
	check(t, services.Advance(1, 600*time.Millisecond))
	event, ok := services.Events.PopReady(services.Clock.Monotonic())
	if !services.Input.Held("fire") || !ok || event.Kind != EventInputRepeat || event.At != 500*time.Millisecond {
		t.Fatalf("before release: held=%t event=%+v ready=%t", services.Input.Held("fire"), event, ok)
	}
	check(t, services.Advance(1, 400*time.Millisecond))
	if services.Input.Held("fire") {
		t.Fatal("key still held at release deadline")
	}
	var kinds []EventKind
	for event, ok := services.Events.PopReady(time.Second); ok; event, ok = services.Events.PopReady(time.Second) {
		kinds = append(kinds, event.Kind)
		if event.Kind == EventInputRepeat && event.At >= time.Second {
			t.Fatal("repeat emitted at or after release")
		}
	}
	if !reflect.DeepEqual(kinds, []EventKind{EventInputRepeat, EventInputRelease}) {
		t.Fatalf("events at release = %v", kinds)
	}
}

func TestServicesFuturePressAndRepeatStartAtScheduledTime(t *testing.T) {
	services, err := NewServices(Config{})
	check(t, err)
	check(t, services.QueueInput(1, "fire", true, 200*time.Millisecond))
	check(t, services.Advance(1, 199*time.Millisecond))
	if services.Input.Held("fire") || services.Events.Len() != 0 {
		t.Fatal("press applied before scheduled time")
	}
	check(t, services.Advance(1, time.Millisecond))
	event, ok := services.Events.PopReady(services.Clock.Monotonic())
	if !services.Input.Held("fire") || !ok || event.Kind != EventInputPress || event.At != 200*time.Millisecond {
		t.Fatalf("at press: held=%t event=%+v ready=%t", services.Input.Held("fire"), event, ok)
	}
	check(t, services.Advance(1, 499*time.Millisecond))
	if services.Events.Len() != 0 {
		t.Fatal("key repeated before delay from scheduled press")
	}
	check(t, services.Advance(1, time.Millisecond))
	event, ok = services.Events.PopReady(services.Clock.Monotonic())
	if !ok || event.Kind != EventInputRepeat || event.At != 700*time.Millisecond {
		t.Fatalf("first scheduled repeat = %+v, ready=%t", event, ok)
	}
}

func TestServicesScheduledInputSameTimeOrderAndFocus(t *testing.T) {
	for _, focused := range []bool{true, false} {
		for _, firstPressed := range []bool{true, false} {
			services, err := NewServices(Config{})
			check(t, err)
			services.Input.SetFocus(focused)
			check(t, services.QueueInput(1, "fire", firstPressed, time.Second))
			check(t, services.QueueInput(1, "fire", !firstPressed, time.Second))
			check(t, services.Advance(1, time.Second))
			if services.Input.Held("fire") != !firstPressed {
				t.Fatalf("same-time order changed: focused=%t firstPressed=%t", focused, firstPressed)
			}
			wantEvents := 0
			if focused {
				wantEvents = 1
				if firstPressed {
					wantEvents = 2
				}
			}
			if services.Events.Len() != wantEvents {
				t.Fatalf("same-time events=%d, want %d (focused=%t firstPressed=%t)", services.Events.Len(), wantEvents, focused, firstPressed)
			}
		}
	}
}

func TestServicesScheduledInputQueueFailureIsAtomic(t *testing.T) {
	config := DefaultConfig()
	config.Limits.MaxEvents = 1
	config.Limits.MaxControls = 1
	services, err := NewServices(config)
	check(t, err)
	check(t, services.QueueInput(1, "fire", true, time.Second))
	before := services.Snapshot()
	for _, control := range []string{"fire", "up"} {
		if err := services.QueueInput(1, control, false, 2*time.Second); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("queue past limit error = %v", err)
		}
		if !reflect.DeepEqual(services.Snapshot(), before) {
			t.Fatal("failed queue changed reserved controls or pending input")
		}
	}
}

func TestServicesScheduledInputEventFailureRollsBackAndRetries(t *testing.T) {
	config := DefaultConfig()
	config.Limits.MaxEvents = 2
	services, err := NewServices(config)
	check(t, err)
	check(t, services.QueueInput(1, "fire", true, 100*time.Millisecond))
	check(t, services.QueueInput(1, "fire", false, 200*time.Millisecond))
	_, err = services.Events.Enqueue(Event{Kind: EventApplication})
	check(t, err)
	before := services.Snapshot()
	if err := services.Advance(1, time.Second); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("full event queue advance error = %v", err)
	}
	if !reflect.DeepEqual(services.Snapshot(), before) {
		t.Fatal("partial input advance was not rolled back")
	}
	services.Events.PopReady(0)
	check(t, services.Advance(1, time.Second))
	if services.Input.Held("fire") || services.Events.Len() != 2 {
		t.Fatal("retry did not apply both original transitions exactly once")
	}
}

func TestServicesRestoreRejectsInvalidScheduledInputAtomically(t *testing.T) {
	config := DefaultConfig()
	config.Limits.MaxEvents = 2
	services, err := NewServices(config)
	check(t, err)
	check(t, services.Advance(1, time.Second))
	check(t, services.QueueInput(1, "fire", true, 2*time.Second))
	check(t, services.QueueInput(1, "fire", false, 3*time.Second))
	before := services.Snapshot()
	for name, mutate := range map[string]func(*ServicesState){
		"past transition": func(s *ServicesState) { s.Input.Pending[0].AtNS = int64(time.Second) },
		"unsorted": func(s *ServicesState) {
			s.Input.Pending[0], s.Input.Pending[1] = s.Input.Pending[1], s.Input.Pending[0]
		},
		"missing control": func(s *ServicesState) { s.Input.Pending[0].Control = "missing" },
		"repeat overflow": func(s *ServicesState) { s.Input.Pending[1].Pressed = true; s.Input.Pending[1].AtNS = math.MaxInt64 },
		"queue limit":     func(s *ServicesState) { s.Input.Pending = append(s.Input.Pending, s.Input.Pending[1]) },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := services.Snapshot()
			mutate(&invalid)
			if err := services.Restore(invalid); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("invalid pending input restore error = %v", err)
			}
			if !reflect.DeepEqual(services.Snapshot(), before) {
				t.Fatal("rejected input restore changed services")
			}
		})
	}
}

func TestServicesLegacyInputStateLoads(t *testing.T) {
	services, err := NewServices(Config{})
	check(t, err)
	check(t, services.QueueInput(1, "fire", true, 0))
	state := services.Input.Snapshot()
	old := inputStateV2{
		MaxControls: state.MaxControls, RepeatDelayNS: state.RepeatDelayNS,
		RepeatPeriodNS: state.RepeatPeriodNS, Focused: state.Focused, Controls: state.Controls,
	}
	payload, err := encodeStateValue(old)
	check(t, err)
	encoded, err := services.MarshalBinary()
	check(t, err)
	legacy := replaceServiceComponentForTest(t, encoded, "input", payload, 2)
	restored, err := NewServices(Config{})
	check(t, err)
	check(t, restored.UnmarshalBinary(legacy))
	if !reflect.DeepEqual(restored.Snapshot(), services.Snapshot()) {
		t.Fatal("legacy input state changed during migration")
	}
	_, err = restored.MarshalBinary()
	check(t, err)
}

func TestServicesScheduledInputRoundTripAndChronologicalAdvance(t *testing.T) {
	services, err := NewServices(Config{})
	check(t, err)
	// Queue out of time order; the repeated press at the same time is a no-op.
	check(t, services.QueueInput(7, "fire", false, 2*time.Second))
	check(t, services.QueueInput(7, "fire", true, time.Second))
	check(t, services.QueueInput(7, "fire", true, time.Second))
	if services.Input.Held("fire") {
		t.Fatal("future press changed present held state")
	}
	encoded, err := services.MarshalBinary()
	check(t, err)
	restored, err := NewServices(Config{})
	check(t, err)
	check(t, restored.UnmarshalBinary(encoded))
	if !reflect.DeepEqual(restored.Snapshot(), services.Snapshot()) {
		t.Fatal("scheduled input changed across binary round trip")
	}
	for _, current := range []*Services{services, restored} {
		check(t, current.Advance(7, 2500*time.Millisecond))
		if current.Input.Held("fire") {
			t.Fatal("key still held after crossing both transitions")
		}
		var events []Event
		for event, ok := current.Events.PopReady(current.Clock.Monotonic()); ok; event, ok = current.Events.PopReady(current.Clock.Monotonic()) {
			events = append(events, event)
		}
		if len(events) != 3 || events[0].Kind != EventInputPress || events[0].At != time.Second ||
			events[1].Kind != EventInputRepeat || events[1].At != 1500*time.Millisecond ||
			events[2].Kind != EventInputRelease || events[2].At != 2*time.Second {
			t.Fatalf("scheduled event timeline = %+v", events)
		}
	}
}

func TestServicesScheduledInputAdvanceFailureRollsBack(t *testing.T) {
	config := DefaultConfig()
	config.ReplayMode = ReplayRecord
	config.Limits.Replay.MaxEntries = 2
	services, err := NewServices(config)
	check(t, err)
	check(t, services.QueueInput(1, "fire", true, time.Second))
	check(t, services.QueueInput(1, "fire", false, 2*time.Second))
	before := services.Snapshot()
	if err := services.Advance(1, 2500*time.Millisecond); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("exhausted replay advance error = %v", err)
	}
	if !reflect.DeepEqual(services.Snapshot(), before) {
		t.Fatal("failed advance changed scheduled input or held state")
	}
}
