package skvm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
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
			if state.started {
				return Value{}, false, vm.newThrowable("java/lang/IllegalThreadStateException", "")
			}
			state.started = true
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
	previousBudget := vm.threadBudget
	state.blockedClip = 0
	vm.runningThread = reference
	vm.threadFrameBase = len(vm.frames)
	vm.threadBudget = threadInstructionQuantum
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
	vm.threadBudget = previousBudget
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

// blockThreadOnClip parks the running thread until the clip stops. It reports
// whether the caller is a thread that can be parked at all: a play made from
// the MIDlet's own callback has no worker to suspend, so it stays synchronous.
func (vm *VM) blockThreadOnClip(clip shared.ServiceID) (*threadYield, bool) {
	if vm.runningThread == 0 || clip == 0 {
		return nil, false
	}
	state, err := vm.thread(vm.runningThread)
	if err != nil {
		return nil, false
	}
	info, err := vm.services.Media.Info(vm.serviceOwner, clip)
	// A clip the mixer will never finish - silence, or a source it could not
	// decode - would park the thread for good, so only audible playback
	// blocks.
	if err != nil || info.State != shared.ClipPlaying ||
		!info.Decoded || info.Duration <= 0 {
		return nil, false
	}
	state.blockedClip = clip
	return &threadYield{}, true
}

// releaseClipWaiters resumes every thread parked on a clip that has just been
// stopped, cleared, or destroyed. They run before the caller continues, the
// way a handset's audio thread wakes inside stop() rather than a frame later:
// a waiter that resumed afterwards would close the clip a restarted worker had
// already opened, and cut the new sound off.
func (vm *VM) releaseClipWaiters(ctx context.Context, clip shared.ServiceID) error {
	if clip == 0 {
		return nil
	}
	references := make([]uint32, 0)
	for reference, object := range vm.heap {
		state, ok := object.Native.(*threadState)
		if ok && state.active && state.blockedClip == clip &&
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
		if !state.active || state.blockedClip != clip {
			continue
		}
		if err := vm.runThread(ctx, reference, state); err != nil {
			return err
		}
	}
	return nil
}

// threadBlocked reports whether a parked thread's clip is still playing. A
// clip that was stopped, ran out, or was destroyed releases its waiter.
func (vm *VM) threadBlocked(state *threadState) bool {
	if state.blockedClip == 0 {
		return false
	}
	info, err := vm.services.Media.Info(vm.serviceOwner, state.blockedClip)
	return err == nil && info.State == shared.ClipPlaying
}

func (vm *VM) runReadyThreads(ctx context.Context) error {
	now := vm.services.Clock.Monotonic()
	references := make([]uint32, 0)
	for reference, object := range vm.heap {
		state, ok := object.Native.(*threadState)
		if ok && state.active && state.wakeAt <= now &&
			reference != vm.runningThread && !vm.threadBlocked(state) {
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
		if state.active && state.wakeAt <= vm.services.Clock.Monotonic() &&
			!vm.threadBlocked(state) {
			if err := vm.runThread(ctx, reference, state); err != nil {
				return err
			}
		}
	}
	return nil
}
