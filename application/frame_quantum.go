package application

import "time"

// FrameQuantum reports how much guest time one StepFrame advances.
//
// The virtual clock never consults host wall time, so a machine only runs at
// handset speed if its driver issues quanta at the rate this reports: one
// second of guest time needs time.Second/FrameQuantum() calls. The value is
// not the same for every runtime, so a driver that assumes a fixed sixty calls
// per second runs some titles slow.
func (m *Machine) FrameQuantum() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.ktf != nil:
		return ktfFrameDuration
	default:
		return wipiFrameDuration
	}
}

// FrameQuantum reports how much guest time one StepFrame advances for a SKVM
// machine, which paces itself from the shared service configuration.
func (m *skvmMachine) FrameQuantum() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.services == nil || m.services.Config.FrameDuration <= 0 {
		return wipiFrameDuration
	}
	return m.services.Config.FrameDuration
}
