package runtime

import (
	"testing"
	"time"
)

// Characterization test locking key-repeat delay/period edge timing before
// the Input code moves from events.go to input.go.

func TestInputRepeatDelayPeriodBoundaries(t *testing.T) {
	bus := NewEventBus(16, 64)
	input := NewInput(4, 100*time.Millisecond, 50*time.Millisecond)
	if err := input.Change(bus, 1, "up", true, 0); err != nil {
		t.Fatal(err)
	}
	drain := func(now time.Duration) []EventKind {
		var kinds []EventKind
		for {
			event, ok := bus.PopReady(now)
			if !ok {
				return kinds
			}
			kinds = append(kinds, event.Kind)
		}
	}
	if kinds := drain(0); len(kinds) != 1 || kinds[0] != EventInputPress {
		t.Fatalf("events at press = %v, want [press]", kinds)
	}
	if err := input.Advance(bus, 1, 99*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if kinds := drain(99 * time.Millisecond); len(kinds) != 0 {
		t.Fatalf("events before delay = %v, want none", kinds)
	}
	if err := input.Advance(bus, 1, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if kinds := drain(100 * time.Millisecond); len(kinds) != 1 || kinds[0] != EventInputRepeat {
		t.Fatalf("events at delay = %v, want [repeat]", kinds)
	}
	if err := input.Advance(bus, 1, 149*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if kinds := drain(149 * time.Millisecond); len(kinds) != 0 {
		t.Fatalf("events before period = %v, want none", kinds)
	}
	if err := input.Advance(bus, 1, 150*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if kinds := drain(150 * time.Millisecond); len(kinds) != 1 || kinds[0] != EventInputRepeat {
		t.Fatalf("events at period = %v, want [repeat]", kinds)
	}
	if err := input.Change(bus, 1, "up", false, 160*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if kinds := drain(160 * time.Millisecond); len(kinds) != 1 || kinds[0] != EventInputRelease {
		t.Fatalf("events at release = %v, want [release]", kinds)
	}
	if err := input.Advance(bus, 1, time.Second); err != nil {
		t.Fatal(err)
	}
	if kinds := drain(time.Second); len(kinds) != 0 {
		t.Fatalf("events after release = %v, want none", kinds)
	}
}
