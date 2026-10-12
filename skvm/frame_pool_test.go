package skvm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFrameExecutionStillObservesCancellationBetweenInstructions(t *testing.T) {
	vm := &VM{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm.SetTraceHook(func(TraceEvent) error {
		if vm.Instructions == 2 {
			cancel()
		}
		return nil
	})
	method := Method{AccessFlags: AccessStatic, Name: "cancel", Descriptor: "()I",
		MaxStack: 1, Code: []byte{0x00, 0x00, 0x10, 42, 0xac}}
	budget := uint64(100)
	_, _, err := vm.execute(ctx, &Class{Name: "Cancellation"}, method, 0, nil, &budget)
	if !errors.Is(err, context.Canceled) || vm.Instructions != 2 {
		t.Fatalf("cancellation returned %v after %d instructions, want canceled after 2", err, vm.Instructions)
	}
}

func TestReusedFrameKeepsUninitializedLocalsUnset(t *testing.T) {
	vm := &VM{}
	class := &Class{Name: "Reuse"}
	method := Method{AccessFlags: AccessStatic, Name: "test", Descriptor: "()I",
		MaxLocals: 1, MaxStack: 1, Code: []byte{0x10, 42, 0x3b, 0x1a, 0xac}}
	budget := uint64(100)
	value, hasValue, err := vm.execute(context.Background(), class, method, 0, nil, &budget)
	check(t, err)
	if !hasValue || value != IntValue(42) || len(vm.framePool) != 1 {
		t.Fatal("completed call did not return 42 and retain its frame")
	}
	method.Code = []byte{0x1a, 0xac}
	if _, _, err := vm.execute(context.Background(), class, method, 0, nil, &budget); err == nil {
		t.Fatal("a reused frame exposed the previous call's local")
	}
}

func TestCompletedPooledFramesAreNotGCRoots(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	reference := vm.NewObject("java/lang/Object", nil)
	current := vm.borrowFrame(1, 1)
	current.locals[0] = ReferenceValue(reference)
	current.stack = append(current.stack, ReferenceValue(reference))
	vm.releaseFrame(current)
	check(t, vm.collectGarbage())
	if vm.heap[reference] != nil {
		t.Fatal("a completed call's pooled buffers retained an otherwise unreachable object")
	}
}

func TestPooledFrameReuseKeepsSleepingThreadContinuation(t *testing.T) {
	vm, err := New(map[string][]byte{"Worker": syntheticThreadClass(t)})
	check(t, err)
	target, err := vm.allocateObject("Worker")
	check(t, err)
	thread := vm.NewObject("java/lang/Thread", nil)
	invokeTestNative(t, vm, "java/lang/Thread", "<init>", "(Ljava/lang/Runnable;)V", thread, ReferenceValue(target))
	invokeTestNative(t, vm, "java/lang/Thread", "start", "()V", thread)
	state, err := vm.thread(thread)
	check(t, err)
	if len(state.continuation) != 2 || len(vm.framePool) == 0 {
		t.Fatal("worker did not park with reusable execution buffers")
	}
	method := Method{AccessFlags: AccessStatic, Name: "other", Descriptor: "()I",
		MaxLocals: 16, MaxStack: 16, Code: []byte{0x10, 42, 0x3b, 0x1a, 0xac}}
	for n := 0; n < 4; n++ {
		budget := uint64(100)
		_, _, err := vm.execute(context.Background(), &Class{Name: "Other"}, method, 0, nil, &budget)
		check(t, err)
	}
	check(t, vm.Advance(context.Background(), time.Millisecond, nil))
	counter := vm.classes["Worker"].static[fieldStorageKey("Worker", "counter", "I")]
	if counter != IntValue(11) || state.active {
		t.Fatal("reusing returned frames changed a sleeping worker's continuation")
	}
}

func TestPooledFrameReuseKeepsYieldedClassInitializer(t *testing.T) {
	vm, err := New(map[string][]byte{"Worker": syntheticThreadClassWithInitializer(t, true)})
	check(t, err)
	target, err := vm.allocateObject("Worker")
	check(t, err)
	thread := vm.NewObject("java/lang/Thread", nil)
	invokeTestNative(t, vm, "java/lang/Thread", "<init>", "(Ljava/lang/Runnable;)V", thread, ReferenceValue(target))
	invokeTestNative(t, vm, "java/lang/Thread", "start", "()V", thread)
	state, err := vm.thread(thread)
	check(t, err)
	if vm.classes["Worker"].initState != classInitializing || !state.active || len(state.continuation) == 0 {
		t.Fatal("worker did not park in its class initializer")
	}
	clobberPooledExecutionFrames(t, vm)
	// Do not save/restore: that would detach even incorrectly aliased buffers.
	check(t, vm.Advance(context.Background(), time.Nanosecond, nil))
	counter := fieldStorageKey("Worker", "counter", "I")
	if vm.classes["Worker"].initState != classInitialized || vm.classes["Worker"].static[counter] != IntValue(8) {
		t.Fatal("reusing execution buffers changed the resumed class initializer")
	}
	clobberPooledExecutionFrames(t, vm)
	check(t, vm.Advance(context.Background(), time.Millisecond, nil))
	if vm.classes["Worker"].static[counter] != IntValue(18) || state.active || len(state.continuation) != 0 {
		t.Fatal("reusing execution buffers changed the worker after initialization")
	}
}

func clobberPooledExecutionFrames(t *testing.T, vm *VM) {
	t.Helper()
	if len(vm.framePool) == 0 {
		t.Fatal("fixture retained no reusable execution frames")
	}
	// Borrow every entry at once, retaining its original backing arrays. Sequential
	// method calls would only overwrite the last LIFO entry; requesting larger
	// buffers could allocate replacements and miss an alias in the old buffers.
	borrowed := make([]*frame, 0, len(vm.framePool))
	for len(vm.framePool) != 0 {
		pooled := vm.framePool[len(vm.framePool)-1]
		current := vm.borrowFrame(cap(pooled.locals), cap(pooled.stack))
		for index := range current.locals {
			current.locals[index] = IntValue(-12345)
		}
		for len(current.stack) < cap(current.stack) {
			current.stack = append(current.stack, IntValue(-12345))
		}
		borrowed = append(borrowed, current)
	}
	for _, current := range borrowed {
		vm.releaseFrame(current)
	}
}

func BenchmarkExecuteSmallMethod(b *testing.B) {
	for _, reuse := range []bool{false, true} {
		name := "fresh"
		if reuse {
			name = "reused"
		}
		b.Run(name, func(b *testing.B) {
			vm := &VM{framePool: make([]*frame, 0, 64)}
			class := &Class{Name: "Add"}
			method := Method{AccessFlags: AccessStatic, Name: "add", Descriptor: "(II)I",
				MaxLocals: 2, MaxStack: 2, Code: []byte{0x1a, 0x1b, 0x60, 0xac}}
			args := []Value{IntValue(31), IntValue(11)}
			ctx := context.Background()
			budget := uint64(100)
			_, _, _ = vm.execute(ctx, class, method, 0, args, &budget)
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				if !reuse {
					vm.framePool = vm.framePool[:0]
				}
				budget = 100
				value, hasValue, err := vm.execute(ctx, class, method, 0, args, &budget)
				if err != nil || !hasValue || value != IntValue(42) {
					b.Fatalf("add returned %v, %v, %v", value, hasValue, err)
				}
			}
		})
	}
}
