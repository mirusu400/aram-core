package application

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
)

func (m *Machine) writeRaptorState(writer *guest.StateWriter) error {
	return raptorrt.WriteState(m.raptor, m.cpu, writer)
}

func (m *Machine) parseRaptorState(
	decoder *guest.StateDecoder,
) (*raptorrt.SavedState, error) {
	return raptorrt.ParseState(m.raptor, decoder)
}

func (m *Machine) restoreRaptorState(state *raptorrt.SavedState) error {
	return raptorrt.RestoreState(m.raptor, m.cpu, state)
}
