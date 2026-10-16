package core

import (
	"fmt"
	"time"
)

// PointerInputEvent uses guest framebuffer coordinates and guest-relative time.
// Zero At applies a live press or release at the machine's current time.
type PointerInputEvent struct {
	X, Y    int
	Pressed bool
	At      time.Duration
}

func (e PointerInputEvent) Validate() error {
	if e.X < 0 || e.Y < 0 || e.X > 0x7fff || e.Y > 0x7fff {
		return fmt.Errorf("pointer coordinates (%d,%d) are out of range", e.X, e.Y)
	}
	if e.At < 0 {
		return fmt.Errorf("pointer event time %s is negative", e.At)
	}
	return nil
}

// PointerInputMachine is optional; keypad-only machines retain Machine's contract.
type PointerInputMachine interface {
	SupportsPointer() bool
	QueuePointer(PointerInputEvent) error
}
