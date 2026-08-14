package application

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
)

func (m *Machine) writeWIPIState(writer *guest.StateWriter) error {
	return wipirt.WriteState(m.wipi, m.cpu, writer)
}

func (m *Machine) parseWIPIState(
	decoder *guest.StateDecoder,
) (*wipirt.SavedState, error) {
	return wipirt.ParseState(m.wipi, decoder)
}
