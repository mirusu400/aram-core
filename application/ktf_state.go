package application

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
)

func (m *Machine) writeKTFState(writer *guest.StateWriter) error {
	return ktfrt.WriteState(m.ktf, m.cpu, m.ktfStarted, writer)
}

func (m *Machine) parseKTFState(
	decoder *guest.StateDecoder,
) (*ktfrt.SavedState, error) {
	return ktfrt.ParseState(m.ktf, decoder)
}

func (m *Machine) restoreKTFState(saved *ktfrt.SavedState) error {
	return ktfrt.RestoreState(m.ktf, m.cpu, saved, &m.ktfStarted)
}
