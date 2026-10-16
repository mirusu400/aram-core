package ktf

import (
	"testing"
	"time"
)

func TestKTFTimerSleepingCallbackKeepsItsWorker(t *testing.T) {
	r := newTestRuntime(t)
	r.TickMS = 100
	first := &Task{timerOwner: 1, timerDeadlineMS: 80, timerStarted: true, WakeAtMS: 150}
	next := &Task{timerOwner: 1, timerDeadlineMS: 100}
	r.Tasks = []*Task{first, next}
	r.taskCursor = 1
	if r.HasRunnableTask() || r.nextRunnableTask() != nil {
		t.Fatal("a second callback ran while its Timer's worker was sleeping")
	}
	if delay, ok := r.NextWakeWithin(time.Second); !ok || delay != 50*time.Millisecond {
		t.Fatalf("worker wake = %s/%t, want 50ms/true", delay, ok)
	}
	independent := &Task{timerOwner: 2, timerDeadlineMS: 100}
	r.Tasks = append(r.Tasks, independent)
	if got := r.nextRunnableTask(); got != independent {
		t.Fatal("one sleeping Timer blocked an independent Timer")
	}
	independent.Done = true
	r.TickMS = 150
	if got := r.nextRunnableTask(); got != first {
		t.Fatal("the sleeping callback did not resume before its successor")
	}
	first.Done = true
	if got := r.nextRunnableTask(); got != next {
		t.Fatal("the successor did not run after its predecessor returned")
	}
}

func TestKTFTimerCallbacksRunInDeadlineOrder(t *testing.T) {
	r := newTestRuntime(t)
	r.TickMS = 100
	later := &Task{timerOwner: 1, timerDeadlineMS: 200, WakeAtMS: 200}
	earlier := &Task{timerOwner: 1, timerDeadlineMS: 150, WakeAtMS: 150}
	r.Tasks = []*Task{later, earlier}
	if delay, ok := r.NextWakeWithin(time.Second); !ok || delay != 50*time.Millisecond {
		t.Fatalf("first deadline = %s/%t, want 50ms/true", delay, ok)
	}
	r.TickMS = 250
	if got := r.nextRunnableTask(); got != earlier {
		t.Fatal("the round-robin cursor overtook the earlier Timer deadline")
	}
	earlier.timerStarted = true
	earlier.WakeAtMS = 300
	if r.HasRunnableTask() {
		t.Fatal("an overdue callback overtook its sleeping predecessor")
	}
	earlier.Done = true
	if got := r.nextRunnableTask(); got != later {
		t.Fatal("the later callback was not released on completion")
	}
}

func TestKTFTimerRestoredOverlappingCallbacksMakeProgress(t *testing.T) {
	// Older states may already contain more than one started callback for an
	// owner. Serialize them in deadline order instead of blocking each other.
	r := newTestRuntime(t)
	later := &Task{timerOwner: 1, timerDeadlineMS: 20, timerStarted: true}
	earlier := &Task{timerOwner: 1, timerDeadlineMS: 10, timerStarted: true}
	r.Tasks = []*Task{later, earlier}
	if got := r.nextRunnableTask(); got != earlier {
		t.Fatal("overlapping callbacks from an older state cannot make progress")
	}
	earlier.Done = true
	if got := r.nextRunnableTask(); got != later {
		t.Fatal("the remaining restored callback cannot make progress")
	}
}
