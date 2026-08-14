package skvm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

func (vm *VM) installThreadNatives() {
	vm.RegisterNative("java/lang/Thread", "<init>", "()V", func(
		_ context.Context,
		vm *VM,
		receiver uint32,
		_ []Value,
	) (Value, bool, error) {
		return Value{}, false, vm.setNative(receiver, &threadState{target: receiver})
	})
	vm.RegisterNative(
		"java/lang/Thread",
		"<init>",
		"(Ljava/lang/Runnable;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			target, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.setNative(receiver, &threadState{target: target})
		},
	)
	vm.RegisterNative(
		"java/lang/Thread",
		"start",
		"()V",
		func(ctx context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.thread(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if state.active {
				// Several SK-VM MIDlets call startApp again after a lifecycle
				// resume and unconditionally start their long-lived worker.
				// The legacy runtime treated an already-active start as a
				// resume no-op.
				return Value{}, false, nil
			}
			state.active = true
			state.wakeAt = vm.services.Clock.Monotonic()
			return Value{}, false, vm.runThread(ctx, receiver, state)
		},
	)
	vm.RegisterNative(
		"java/lang/Thread",
		"yield",
		"()V",
		func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
			if vm.runningThread != 0 {
				return Value{}, false, &threadYield{delay: time.Nanosecond}
			}
			return Value{}, false, nil
		},
	)
	vm.RegisterNative("java/lang/Thread", "setPriority", "(I)V", nativeVoid)
	vm.RegisterNative(
		"java/lang/Thread",
		"isAlive",
		"()Z",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.thread(receiver)
			if err != nil {
				return Value{}, false, err
			}
			return boolValue(state.active), true, nil
		},
	)
	vm.RegisterNative(
		"java/lang/Thread",
		"sleep",
		"(J)V",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			if len(args) != 1 {
				return Value{}, false, fmt.Errorf("Thread.sleep argument mismatch")
			}
			duration, err := args[0].Long()
			if err != nil {
				return Value{}, false, err
			}
			if duration < 0 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			if duration > int64((^uint64(0)>>1)/uint64(time.Millisecond)) {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			delay := time.Duration(duration) * time.Millisecond
			if vm.runningThread != 0 {
				return Value{}, false, &threadYield{delay: delay}
			}
			if err := vm.services.Advance(
				vm.serviceOwner,
				delay,
			); err != nil {
				return Value{}, false, err
			}
			return Value{}, false, nil
		},
	)
}

func (vm *VM) thread(reference uint32) (*threadState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid Thread reference")
	}
	state, ok := object.Native.(*threadState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a Thread", reference)
	}
	return state, nil
}

func (vm *VM) runThread(
	ctx context.Context,
	reference uint32,
	state *threadState,
) error {
	if !state.active {
		return nil
	}
	previous := vm.runningThread
	previousBase := vm.threadFrameBase
	vm.runningThread = reference
	vm.threadFrameBase = len(vm.frames)
	var err error
	if len(state.continuation) != 0 {
		continuation := state.continuation
		state.continuation = nil
		budget := vm.remainingBudget()
		_, _, err = vm.resumeFrames(ctx, continuation, 0, &budget)
	} else {
		_, _, err = vm.InvokeVirtual(ctx, state.target, "run", "()V")
	}
	vm.runningThread = previous
	vm.threadFrameBase = previousBase
	var yielded *threadYield
	if errors.As(err, &yielded) {
		now := vm.services.Clock.Monotonic()
		if yielded.delay < 0 || yielded.delay > time.Duration(^uint64(0)>>1)-now {
			state.active = false
			return fmt.Errorf("invalid thread yield duration %s", yielded.delay)
		}
		state.wakeAt = now + yielded.delay
		return nil
	}
	state.active = false
	state.continuation = nil
	if errors.Is(err, ErrMethodNotFound) {
		return nil
	}
	return err
}

func (vm *VM) runReadyThreads(ctx context.Context) error {
	now := vm.services.Clock.Monotonic()
	references := make([]uint32, 0)
	for reference, object := range vm.heap {
		state, ok := object.Native.(*threadState)
		if ok && state.active && state.wakeAt <= now &&
			reference != vm.runningThread {
			references = append(references, reference)
		}
	}
	sort.Slice(references, func(left, right int) bool {
		return references[left] < references[right]
	})
	for _, reference := range references {
		state, err := vm.thread(reference)
		if err != nil {
			return err
		}
		if state.active && state.wakeAt <= vm.services.Clock.Monotonic() {
			if err := vm.runThread(ctx, reference, state); err != nil {
				return err
			}
		}
	}
	return nil
}
