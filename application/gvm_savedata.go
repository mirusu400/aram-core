package application

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

const (
	gvmSaveDataMagic   = "ARAMGVMSAVE"
	gvmSaveDataVersion = 1
	gvmMaxSaveDataSize = 128 << 10
)

type gvmSaveDataEnvelope struct {
	Magic   string
	Version uint32
	Data    []byte
}

var _ machinecore.SaveDataMachine = (*gvmMachine)(nil)

// ExportSaveData serializes the fixed persistent byte record used by the GVM
// data-read/data-write services. Volatile VM, display, and timer state are not
// included.
func (m *gvmMachine) ExportSaveData() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, cpu.ErrClosed
	}
	if !gvmSaveDataStateAllowed(m.state) {
		return nil, fmt.Errorf("export GVM save data from %s: %w", m.state, ErrInvalidState)
	}
	var output bytes.Buffer
	if err := gob.NewEncoder(&output).Encode(gvmSaveDataEnvelope{
		Magic: gvmSaveDataMagic, Version: gvmSaveDataVersion,
		Data: m.persistent.snapshot(),
	}); err != nil {
		return nil, fmt.Errorf("encode GVM save data: %w", err)
	}
	return output.Bytes(), nil
}

// ImportSaveData atomically restores the persistent record before execution.
// Empty input is the ordinary first-launch case.
func (m *gvmMachine) ImportSaveData(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > gvmMaxSaveDataSize {
		return fmt.Errorf("GVM save data exceeds byte limit")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if !gvmSaveDataStateAllowed(m.state) {
		return fmt.Errorf("import GVM save data from %s: %w", m.state, ErrInvalidState)
	}
	var envelope gvmSaveDataEnvelope
	decoder := gob.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("decode GVM save data: %w", err)
	}
	if envelope.Magic != gvmSaveDataMagic {
		return fmt.Errorf("GVM save data has unexpected magic %q", envelope.Magic)
	}
	if envelope.Version != gvmSaveDataVersion {
		return fmt.Errorf("GVM save data has unsupported version %d", envelope.Version)
	}
	if err := decoder.Decode(new(gvmSaveDataEnvelope)); err != io.EOF {
		return fmt.Errorf("GVM save data has trailing content")
	}
	if len(envelope.Data) > maxGVMPersistentDataBytes {
		return fmt.Errorf("GVM save data payload exceeds byte limit")
	}
	if m.persistent == nil {
		m.persistent = new(gvmPersistentStore)
	}
	return m.persistent.restore(envelope.Data)
}

func gvmSaveDataStateAllowed(state machinecore.State) bool {
	switch state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
		return true
	default:
		return false
	}
}
