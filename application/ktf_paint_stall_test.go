package application

import (
	"context"
	"testing"
)

func newPaintStallRuntime(card uint32, pending *ktfTask) *ktfRuntime {
	return &ktfRuntime{
		tasks:              []*ktfTask{pending},
		dirtyCards:         map[uint32]bool{card: true},
		deferredPaintCards: make(map[*ktfTask][]uint32),
		deferredShownCards: make(map[*ktfTask]map[uint32]bool),
		paintTasks:         map[uint32]*ktfTask{card: pending},
	}
}

// TestPaintCardReportsStallWhilePaintTaskSleeps covers the case that made a
// KTF title burn a whole quantum: the card's paint task is parked in a guest
// Thread.sleep, so every further repaint coalesces and no frame can appear
// until the virtual clock advances at the end of the quantum.
func TestPaintCardReportsStallWhilePaintTaskSleeps(t *testing.T) {
	const card = uint32(0x10001000)
	pending := &ktfTask{wakeAtMS: 5000}
	runtime := newPaintStallRuntime(card, pending)
	runtime.tickMS = 4000

	if err := runtime.paintCard(context.Background(), card); err != nil {
		t.Fatal(err)
	}
	if !runtime.paintStalled {
		t.Fatal("a repaint dropped for a sleeping paint task did not stall")
	}
	if runtime.dirtyCards[card] {
		t.Fatal("coalesced card stayed dirty")
	}
	if runtime.paintTasks[card] != pending {
		t.Fatal("coalescing replaced the pending paint task")
	}
}

// TestPaintCardDoesNotStallForRunnablePaintTask keeps the quantum running when
// the pending paint task is merely waiting its turn: it can still be scheduled
// and present within this quantum.
func TestPaintCardDoesNotStallForRunnablePaintTask(t *testing.T) {
	const card = uint32(0x10001000)
	pending := &ktfTask{}
	runtime := newPaintStallRuntime(card, pending)
	runtime.tickMS = 4000

	if err := runtime.paintCard(context.Background(), card); err != nil {
		t.Fatal(err)
	}
	if runtime.paintStalled {
		t.Fatal("a runnable paint task stalled the quantum")
	}
}

// TestPaintCardDoesNotStallForElapsedSleep pins the boundary: once the clock
// has reached the wake time the task is runnable again, and nextRunnableTask
// clears the deadline on its next pass.
func TestPaintCardDoesNotStallForElapsedSleep(t *testing.T) {
	const card = uint32(0x10001000)
	pending := &ktfTask{wakeAtMS: 4000}
	runtime := newPaintStallRuntime(card, pending)
	runtime.tickMS = 4000

	if err := runtime.paintCard(context.Background(), card); err != nil {
		t.Fatal(err)
	}
	if runtime.paintStalled {
		t.Fatal("an elapsed sleep deadline stalled the quantum")
	}
}
