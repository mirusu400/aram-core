package ktf

import (
	"context"
	"strings"
	"testing"
)

func newPaintStallRuntime(card uint32, pending *Task) *Runtime {
	return &Runtime{
		Tasks:              []*Task{pending},
		dirtyCards:         map[uint32]bool{card: true},
		deferredPaintCards: make(map[*Task][]uint32),
		deferredShownCards: make(map[*Task]map[uint32]bool),
		PaintTasks:         map[uint32]*Task{card: pending},
	}
}

// TestPaintCardReportsStallWhilePaintTaskSleeps covers the case that made a
// KTF title burn a whole quantum: the card's paint task is parked in a guest
// Thread.sleep, so every further repaint coalesces and no frame can appear
// until the virtual clock advances at the end of the quantum.
func TestPaintCardReportsStallWhilePaintTaskSleeps(t *testing.T) {
	const card = uint32(0x10001000)
	pending := &Task{WakeAtMS: 5000}
	runtime := newPaintStallRuntime(card, pending)
	runtime.TickMS = 4000

	check(t, runtime.paintCard(context.Background(), card))
	if !runtime.PaintStalled {
		t.Fatal("a repaint dropped for a sleeping paint task did not stall")
	}
	if runtime.dirtyCards[card] {
		t.Fatal("coalesced card stayed dirty")
	}
	if runtime.PaintTasks[card] != pending {
		t.Fatal("coalescing replaced the pending paint task")
	}
}

// TestPaintCardDoesNotStallForRunnablePaintTask keeps the quantum running when
// the pending paint task is merely waiting its turn: it can still be scheduled
// and present within this quantum.
func TestPaintCardDoesNotStallForRunnablePaintTask(t *testing.T) {
	const card = uint32(0x10001000)
	pending := &Task{}
	runtime := newPaintStallRuntime(card, pending)
	runtime.TickMS = 4000

	check(t, runtime.paintCard(context.Background(), card))
	if runtime.PaintStalled {
		t.Fatal("a runnable paint task stalled the quantum")
	}
}

// TestPaintCardDoesNotStallForElapsedSleep pins the boundary: once the clock
// has reached the wake time the task is runnable again, and nextRunnableTask
// clears the deadline on its next pass.
func TestPaintCardDoesNotStallForElapsedSleep(t *testing.T) {
	const card = uint32(0x10001000)
	pending := &Task{WakeAtMS: 4000}
	runtime := newPaintStallRuntime(card, pending)
	runtime.TickMS = 4000

	check(t, runtime.paintCard(context.Background(), card))
	if runtime.PaintStalled {
		t.Fatal("an elapsed sleep deadline stalled the quantum")
	}
}

// newPaintDeferInputRuntime builds a Runtime that satisfies every
// precondition paintCard checks before it leaves a card dirty so a key
// already waiting at the machine can reach it before the card repaints
// itself: deferred threads are on, a key is waiting, the card painted at
// least once already, it is the card on the default display, and nothing
// else (a pending WIPI-C timer task, an exhausted task table) would already
// have been caught by an earlier guard.
func newPaintDeferInputRuntime(card, display uint32, inputWaiting bool) *Runtime {
	return &Runtime{
		DeferThreads:          true,
		InputWaiting:          inputWaiting,
		dirtyCards:            map[uint32]bool{card: true},
		paintInitializedCards: map[uint32]bool{card: true},
		DisplayCards:          map[uint32]uint32{display: card},
		DefaultDisplay:        display,
		PaintTasks:            map[uint32]*Task{},
		traceMode:             KTFTraceFull,
	}
}

// hostTraceHas reports whether a host trace line contains substr.
func hostTraceHas(trace []string, substr string) bool {
	for _, line := range trace {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// TestPaintCardDefersForWaitingInput pins issues #159 #165 #169 #170 #186: a
// title whose paint handler ends by calling repaint() again (아포칼립스's
// title screen, 크로스워드's loading bar) used to re-queue its own next paint
// right here, before the machine got a chance to offer the key it was
// holding. CanQueueKeyEvent refuses a key while a paint is pending, so the
// key never reached the card and the machine's input queue filled up. The
// fix leaves the card dirty instead of queuing a new paint task, so the key
// task created for the waiting input takes the card over instead.
func TestPaintCardDefersForWaitingInput(t *testing.T) {
	const card = uint32(0x10001000)
	const display = uint32(1)
	runtime := newPaintDeferInputRuntime(card, display, true)

	check(t, runtime.paintCard(context.Background(), card))
	if !runtime.dirtyCards[card] {
		t.Fatal("a card deferred for waiting input was not left dirty")
	}
	if _, queued := runtime.PaintTasks[card]; queued {
		t.Fatal("a card deferred for waiting input queued a paint task anyway")
	}
	if !hostTraceHas(runtime.HostTrace, "java_paint_defer_input") {
		t.Fatalf(
			"deferring for waiting input did not trace java_paint_defer_input, trace=%v",
			runtime.HostTrace,
		)
	}
}

// TestPaintCardDoesNotDeferForWaitingInputWithoutWaitingKey proves the
// defer-for-input branch is conditioned on InputWaiting: with no key waiting
// at the machine, paintCard must not take it, even though every other
// precondition (deferred threads, an already-painted card on the default
// display, no competing timer task) still holds. Everything past that branch
// needs a live CPU and Java heap this test does not build, so paintCard is
// expected to fail past the branch rather than succeed - the point here is
// only that it never traces the defer-for-input line on its way there.
func TestPaintCardDoesNotDeferForWaitingInputWithoutWaitingKey(t *testing.T) {
	const card = uint32(0x10001000)
	const display = uint32(1)
	runtime := newPaintDeferInputRuntime(card, display, false)

	if err := runtime.paintCard(context.Background(), card); err == nil {
		t.Fatal("paintCard fell through to a real paint with no CPU or Java heap and did not fail")
	}
	if hostTraceHas(runtime.HostTrace, "java_paint_defer_input") {
		t.Fatalf(
			"paintCard deferred for waiting input with InputWaiting false, trace=%v",
			runtime.HostTrace,
		)
	}
}

// TestPaintCardDoesNotDeferForWaitingInputOnNonDefaultCard proves the other
// half of the same guard: a card that is not the one currently shown on the
// default display must not have its paint deferred for a waiting key, even
// though a key is waiting and the card otherwise qualifies. QueueKeyEvent
// only ever offers a key to r.DisplayCards[r.DefaultDisplay], so deferring
// any other card's paint here would leave it dirty for nothing.
func TestPaintCardDoesNotDeferForWaitingInputOnNonDefaultCard(t *testing.T) {
	const card = uint32(0x10001000)
	const otherCard = uint32(0x10002000)
	const display = uint32(1)
	runtime := newPaintDeferInputRuntime(card, display, true)
	// Point the default display at a different card, so card no longer
	// matches r.DisplayCards[r.DefaultDisplay].
	runtime.DisplayCards[display] = otherCard

	if err := runtime.paintCard(context.Background(), card); err == nil {
		t.Fatal("paintCard fell through to a real paint with no CPU or Java heap and did not fail")
	}
	if hostTraceHas(runtime.HostTrace, "java_paint_defer_input") {
		t.Fatalf(
			"paintCard deferred for waiting input on a non-default-display card, trace=%v",
			runtime.HostTrace,
		)
	}
}
