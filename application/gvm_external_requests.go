package application

import (
	"fmt"

	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

var _ machinecore.ExternalRequestMachine = (*gvmMachine)(nil)

// gvmRequestOwner tags the handset requests of the single GVM script.
const gvmRequestOwner shared.OwnerID = 1

// gvmHostRequests records the handset actions GVM opcodes c4 (open the
// browser) and c5 (exit) request. The VM calls it while the machine lock is
// held, so it never takes that lock; the machine stops after the dispatch.
type gvmHostRequests struct {
	device *shared.Device
	clock  *shared.Clock
	close  bool
}

func newGVMHostRequests(clock *shared.Clock) (*gvmHostRequests, error) {
	device, err := shared.NewDevice(shared.DeviceConfig{}, shared.DeviceLimits{})
	if err != nil {
		return nil, err
	}
	return &gvmHostRequests{device: device, clock: clock}, nil
}

func (r *gvmHostRequests) RequestGVMBrowser(url string) error {
	if _, err := r.device.Request(gvmRequestOwner, shared.RequestBrowser, url, nil, r.clock.Monotonic()); err != nil {
		return err
	}
	r.close = true
	return nil
}

func (r *gvmHostRequests) RequestGVMExit() error {
	r.close = true
	return nil
}

func (m *gvmMachine) ExternalRequests() []machinecore.ExternalRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.hostRequests == nil {
		return nil
	}
	return m.requestIDs.project(m.hostRequests.device.Requests())
}

func (m *gvmMachine) ClearExternalRequests(through uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.hostRequests == nil {
		return
	}
	if guestThrough, ok := m.requestIDs.acknowledge(through); ok {
		m.hostRequests.device.ClearRequests(guestThrough)
	}
}

// finishGVMCloseRequestLocked stops a machine whose dispatch ended in c4 or
// c5. The native host loop stops the script without another guest event.
func (m *gvmMachine) finishGVMCloseRequestLocked() error {
	if m.hostRequests == nil || !m.hostRequests.close {
		return nil
	}
	m.hostRequests.close = false
	if err := m.discardGVMAudioLocked(); err != nil {
		m.state = machinecore.StateFaulted
		return fmt.Errorf("stop GVM audio: %w", err)
	}
	m.state = machinecore.StateStopped
	return nil
}
