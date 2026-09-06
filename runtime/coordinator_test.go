package runtime

import (
	"errors"
	"reflect"
	"testing"
)

func TestCoordinatorLifecycleBudgetAndScheduling(t *testing.T) {
	coordinator, err := NewCoordinator(CoordinatorLimits{})
	check(t, err)
	first, err := coordinator.Register("ktf", 10)
	check(t, err)
	second, err := coordinator.Register("skvm", 10)
	check(t, err)
	for _, owner := range []OwnerID{first, second} {
		check(t, coordinator.Transition(owner, LifecycleReady, 0, nil))
		check(t, coordinator.Transition(owner, LifecycleRunning, 0, nil))
	}
	if _, err := coordinator.BeginQuantum(); err != nil {
		t.Fatal(err)
	}
	one, ok := coordinator.NextRunnable()
	two, ok2 := coordinator.NextRunnable()
	if !ok || !ok2 || one != first || two != second {
		t.Fatalf("round-robin owners = %d/%v, %d/%v", one, ok, two, ok2)
	}
	check(t, coordinator.Consume(first, 10))
	if err := coordinator.Consume(first, 1); err == nil {
		t.Fatal("Consume exceeded adapter budget")
	}
	state := coordinator.Snapshot()
	clone, err := NewCoordinator(CoordinatorLimits{})
	check(t, err)
	check(t, clone.Restore(state))
	if !reflect.DeepEqual(clone.Snapshot(), state) {
		t.Fatal("coordinator state did not round-trip")
	}
}

func TestCoordinatorRejectsInvalidTransitionWithoutMutation(t *testing.T) {
	coordinator, err := NewCoordinator(CoordinatorLimits{})
	check(t, err)
	owner, err := coordinator.Register("adapter", 0)
	check(t, err)
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
	check(t, err)
	owner, err := coordinator.Register("adapter", 0)
	check(t, err)
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
	check(t, err)
	owner, err := coordinator.Register("adapter", 0)
	check(t, err)
	check(t, coordinator.Fault(owner, "boom", 0, nil))
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
