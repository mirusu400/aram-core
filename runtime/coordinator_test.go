package runtime

import (
	"errors"
	"reflect"
	"testing"
)

func TestCoordinatorLifecycleBudgetAndScheduling(t *testing.T) {
	coordinator, err := NewCoordinator(CoordinatorLimits{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := coordinator.Register("ktf", 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.Register("skvm", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []OwnerID{first, second} {
		if err := coordinator.Transition(owner, LifecycleReady, 0, nil); err != nil {
			t.Fatal(err)
		}
		if err := coordinator.Transition(owner, LifecycleRunning, 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := coordinator.BeginQuantum(); err != nil {
		t.Fatal(err)
	}
	one, ok := coordinator.NextRunnable()
	two, ok2 := coordinator.NextRunnable()
	if !ok || !ok2 || one != first || two != second {
		t.Fatalf("round-robin owners = %d/%v, %d/%v", one, ok, two, ok2)
	}
	if err := coordinator.Consume(first, 10); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Consume(first, 1); err == nil {
		t.Fatal("Consume exceeded adapter budget")
	}
	state := coordinator.Snapshot()
	clone, err := NewCoordinator(CoordinatorLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.Restore(state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clone.Snapshot(), state) {
		t.Fatal("coordinator state did not round-trip")
	}
}

func TestCoordinatorRejectsInvalidTransitionWithoutMutation(t *testing.T) {
	coordinator, err := NewCoordinator(CoordinatorLimits{})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := coordinator.Register("adapter", 0)
	if err != nil {
		t.Fatal(err)
	}
	before := coordinator.Snapshot()
	if err := coordinator.Transition(owner, LifecyclePaused, 0, nil); err == nil {
		t.Fatal("loaded adapter transitioned directly to paused")
	}
	if after := coordinator.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("invalid lifecycle transition mutated coordinator")
	}
}

func TestCoordinatorRejectsNULFaultWithoutMutation(t *testing.T) {
	coordinator, err := NewCoordinator(CoordinatorLimits{})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := coordinator.Register("adapter", 0)
	if err != nil {
		t.Fatal(err)
	}
	before := coordinator.Snapshot()
	if err := coordinator.Fault(
		owner,
		"bad\x00fault",
		0,
		nil,
	); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("NUL fault error = %v", err)
	}
	if !reflect.DeepEqual(coordinator.Snapshot(), before) {
		t.Fatal("rejected fault mutated coordinator")
	}
}

func TestCoordinatorTransitionQueueFailureRestoresFault(t *testing.T) {
	coordinator, err := NewCoordinator(CoordinatorLimits{})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := coordinator.Register("adapter", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Fault(owner, "boom", 0, nil); err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(1, 1)
	if _, err := bus.Enqueue(Event{Kind: EventApplication}); err != nil {
		t.Fatal(err)
	}
	before := coordinator.Snapshot()
	if err := coordinator.Transition(
		owner,
		LifecycleReady,
		0,
		bus,
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("full-queue transition error = %v", err)
	}
	if !reflect.DeepEqual(coordinator.Snapshot(), before) {
		t.Fatal("failed lifecycle event lost the previous fault state")
	}
}
