package brewrt

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

const (
	PersistentStateSchema = uint32(1)
	MaxPersistentBytes    = 64 << 20
	maxPersistentEntries  = 4096
)

// PersistentState contains handset flash content, without CPU state, handles,
// or unchanged package resources. File removals are relative to the package.
type PersistentState struct {
	Schema       uint32
	Files        []PersistentFile
	RemovedFiles []string
	Directories  []string
	Databases    []PersistentDatabase
	Preferences  []PersistentPreference
}

type PersistentFile struct {
	Path string
	Data []byte
}

type PersistentDatabase struct {
	Path    string
	NextID  uint16
	Records []PersistentRecord
}

type PersistentRecord struct {
	ID     uint16
	Fields []PersistentField
}

type PersistentField struct {
	Type byte
	Name byte
	Data []byte
}

type PersistentPreference struct {
	ClassID uint32
	Version uint16
	Data    []byte
}

func (s PersistentState) Empty() bool {
	return len(s.Files)+len(s.RemovedFiles)+len(s.Directories)+len(s.Databases)+len(s.Preferences) == 0
}

func (r *Runtime) CapturePersistentState(packageFiles map[string][]byte) PersistentState {
	state := PersistentState{Schema: PersistentStateSchema}
	for path, data := range r.files {
		if original, packaged := packageFiles[path]; packaged && bytes.Equal(data, original) {
			continue
		}
		state.Files = append(state.Files, PersistentFile{Path: path, Data: bytes.Clone(data)})
	}
	for path := range packageFiles {
		if _, exists := r.files[path]; !exists {
			state.RemovedFiles = append(state.RemovedFiles, path)
		}
	}
	for path := range r.directories {
		state.Directories = append(state.Directories, path)
	}
	for path, database := range r.databases {
		saved := PersistentDatabase{Path: path, NextID: database.nextID}
		for _, record := range database.records {
			entry := PersistentRecord{ID: record.id}
			for _, field := range record.fields {
				entry.Fields = append(entry.Fields, PersistentField{
					Type: field.fieldType, Name: field.name, Data: bytes.Clone(field.data),
				})
			}
			saved.Records = append(saved.Records, entry)
		}
		state.Databases = append(state.Databases, saved)
	}
	for key, data := range r.preferences {
		state.Preferences = append(state.Preferences, PersistentPreference{
			ClassID: key.classID, Version: key.version, Data: bytes.Clone(data),
		})
	}
	sort.Slice(state.Files, func(i, j int) bool { return state.Files[i].Path < state.Files[j].Path })
	sort.Strings(state.RemovedFiles)
	sort.Strings(state.Directories)
	sort.Slice(state.Databases, func(i, j int) bool { return state.Databases[i].Path < state.Databases[j].Path })
	sort.Slice(state.Preferences, func(i, j int) bool {
		a, b := state.Preferences[i], state.Preferences[j]
		return a.ClassID < b.ClassID || a.ClassID == b.ClassID && a.Version < b.Version
	})
	return state
}

