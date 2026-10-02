package skvm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSKVMObjectWaitSurvivesSaveAndWakesOnNotify(t *testing.T) {
	vm, err := New(map[string][]byte{"Worker": syntheticThreadClassWithInitializer(t, false)})
	check(t, err)
	object := vm.NewObject("java/lang/Object", nil)
	target, err := vm.allocateObject("Worker")
	check(t, err)
	first := vm.NewObject("java/lang/Thread", nil)
	second := vm.NewObject("java/lang/Thread", nil)
	for _, reference := range []uint32{first, second} {
		invokeTestNative(t, vm, "java/lang/Thread", "<init>", "(Ljava/lang/Runnable;)V", reference, ReferenceValue(target))
		invokeTestNative(t, vm, "java/lang/Thread", "start", "()V", reference)
	}

	vm.runningThread = first
	wait := vm.natives[nativeKey{class: "java/lang/Object", name: "wait", descriptor: "()V"}]
	_, _, err = wait(context.Background(), vm, object, nil)
	var yielded *threadYield
	if !errors.As(err, &yielded) || yielded.waitingOn != object || yielded.delay != 0 {
		t.Fatalf("wait() yielded %v, want an untimed wait on object %d", err, object)
	}
	vm.runningThread = 0
	for _, reference := range []uint32{first, second} {
		state, err := vm.thread(reference)
		check(t, err)
		state.waitingOn = object
		state.wakeAt = time.Duration(^uint64(0) >> 1)
		if !vm.threadBlocked(state) {
			t.Fatal("an untimed waiter became ready without notification")
		}
	}

	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	state, err := vm.thread(first)
	check(t, err)
	if state.waitingOn != object || !vm.threadBlocked(state) {
		t.Fatal("save/load lost the object wait")
	}

	invokeTestNative(t, vm, "java/lang/Object", "notify", "()V", object)
	firstState, _ := vm.thread(first)
	secondState, _ := vm.thread(second)
	if firstState.waitingOn != 0 || secondState.waitingOn != object {
		t.Fatal("notify did not wake exactly the first waiter")
	}
	invokeTestNative(t, vm, "java/lang/Object", "notifyAll", "()V", object)
	if secondState.waitingOn != 0 {
		t.Fatal("notifyAll left a waiter parked")
	}
}
