package runtime

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRegistryAllocatesDeterministicGenerationIDs(t *testing.T) {
	registry := NewRegistry(2)
	first, err := registry.Create(1, KindSurface)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Create(2, KindFile)
	if err != nil {
		t.Fatal(err)
	}
	if first.Slot() != 1 || first.Generation() != 1 ||
		second.Slot() != 2 || second.Generation() != 1 {
		t.Fatalf("allocated IDs = %s, %s", first, second)
	}
	if _, err := registry.Create(1, KindImage); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Create past limit error = %v", err)
	}
	if err := registry.Destroy(first, 1, KindSurface); err != nil {
		t.Fatal(err)
	}
	reused, err := registry.Create(1, KindImage)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Slot() != first.Slot() || reused.Generation() != first.Generation()+1 {
		t.Fatalf("reused ID = %s, first = %s", reused, first)
	}
	if err := registry.Validate(first, 1, KindSurface); !errors.Is(err, ErrStaleID) {
		t.Fatalf("stale Validate error = %v", err)
	}
	if err := registry.Validate(reused, 2, KindImage); !errors.Is(err, ErrWrongOwner) {
		t.Fatalf("wrong-owner Validate error = %v", err)
	}
}

func TestRegistryRestoreValidatesBeforeMutation(t *testing.T) {
	registry := NewRegistry(4)
	id, err := registry.Create(7, KindTimer)
	if err != nil {
		t.Fatal(err)
	}
	before := registry.Snapshot()
	invalid := before
	invalid.Entries = append([]RegistryEntryState(nil), before.Entries...)
	invalid.Entries[0].ID = makeServiceID(id.Slot(), id.Generation()+1)
	if err := registry.Restore(invalid); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Restore invalid state error = %v", err)
	}
	if after := registry.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("registry mutated after rejected restore:\n got %+v\nwant %+v", after, before)
	}

	restored := NewRegistry(1)
	if err := restored.Restore(before); err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("restored state = %+v, want %+v", got, before)
	}
}

func TestRegistrySkipsExhaustedGenerationsWithoutMutatingOnFailure(t *testing.T) {
	registry := NewRegistry(2)
	state := RegistryState{
		Limit:    2,
		NextSlot: 3,
		Generations: []RegistryGenerationState{
			{Slot: 1, Generation: ^uint32(0)},
			{Slot: 2, Generation: 1},
		},
	}
	if err := registry.Restore(state); err != nil {
		t.Fatal(err)
	}
	id, err := registry.Create(1, KindSurface)
	if err != nil {
		t.Fatal(err)
	}
	if id.Slot() != 2 || id.Generation() != 2 {
		t.Fatalf("Create after exhausted slot = %s", id)
	}
	if err := registry.Destroy(id, 1, KindSurface); err != nil {
		t.Fatal(err)
	}
	exhausted := registry.Snapshot()
	exhausted.Generations[1].Generation = ^uint32(0)
	if err := registry.Restore(exhausted); err != nil {
		t.Fatal(err)
	}
	before := registry.Snapshot()
	if _, err := registry.Create(1, KindImage); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Create with exhausted slots error = %v", err)
	}
	if after := registry.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("failed exhausted-slot allocation mutated registry")
	}
}

func TestRegistryRejectsSparseHugeStateWithoutMutation(t *testing.T) {
	registry := NewRegistry(4)
	before := registry.Snapshot()
	invalid := RegistryState{
		Limit:    ^uint32(0),
		NextSlot: ^uint32(0),
	}
	if err := registry.Restore(invalid); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Restore sparse huge registry error = %v", err)
	}
	if after := registry.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("sparse huge registry state mutated registry")
	}
}

