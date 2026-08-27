package application

import (
	"bytes"
	"encoding/gob"
	"fmt"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

// saveDataMagic guards ImportSaveData against unrelated blobs.
const saveDataMagic = "ARAMSAVE"

// saveDataEnvelope is the on-disk form of a title's restart-persistent storage.
type saveDataEnvelope struct {
	Magic   string
	Storage shared.StoragePersistenceState
}

// persistentStorage returns the active backend's storage service, or nil when
// the machine has no storage to persist.
func (m *Machine) persistentStorage() *shared.Storage {
	switch {
	case m.ktf != nil:
		return m.ktf.Services.Storage
	case m.wipi != nil:
		return m.wipi.Services.Storage
	default:
		return nil
	}
}

// ExportSaveData serializes the machine's restart-persistent storage — the
// guest's private files and record stores — so the host can carry a title's
// saves across relaunches the way handset flash survives an app exit. It
// returns nil when the backend has no persistent storage. Callable while the
// machine is Ready, Paused, or Stopped (for example after a Clet calls
// MC_knlExit), but not while it is Running.
func (m *Machine) ExportSaveData() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, cpu.ErrClosed
	}
	if m.state == machinecore.StateRunning {
		return nil, fmt.Errorf("export save data from %s: %w", m.state, ErrInvalidState)
	}
	storage := m.persistentStorage()
	if storage == nil {
		return nil, nil
	}
	envelope := saveDataEnvelope{
		Magic:   saveDataMagic,
		Storage: storage.ExportPersistence(),
	}
	if len(envelope.Storage.Files) == 0 &&
		len(envelope.Storage.Directories) == 0 &&
		len(envelope.Storage.RecordStores) == 0 {
		return nil, nil
	}
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(envelope); err != nil {
		return nil, fmt.Errorf("encode save data: %w", err)
	}
	return buffer.Bytes(), nil
}

// ImportSaveData restores previously exported save data into storage. Call it
// after Load and before Start so the guest's first read observes its saves.
// Empty input is a no-op so a first launch (no save yet) is not an error.
func (m *Machine) ImportSaveData(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	storage := m.persistentStorage()
	if storage == nil {
		return nil
	}
	var envelope saveDataEnvelope
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode save data: %w", err)
	}
	if envelope.Magic != saveDataMagic {
		return fmt.Errorf("save data has unexpected magic %q", envelope.Magic)
	}
	if err := storage.ImportPersistence(envelope.Storage); err != nil {
		return fmt.Errorf("import save data: %w", err)
	}
	// Importing replaces every record store and mints fresh service IDs, so the
	// backend's own database bookkeeping is stale until it reopens the restored
	// stores by name.
	return m.adoptPersistedStorage()
}

// adoptPersistedStorage lets the active backend rebuild compatibility mirrors
// and rebind database adapters to the storage service's imported state.
func (m *Machine) adoptPersistedStorage() error {
	switch {
	case m.ktf != nil:
		return m.ktf.AdoptPersistedDatabases()
	case m.wipi != nil:
		if err := m.wipi.AdoptPersistedFilesystem(); err != nil {
			return err
		}
		return m.wipi.AdoptPersistedDatabases()
	default:
		return nil
	}
}
