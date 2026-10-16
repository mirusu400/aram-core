package application

import (
	"fmt"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

func (m *Machine) SupportsPointer() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && m.minigame != nil && m.minigame.SupportsPointer()
}

func (m *Machine) QueuePointer(event machinecore.PointerInputEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if m.minigame == nil || !m.minigame.SupportsPointer() {
		return fmt.Errorf("loaded title does not support pointer input")
	}
	if event.X >= m.frame.Bounds().Dx() || event.Y >= m.frame.Bounds().Dy() {
		return fmt.Errorf("pointer coordinates are outside the guest display")
	}
	if event.At == 0 {
		event.At = m.wipi.Services.Clock.Monotonic()
	}
	if !m.minigame.QueuePointer(event.X, event.Y, event.Pressed, event.At) {
		return fmt.Errorf("EADS pointer input queue is full")
	}
	return nil
}

var _ machinecore.PointerInputMachine = (*Machine)(nil)
