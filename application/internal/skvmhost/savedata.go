package skvmhost

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

const skvmSaveDataMagic = "ARAMSKVMSAVE"

type skvmSaveDataEnvelope struct {
	Magic   string
	Storage shared.StoragePersistenceState
}

// ExportSaveData serializes the writable files and Java RMS record stores that
// must survive closing and reopening an SKVM title.
func (m *Machine) ExportSaveData() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("SKVM machine is closed")
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
	default:
		return nil, fmt.Errorf("export SKVM save data from %s: %w", m.state, guest.ErrInvalidState)
	}
	persistence := m.services.Storage.ExportPersistence()
	for _, store := range persistence.RecordStores {
		if store.Owner != m.owner {
			return nil, fmt.Errorf(
				"export SKVM save data: record store %q belongs to owner %d, want %d",
				store.Name,
				store.Owner,
				m.owner,
			)
		}
	}
	if len(persistence.Directories) == 0 &&
		len(persistence.Files) == 0 &&
		len(persistence.RecordStores) == 0 {
		return nil, nil
	}
	var output bytes.Buffer
	if err := gob.NewEncoder(&output).Encode(skvmSaveDataEnvelope{
		Magic:   skvmSaveDataMagic,
		Storage: persistence,
	}); err != nil {
		return nil, fmt.Errorf("encode SKVM save data: %w", err)
	}
	return output.Bytes(), nil
}

// ImportSaveData restores writable storage before the title starts. Record
// stores are rebound to this VM's process-local owner ID because owner IDs are
// not stable across machine instances.
func (m *Machine) ImportSaveData(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
	default:
		return fmt.Errorf("import SKVM save data from %s: %w", m.state, guest.ErrInvalidState)
	}
	var envelope skvmSaveDataEnvelope
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode SKVM save data: %w", err)
	}
	if envelope.Magic != skvmSaveDataMagic {
		return fmt.Errorf("SKVM save data has unexpected magic %q", envelope.Magic)
	}
	for index := range envelope.Storage.RecordStores {
		envelope.Storage.RecordStores[index].Owner = m.owner
	}
	if err := m.services.Storage.ImportPersistence(envelope.Storage); err != nil {
		return fmt.Errorf("import SKVM save data: %w", err)
	}
	return nil
}
