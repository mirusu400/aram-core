package application

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader/gnex"
)

const (
	gnex32SaveDataMagic   = "ARAMGNEX32SAVE"
	gnex32SaveDataVersion = 1
	gnex32MaxSaveDataSize = 4 << 10
	gnex32UserNVBytes     = gnex32.UserNVWords * 4
)

type gnex32SaveDataEnvelope struct {
	Magic   string
	Version uint32
	UserNV  [gnex32.UserNVWords]uint32
}

var _ machinecore.SaveDataMachine = (*gnex32Machine)(nil)

// ExportSaveData serializes the 64-byte user non-volatile area that survives a
// GNEX title restart. As with the other application backends, callers snapshot
// only at a lifecycle boundary where guest execution is not running.
func (m *gnex32Machine) ExportSaveData() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, cpu.ErrClosed
	}
	if !gnex32SaveDataStateAllowed(m.state) {
		return nil, fmt.Errorf("export GNEX32 save data from %s: %w", m.state, ErrInvalidState)
	}
	var userNV [gnex32.UserNVWords]uint32
	if m.runtime != nil {
		userNV = m.runtime.CaptureUserNV()
	}
	var output bytes.Buffer
	if err := gob.NewEncoder(&output).Encode(gnex32SaveDataEnvelope{
		Magic:   gnex32SaveDataMagic,
		Version: gnex32SaveDataVersion,
		UserNV:  userNV,
	}); err != nil {
		return nil, fmt.Errorf("encode GNEX32 save data: %w", err)
	}
	return output.Bytes(), nil
}

// ImportSaveData restores user non-volatile storage before execution resumes.
// Empty input is the ordinary first-launch case and leaves an archive-provided
// UserNV image intact.
func (m *gnex32Machine) ImportSaveData(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > gnex32MaxSaveDataSize {
		return fmt.Errorf("GNEX32 save data exceeds byte limit")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if !gnex32SaveDataStateAllowed(m.state) {
		return fmt.Errorf("import GNEX32 save data from %s: %w", m.state, ErrInvalidState)
	}
	var envelope gnex32SaveDataEnvelope
	decoder := gob.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("decode GNEX32 save data: %w", err)
	}
	if envelope.Magic != gnex32SaveDataMagic {
		return fmt.Errorf("GNEX32 save data has unexpected magic %q", envelope.Magic)
	}
	if envelope.Version != gnex32SaveDataVersion {
		return fmt.Errorf("GNEX32 save data has unsupported version %d", envelope.Version)
	}
	if err := decoder.Decode(new(gnex32SaveDataEnvelope)); err != io.EOF {
		return fmt.Errorf("GNEX32 save data has trailing content")
	}
	if m.runtime == nil {
		return fmt.Errorf("import GNEX32 save data without runtime: %w", ErrInvalidState)
	}
	m.runtime.RestoreUserNV(envelope.UserNV)
	return nil
}

func gnex32SaveDataStateAllowed(state machinecore.State) bool {
	switch state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
		return true
	default:
		return false
	}
}

type gnex32ArchiveUserNVCandidate struct {
	name     string
	priority int
	data     []byte
}

// gnex32ArchiveUserNV decodes the raw handset save included with some GNEX
// distributions. UserNV.sys is the most explicit name, followed by user.dat;
// other .dat members qualify only when they have the exact 64-byte GNEX size,
// which excludes observed installer registry records.
func gnex32ArchiveUserNV(pkg gnex.Package) ([gnex32.UserNVWords]uint32, bool, error) {
	var zero [gnex32.UserNVWords]uint32
	names := make([]string, 0, len(pkg.Files))
	for name := range pkg.Files {
		names = append(names, name)
	}
	sort.Strings(names)

	var candidates []gnex32ArchiveUserNVCandidate
	for _, name := range names {
		data := pkg.Files[name]
		base := path.Base(name)
		priority := 0
		switch {
		case strings.EqualFold(base, "UserNV.sys"):
			priority = 3
		case strings.EqualFold(base, "user.dat"):
			priority = 2
		case strings.EqualFold(path.Ext(base), ".dat") && len(data) == gnex32UserNVBytes:
			priority = 1
		}
		if priority == 0 {
			continue
		}
		if len(data) != gnex32UserNVBytes {
			return zero, false, fmt.Errorf("GNEX32 archive UserNV %q has %d bytes, want %d", name, len(data), gnex32UserNVBytes)
		}
		candidates = append(candidates, gnex32ArchiveUserNVCandidate{name: name, priority: priority, data: data})
	}
	if len(candidates) == 0 {
		return zero, false, nil
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.priority > best.priority {
			best = candidate
			continue
		}
		if candidate.priority == best.priority {
			return zero, false, fmt.Errorf("GNEX32 archive has ambiguous UserNV members %q and %q", best.name, candidate.name)
		}
	}
	var userNV [gnex32.UserNVWords]uint32
	for index := range userNV {
		userNV[index] = binary.LittleEndian.Uint32(best.data[index*4:])
	}
	return userNV, true, nil
}
