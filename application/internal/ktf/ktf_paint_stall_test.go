package ktf

import (
	"context"
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

	if err := runtime.paintCard(context.Background(), card); err != nil {
		t.Fatal(err)
	}
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

	if err := runtime.paintCard(context.Background(), card); err != nil {
		t.Fatal(err)
	}
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

	if err := runtime.paintCard(context.Background(), card); err != nil {
		t.Fatal(err)
	}
	if runtime.PaintStalled {
		t.Fatal("an elapsed sleep deadline stalled the quantum")
	}
}
