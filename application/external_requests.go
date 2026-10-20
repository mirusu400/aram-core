package application

import (
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

var _ machinecore.VibrationMachine = (*Machine)(nil)
var _ machinecore.VibrationMachine = (*gnex32Machine)(nil)
var _ machinecore.ExternalRequestMachine = (*Machine)(nil)
var _ machinecore.ExternalRequestMachine = (*gnex32Machine)(nil)

// Vibration exposes GNEX handset vibration using the same optional host
// contract as the other runtimes.
func (m *gnex32Machine) Vibration() (uint8, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime == nil {
		return 0, 0
	}
	level, until := m.runtime.Vibration()
	return hostVibration(level, until, m.runtime.AudioTime())
}

func (m *Machine) ExternalRequests() []machinecore.ExternalRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	services := m.guestServicesLocked()
	if services == nil || services.Device == nil {
		return nil
	}
	return hostExternalRequests(services.Device.Requests())
}

func (m *Machine) ClearExternalRequests(through uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	services := m.guestServicesLocked()
	if services != nil && services.Device != nil {
		services.Device.ClearRequests(through)
	}
}

func (m *gnex32Machine) ExternalRequests() []machinecore.ExternalRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime == nil {
		return nil
	}
	return hostExternalRequests(m.runtime.ExternalRequests())
}

func (m *gnex32Machine) ClearExternalRequests(through uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime != nil {
		m.runtime.ClearExternalRequests(through)
	}
}

func hostExternalRequests(requests []shared.ExternalRequest) []machinecore.ExternalRequest {
	result := make([]machinecore.ExternalRequest, len(requests))
	for index, request := range requests {
		result[index] = machinecore.ExternalRequest{
			Sequence: request.Sequence,
			GuestNS:  request.AtNS,
			Kind:     machinecore.ExternalRequestKind(request.Kind),
			Target:   request.Target,
			Data:     append([]byte(nil), request.Data...),
		}
	}
	return result
}
