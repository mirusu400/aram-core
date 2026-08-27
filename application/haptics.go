package application

import (
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

// guestServicesLocked returns the shared services of the active guest runtime,
// or nil when no WIPI/KTF guest is loaded. Callers hold m.mu. KTF is checked
// first to match guestTimeLocked, so the two never disagree about which runtime
// owns the clock a vibration deadline is measured against.
func (m *Machine) guestServicesLocked() *shared.Services {
	switch {
	case m.ktf != nil && m.ktf.Services != nil:
		return m.ktf.Services
	case m.wipi != nil && m.wipi.Services != nil:
		return m.wipi.Services
	}
	return nil
}

// Vibration reports the guest's requested haptic feedback: motor strength
// (0-100) and the time remaining before it stops. It returns (0, 0) when no
// guest is loaded or no vibration is active, so a host driver can actuate a
// real rumble motor or phone vibrator without reaching into guest state.
func (m *Machine) Vibration() (uint8, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	services := m.guestServicesLocked()
	if services == nil || services.Device == nil || services.Clock == nil {
		return 0, 0
	}
	level, until := services.Device.Vibration()
	return hostVibration(level, until, services.Clock.Monotonic())
}

// hostVibration turns the device's stored (level, deadline) into a host-facing
// (level, remaining) pair, collapsing an inactive or already-expired pulse to
// zero. It is pure so the deadline arithmetic can be tested without a machine.
func hostVibration(level uint8, until, now time.Duration) (uint8, time.Duration) {
	if level == 0 || until == 0 {
		return 0, 0
	}
	remaining := until - now
	if remaining <= 0 {
		return 0, 0
	}
	return level, remaining
}
