package skvm

import (
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
