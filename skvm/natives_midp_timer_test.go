package skvm

import (
	"context"
	"testing"
	"time"
)

func TestMIDPTimerDateOverloadsAndScheduledTime(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	timer := vm.NewObject("java/util/Timer", nil)
	task := vm.NewObject("java/util/TimerTask", nil)
	invokeTestNative(t, vm, "java/util/Timer", "<init>", "()V", timer)
	invokeTestNative(t, vm, "java/util/TimerTask", "<init>", "()V", task)
	whenMillis := vm.services.Clock.WallMillis() + 500
	when := vm.NewObject("java/util/Date", &dateState{millis: whenMillis})
	invokeTestNative(t, vm, "java/util/Timer", "schedule", "(Ljava/util/TimerTask;Ljava/util/Date;)V", timer,
		ReferenceValue(task), ReferenceValue(when))
	value := invokeTestNative(t, vm, "java/util/TimerTask", "scheduledExecutionTime", "()J", task)
	got, err := value.Long()
	check(t, err)
	if got != whenMillis {
		t.Fatalf("scheduled execution time = %d, want %d", got, whenMillis)
	}
	check(t, vm.Advance(t.Context(), 500*time.Millisecond, nil))
}

func TestMIDPOneShotTimersRetireAfterCallback(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	timer := vm.NewObject("java/util/Timer", nil)
	invokeTestNative(t, vm, "java/util/Timer", "<init>", "()V", timer)
	var lastTask uint32
	for i := 0; i < 1100; i++ {
		task := vm.NewObject("java/util/TimerTask", nil)
		invokeTestNative(t, vm, "java/util/TimerTask", "<init>", "()V", task)
		invokeTestNative(t, vm, "java/util/Timer", "schedule", "(Ljava/util/TimerTask;J)V", timer,
			ReferenceValue(task), LongValue(0))
		check(t, vm.Advance(context.Background(), time.Millisecond, nil))
		lastTask = task
	}
	if got := len(vm.services.Timers.Snapshot().Timers); got != 0 {
		t.Fatalf("completed one-shot timers retained %d service slots", got)
	}
	owner, err := vm.timerObject(timer)
	check(t, err)
	if len(owner.timers) != 0 {
		t.Fatalf("completed one-shot timers retained %d owner entries", len(owner.timers))
	}
	if wasActive, err := invokeTestNative(t, vm, "java/util/TimerTask", "cancel", "()Z", lastTask).Int(); err != nil || wasActive != 0 {
		t.Fatalf("completed task cancel = %d, %v", wasActive, err)
	}
	if _, _, err := vm.natives[nativeKey{"java/util/Timer", "schedule", "(Ljava/util/TimerTask;J)V"}](
		context.Background(), vm, timer, []Value{ReferenceValue(lastTask), LongValue(0)},
	); err == nil {
		t.Fatal("completed TimerTask was rescheduled")
	}
	invokeTestNative(t, vm, "java/util/Timer", "cancel", "()V", timer)
	state, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(state))
	if got := len(vm.services.Timers.Snapshot().Timers); got != 0 {
		t.Fatalf("restored completed timers retained %d service slots", got)
	}
}

func TestMIDPCancelledTimersReleaseServiceSlots(t *testing.T) {
	for _, cancelOwner := range []bool{false, true} {
		name := "task"
		if cancelOwner {
			name = "owner"
		}
		t.Run(name, func(t *testing.T) {
			vm, err := New(map[string][]byte{})
			check(t, err)
			owner := vm.NewObject("java/util/Timer", nil)
			invokeTestNative(t, vm, "java/util/Timer", "<init>", "()V", owner)
			for i := 0; i < 1100; i++ {
				if cancelOwner && i != 0 {
					owner = vm.NewObject("java/util/Timer", nil)
					invokeTestNative(t, vm, "java/util/Timer", "<init>", "()V", owner)
				}
				task := vm.NewObject("java/util/TimerTask", nil)
				invokeTestNative(t, vm, "java/util/TimerTask", "<init>", "()V", task)
				invokeTestNative(t, vm, "java/util/Timer", "schedule", "(Ljava/util/TimerTask;J)V", owner,
					ReferenceValue(task), LongValue(60_000))
				if cancelOwner {
					invokeTestNative(t, vm, "java/util/Timer", "cancel", "()V", owner)
				} else {
					value := invokeTestNative(t, vm, "java/util/TimerTask", "cancel", "()Z", task)
					if active, err := value.Int(); err != nil || active != 1 {
						t.Fatalf("cancel task %d = %d, %v", i, active, err)
					}
				}
			}
			if got := len(vm.services.Timers.Snapshot().Timers); got != 0 {
				t.Fatalf("cancelled timers retained %d service slots", got)
			}
		})
	}
}

func TestMIDPLegacyCancelledTimersReapAtCapacity(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	owner := vm.NewObject("java/util/Timer", nil)
	invokeTestNative(t, vm, "java/util/Timer", "<init>", "()V", owner)
	for i := 0; i < 1024; i++ {
		task := vm.NewObject("java/util/TimerTask", nil)
		invokeTestNative(t, vm, "java/util/TimerTask", "<init>", "()V", task)
		invokeTestNative(t, vm, "java/util/Timer", "schedule", "(Ljava/util/TimerTask;J)V", owner,
			ReferenceValue(task), LongValue(60_000))
		state, err := vm.timerTask(task)
		check(t, err)
		check(t, vm.services.Timers.Cancel(state.timer, vm.serviceOwner))
		state.cancelled = true // State left by the old TimerTask.cancel implementation.
	}
	if got := len(vm.services.Timers.Snapshot().Timers); got != 1024 {
		t.Fatalf("legacy timer count = %d, want 1024", got)
	}
	task := vm.NewObject("java/util/TimerTask", nil)
	invokeTestNative(t, vm, "java/util/TimerTask", "<init>", "()V", task)
	invokeTestNative(t, vm, "java/util/Timer", "schedule", "(Ljava/util/TimerTask;J)V", owner,
		ReferenceValue(task), LongValue(60_000))
	if got := len(vm.services.Timers.Snapshot().Timers); got != 1 {
		t.Fatalf("reaped timer count = %d, want 1", got)
	}
	ownerState, err := vm.timerObject(owner)
	check(t, err)
	if got := len(ownerState.timers); got != 1 {
		t.Fatalf("reaped owner entries = %d, want 1", got)
	}
}
