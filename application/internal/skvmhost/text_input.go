package skvmhost

import machinecore "github.com/mirusu400/aram-core/core"

// TextInputActive exposes only the guest's focused-input hint. The VM object
// and editable field remain private to the Java runtime.
func (m *Machine) TextInputActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && m.vm != nil && m.vm.TextInputActive()
}

var _ machinecore.TextInputMachine = (*Machine)(nil)