// Validate also runs before export or reset so a snapshot that exceeds the
// restore limits cannot replace a usable host save or destroy live storage.
func (state PersistentState) Validate(packageFiles map[string][]byte) error {
	if state.Schema != PersistentStateSchema ||
		len(state.Files)+len(state.RemovedFiles)+len(state.Directories)+len(state.Databases)+len(state.Preferences) > maxPersistentEntries {
		return fmt.Errorf("invalid BREW persistence schema or entry count")
	}
	validPath := func(path string) bool {
		return path != "" && len(path) < 4096 && !strings.ContainsRune(path, 0) && normalizeGuestPath(path) == path
	}
	var used uint64
	addBytes := func(size int) bool {
		used += uint64(size)
		return used <= MaxPersistentBytes
	}
	seenFiles := make(map[string]bool)
	for _, saved := range state.Files {
		if !validPath(saved.Path) || seenFiles[saved.Path] || len(saved.Data) > int(heapSize) || !addBytes(len(saved.Data)) {
			return fmt.Errorf("invalid BREW persistent file")
		}
		seenFiles[saved.Path] = true
	}
	for _, path := range state.RemovedFiles {
		if _, packaged := packageFiles[path]; !packaged || seenFiles[path] {
			return fmt.Errorf("invalid BREW removed package file")
		}
		seenFiles[path] = true
	}
	directories := make(map[string]bool, len(state.Directories))
	for _, path := range state.Directories {
		if !validPath(path) || directories[path] || strings.HasSuffix(path, "/") {
			return fmt.Errorf("invalid BREW persistent directory")
		}
		directories[path] = true
	}
	databases := make(map[string]bool, len(state.Databases))
	for _, saved := range state.Databases {
		if !validPath(saved.Path) || databases[saved.Path] || saved.NextID == 0 || len(saved.Records) > maxDBRecords {
			return fmt.Errorf("invalid BREW persistent database")
		}
		databases[saved.Path] = true
		var databaseBytes uint32
		ids := make(map[uint16]bool, len(saved.Records))
		for _, entry := range saved.Records {
			if entry.ID == 0 || entry.ID >= saved.NextID || ids[entry.ID] || len(entry.Fields) > 64 {
				return fmt.Errorf("invalid BREW persistent record")
			}
			ids[entry.ID] = true
			for _, field := range entry.Fields {
				if len(field.Data) > 4096 || !addBytes(len(field.Data)) {
					return fmt.Errorf("invalid BREW persistent field")
				}
				databaseBytes += uint32(len(field.Data))
				if databaseBytes > maxDBBytes {
					return fmt.Errorf("BREW persistent database exceeds byte limit")
				}
			}
		}
	}
	preferences := make(map[brewPreferenceKey]bool, len(state.Preferences))
	for _, saved := range state.Preferences {
		key := brewPreferenceKey{classID: saved.ClassID, version: saved.Version}
		if preferences[key] || len(saved.Data) == 0 || len(saved.Data) > 64<<10 || !addBytes(len(saved.Data)) {
			return fmt.Errorf("invalid BREW persistent preferences")
		}
		preferences[key] = true
	}
	return nil
}

// RestorePersistentState validates the entire candidate before replacing any
// live storage. Import before opening files or databases, as their handles
// belong to the old storage and cannot be carried across a restart.
func (r *Runtime) RestorePersistentState(state PersistentState, packageFiles map[string][]byte) error {
	if len(r.fileHandles)+len(r.databaseHandles)+len(r.dbRecordHandles) != 0 {
		return fmt.Errorf("cannot restore BREW storage with open files or databases")
	}
	if err := state.Validate(packageFiles); err != nil {
		return err
	}
	files := make(map[string][]byte, len(packageFiles)+len(state.Files))
	for path, data := range packageFiles {
		files[path] = bytes.Clone(data)
	}
	for _, saved := range state.Files {
		files[saved.Path] = bytes.Clone(saved.Data)
	}
	for _, path := range state.RemovedFiles {
		delete(files, path)
	}
	directories := make(map[string]struct{}, len(state.Directories))
	for _, path := range state.Directories {
		directories[path] = struct{}{}
	}
	databases := make(map[string]*brewDatabase, len(state.Databases))
	for _, saved := range state.Databases {
		database := &brewDatabase{path: saved.Path, nextID: saved.NextID}
		for _, entry := range saved.Records {
			record := &brewDBRecord{id: entry.ID}
			for _, field := range entry.Fields {
				database.usedBytes += uint32(len(field.Data))
				record.fields = append(record.fields, brewDBField{
					fieldType: field.Type, name: field.Name, data: bytes.Clone(field.Data),
				})
			}
			database.records = append(database.records, record)
		}
		databases[saved.Path] = database
	}
	preferences := make(map[brewPreferenceKey][]byte, len(state.Preferences))
	for _, saved := range state.Preferences {
		preferences[brewPreferenceKey{classID: saved.ClassID, version: saved.Version}] = bytes.Clone(saved.Data)
	}
	r.files, r.directories, r.databases, r.preferences = files, directories, databases, preferences
	return nil
}