func TestClockAndNamedRandomStreamsRoundTrip(t *testing.T) {
	clock, err := NewClock(DefaultWallEpochMillis, 9*60, "ko-KR")
	if err != nil {
		t.Fatal(err)
	}
	if err := clock.Advance(1500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if clock.WallMillis() != DefaultWallEpochMillis+1500 ||
		clock.LocalMillis() != DefaultWallEpochMillis+1500+9*60*60_000 {
		t.Fatalf("clock values = wall %d local %d", clock.WallMillis(), clock.LocalMillis())
	}
	state := clock.Snapshot()
	if err := clock.Advance(-time.Nanosecond); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative advance error = %v", err)
	}
	clone, err := NewClock(1, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.Restore(state); err != nil {
		t.Fatal(err)
	}
	if clone.Snapshot() != state {
		t.Fatalf("restored clock = %+v, want %+v", clone.Snapshot(), state)
	}

	random := NewRandom(0x12345678, 4)
	cFirst, err := random.Uint64("c-rand")
	if err != nil {
		t.Fatal(err)
	}
	javaFirst, err := random.Uint64("java-random")
	if err != nil {
		t.Fatal(err)
	}
	randomState := random.Snapshot()
	cSecond, _ := random.Uint64("c-rand")
	javaSecond, _ := random.Uint64("java-random")

	replayed := NewRandom(0, 1)
	if err := replayed.Restore(randomState); err != nil {
		t.Fatal(err)
	}
	gotC, _ := replayed.Uint64("c-rand")
	gotJava, _ := replayed.Uint64("java-random")
	if gotC != cSecond || gotJava != javaSecond || cFirst == javaFirst {
		t.Fatalf(
			"random replay = (%x, %x), want (%x, %x); first values (%x, %x)",
			gotC,
			gotJava,
			cSecond,
			javaSecond,
			cFirst,
			javaFirst,
		)
	}
}

func TestJavaRandomCompatibilityStreamRoundTrip(t *testing.T) {
	random := NewRandom(0x1234, 4)
	if err := random.SetJavaSeed("java", 0); err != nil {
		t.Fatal(err)
	}
	first, err := random.JavaInt("java")
	if err != nil {
		t.Fatal(err)
	}
	if first != -1155484576 {
		t.Fatalf("first java.util.Random value = %d", first)
	}
	state := random.Snapshot()
	second, err := random.JavaInt("java")
	if err != nil {
		t.Fatal(err)
	}

	restored := NewRandom(0, 1)
	if err := restored.Restore(state); err != nil {
		t.Fatal(err)
	}
	replayed, err := restored.JavaInt("java")
	if err != nil {
		t.Fatal(err)
	}
	if replayed != second {
		t.Fatalf("replayed Java value = %d, want %d", replayed, second)
	}
	if _, err := restored.Uint64("java"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("wrong-policy draw error = %v", err)
	}
}

func TestEventInputAndTimersHaveDeterministicOrdering(t *testing.T) {
	registry := NewRegistry(16)
	bus := NewEventBus(32, 64)
	timers := NewTimers(registry, 8)
	later, err := timers.Define(1, "later-created")
	if err != nil {
		t.Fatal(err)
	}
	first, err := timers.Define(1, "first-created")
	if err != nil {
		t.Fatal(err)
	}
	if err := timers.Set(first, 1, time.Second, 0, 11); err != nil {
		t.Fatal(err)
	}
	if err := timers.Set(later, 1, time.Second, 0, 22); err != nil {
		t.Fatal(err)
	}
	if err := timers.Advance(time.Second, bus); err != nil {
		t.Fatal(err)
	}
	event, ok := bus.PopReady(time.Second)
	if !ok || event.ServiceID != later || event.Value != 22 {
		t.Fatalf("first equal-deadline event = %+v, %v", event, ok)
	}
	event, ok = bus.PopReady(time.Second)
	if !ok || event.ServiceID != first || event.Value != 11 {
		t.Fatalf("second equal-deadline event = %+v, %v", event, ok)
	}

	input := NewInput(4, 100*time.Millisecond, 50*time.Millisecond)
	if err := input.Change(bus, 1, "up", true, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := input.Advance(bus, 1, 2200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	var kinds []EventKind
	for {
		event, ok := bus.PopReady(2200 * time.Millisecond)
		if !ok {
			break
		}
		kinds = append(kinds, event.Kind)
	}
	want := []EventKind{
		EventInputPress,
		EventInputRepeat,
		EventInputRepeat,
		EventInputRepeat,
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("input event kinds = %v, want %v", kinds, want)
	}
}

func TestDirectInputAndTimerQueueFailuresAreAtomic(t *testing.T) {
	bus := NewEventBus(1, 64)
	input := NewInput(4, time.Second, time.Second)
	if _, err := bus.Enqueue(Event{Kind: EventApplication}); err != nil {
		t.Fatal(err)
	}
	inputBefore, busBefore := input.Snapshot(), bus.Snapshot()
	if err := input.Change(bus, 1, "up", true, 0); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Input.Change with full queue error = %v", err)
	}
	if !reflect.DeepEqual(input.Snapshot(), inputBefore) ||
		!reflect.DeepEqual(bus.Snapshot(), busBefore) {
		t.Fatal("failed direct input change was not atomic")
	}

	registry := NewRegistry(4)
	timers := NewTimers(registry, 2)
	first, err := timers.Define(1, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := timers.Define(1, "second")
	if err != nil {
		t.Fatal(err)
	}
	if err := timers.Set(first, 1, 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := timers.Set(second, 1, 0, 0, 2); err != nil {
		t.Fatal(err)
	}
	emptyBus := NewEventBus(1, 64)
	timerBefore, emptyBefore := timers.Snapshot(), emptyBus.Snapshot()
	if err := timers.Advance(0, emptyBus); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Timers.Advance with insufficient queue error = %v", err)
	}
	if !reflect.DeepEqual(timers.Snapshot(), timerBefore) ||
		!reflect.DeepEqual(emptyBus.Snapshot(), emptyBefore) {
		t.Fatal("failed direct timer advance was not atomic")
	}
}

func TestEventAndInputRestoreRejectNonCanonicalStateAtomically(t *testing.T) {
	bus := NewEventBus(4, 32)
	if _, err := bus.Enqueue(Event{
		At: 0, Kind: EventApplication, Name: "before",
	}); err != nil {
		t.Fatal(err)
	}
	busBefore := bus.Snapshot()
	invalidBus := busBefore
	invalidBus.Events = append(invalidBus.Events, Event{
		Sequence: invalidBus.Events[0].Sequence,
		At:       time.Millisecond,
		Kind:     EventApplication,
		Name:     "duplicate",
	})
	if err := bus.Restore(invalidBus); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("duplicate event sequence error = %v", err)
	}
	if !reflect.DeepEqual(bus.Snapshot(), busBefore) {
		t.Fatal("rejected event state mutated the bus")
	}

	input := NewInput(4, time.Second, time.Second)
	inputBefore := input.Snapshot()
	invalidInput := inputBefore
	invalidInput.Controls = []InputControlState{{
		Name: "up", Pressed: true, NextRepeat: 0,
	}}
	if err := input.Restore(invalidInput); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("pressed input without deadline error = %v", err)
	}
	if !reflect.DeepEqual(input.Snapshot(), inputBefore) {
		t.Fatal("rejected input state mutated input")
	}
}

func TestTraceToggleDoesNotChangeGuestVisibleSequences(t *testing.T) {
	run := func(enabled bool) (ServiceID, uint64, RandomState) {
		registry := NewRegistry(8)
		random := NewRandom(99, 4)
		bus := NewEventBus(8, 8)
		trace := NewTrace(2, 4)
		trace.SetEnabled(enabled)

		id, err := registry.Create(1, KindSurface)
		if err != nil {
			t.Fatal(err)
		}
		value, err := random.Uint64("guest")
		if err != nil {
			t.Fatal(err)
		}
		sequence, err := bus.Enqueue(Event{
			At:    time.Millisecond,
			Kind:  EventApplication,
			Owner: 1,
			Value: int64(value),
		})
		if err != nil {
			t.Fatal(err)
		}
		trace.Record(TraceEvent{
			At:        time.Millisecond,
			Runtime:   "test",
			Category:  "service",
			Name:      "create",
			ServiceID: id,
			Arguments: []int64{int64(value)},
			Data:      []byte("diagnostic payload"),
		})
		return id, sequence, random.Snapshot()
	}

	disabledID, disabledSequence, disabledRandom := run(false)
	enabledID, enabledSequence, enabledRandom := run(true)
	if disabledID != enabledID ||
		disabledSequence != enabledSequence ||
		!reflect.DeepEqual(disabledRandom, enabledRandom) {
		t.Fatal("trace enablement changed guest-visible allocation, event, or RNG state")
	}
}
