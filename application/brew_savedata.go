package application

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"

	"github.com/mirusu400/aram-core/application/internal/brewrt"
	"github.com/mirusu400/aram-core/cpu"
)

const brewSaveDataMagic = "ARAMBREWSAVE"

type brewSaveDataEnvelope struct {
	Magic   string
	Storage brewrt.PersistentState
}

// ExportSaveData carries BREW files, databases and IShell preferences through
// the host's existing per-title save-data and backup paths. The machine mutex
// serializes the snapshot with guest execution, including a running session.
func (m *brewMachine) ExportSaveData() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, cpu.ErrClosed
	}
	state := brewrt.PersistentState{Schema: brewrt.PersistentStateSchema}
	if m.runtime != nil {
		state = m.runtime.CapturePersistentState(m.pkg.Files)
	} else if m.persistence != nil {
		state = *m.persistence
	}
	if state.Empty() && !m.hasSaveData {
		return nil, nil
	}
	if err := state.Validate(m.pkg.Files); err != nil {
		return nil, fmt.Errorf("export BREW save data: %w", err)
	}
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(brewSaveDataEnvelope{Magic: brewSaveDataMagic, Storage: state}); err != nil {
		return nil, fmt.Errorf("encode BREW save data: %w", err)
	}
	if buffer.Len() > brewrt.MaxPersistentBytes {
		return nil, fmt.Errorf("BREW save data exceeds byte limit")
	}
	// An empty snapshot after deleting the last save must overwrite an older
	// nonempty save instead of resurrecting it on the next launch.
	m.hasSaveData = true
	return buffer.Bytes(), nil
}

// ImportSaveData restores flash before the guest's first read. Empty input is
// the ordinary first-launch case. Runtime validation also protects paused
// callers from replacing storage that still has open handles.
func (m *brewMachine) ImportSaveData(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if len(data) > brewrt.MaxPersistentBytes {
		return fmt.Errorf("BREW save data exceeds byte limit")
	}
	var envelope brewSaveDataEnvelope
	decoder := gob.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("decode BREW save data: %w", err)
	}
	if envelope.Magic != brewSaveDataMagic {
		return fmt.Errorf("unexpected BREW save data magic")
	}
	if err := decoder.Decode(new(brewSaveDataEnvelope)); err != io.EOF {
		return fmt.Errorf("BREW save data has trailing content")
	}
	runtime := m.runtime
	if runtime == nil {
		var err error
		runtime, err = brewrt.New(m.pkg)
		if err != nil {
			return err
		}
	}
	if err := runtime.RestorePersistentState(envelope.Storage, m.pkg.Files); err != nil {
		if m.runtime == nil {
			_ = runtime.Close()
		}
		return fmt.Errorf("import BREW save data: %w", err)
	}
	if m.runtime == nil {
		m.runtime = runtime
		m.mediaOutputRevision = runtime.AudioOutputRevision()
	}
	m.persistence = nil
	m.hasSaveData = true
	return nil
}
