package application

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/application/internal/minigame"
)

func (m *Machine) writeMinigameState(writer *guest.StateWriter) error {
	return m.minigame.WriteState(writer)
}

func (m *Machine) parseMinigameState(
	decoder *guest.StateDecoder,
) (*minigame.SavedState, error) {
	return minigame.ParseState(m.minigame, decoder)
}
