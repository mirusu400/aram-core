package ktf

import (
	"testing"
	"time"
)

func TestNextWakeWithinReportsTheEarliestDeadlineInTheWindow(t *testing.T) {
	runtime := &Runtime{TickMS: 100}
	blocked := &Task{WakeAtMS: 104}
	blocked.startBlocker = &Task{}
	runtime.Tasks = []*Task{
		nil,
		{WakeAtMS: 0},               // runnable
		{WakeAtMS: 90},              // already due
		{WakeAtMS: 130},             // beyond the window
		{WakeAtMS: ^uint64(0)},      // parked until something else releases it
		{WakeAtMS: 108, Done: true}, // finished
		blocked,                     // waiting on a child start, not the clock
		{WakeAtMS: 112},             // the answer
		{WakeAtMS: 116},             // later than the answer
	}
	wake, ok := runtime.NextWakeWithin(16 * time.Millisecond)
	if !ok || wake != 12*time.Millisecond {
		t.Fatalf("NextWakeWithin = %s, %t; want 12ms, true", wake, ok)
	}
}

func TestNextWakeWithinReportsNothingOutsideTheWindow(t *testing.T) {
	runtime := &Runtime{TickMS: 100}
	runtime.Tasks = []*Task{{WakeAtMS: 130}}
	for _, limit := range []time.Duration{
		-time.Millisecond,
		0,
		999 * time.Microsecond,
		16 * time.Millisecond,
	} {
		if wake, ok := runtime.NextWakeWithin(limit); ok {
			t.Fatalf("NextWakeWithin(%s) = %s, true; want no deadline", limit, wake)
		}
	}
}
