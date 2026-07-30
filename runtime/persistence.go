package runtime

import (
	"fmt"
	"path"
	"sort"
	"time"
)

const StoragePersistenceSchemaVersion = uint32(1)

// PersistentRecordStoreState omits process-local service IDs. Import assigns
// fresh deterministic IDs and adapters reopen stores by owner and name.
type PersistentRecordStoreState struct {
	Owner   OwnerID
	Name    string
	NextID  uint32
	Records []RecordState
}

// StoragePersistenceState contains only restart-persistent namespaces and
// record data. Package resources, temporary files, and open handles are not
// persistence.
type StoragePersistenceState struct {
	Schema       uint32
	Directories  []DirectoryState
	Files        []FileState
	RecordStores []PersistentRecordStoreState
}

func (s *Storage) ExportPersistence() StoragePersistenceState {
	state := StoragePersistenceState{Schema: StoragePersistenceSchemaVersion}
	snapshot := s.Snapshot()
	for _, directory := range snapshot.Directories {
		if persistentNamespace(directory.Namespace) {
			state.Directories = append(state.Directories, directory)
		}
	}
	for _, file := range snapshot.Files {
		if persistentNamespace(file.Namespace) {
			file.Data = cloneBytes(file.Data)
			state.Files = append(state.Files, file)
		}
	}
	for _, store := range snapshot.RecordStores {
		persistent := PersistentRecordStoreState{
			Owner:  store.Owner,
			Name:   store.Name,
			NextID: store.NextID,
		}
		for _, record := range store.Records {
			persistent.Records = append(persistent.Records, RecordState{
				ID: record.ID, Data: cloneBytes(record.Data),
			})
		}
		state.RecordStores = append(state.RecordStores, persistent)
	}
	sort.Slice(state.RecordStores, func(i, j int) bool {
		if state.RecordStores[i].Owner != state.RecordStores[j].Owner {
			return state.RecordStores[i].Owner < state.RecordStores[j].Owner
		}
		return state.RecordStores[i].Name < state.RecordStores[j].Name
	})
	return state
}

// ImportPersistence atomically replaces private/shared files and record
// stores. It is intended for startup before persistent handles are exposed;
// package and temporary namespaces remain untouched.
func (s *Storage) ImportPersistence(state StoragePersistenceState) error {
	if s == nil || s.registry == nil || s.clock == nil {
		return fmt.Errorf("%w: storage is not initialized", ErrInvalidArgument)
	}
	if state.Schema != StoragePersistenceSchemaVersion ||
		len(state.Directories) > int(s.limits.MaxFiles) ||
		len(state.Files) > int(s.limits.MaxFiles) ||
		uint64(len(state.Directories))+uint64(len(state.Files)) >
			uint64(s.limits.MaxFiles) ||
		len(state.RecordStores) > int(s.limits.MaxRecordStores) {
		return fmt.Errorf("%w: invalid persistence limits", ErrInvalidState)
	}
	for _, handle := range s.openFiles {
		if persistentNamespace(handle.namespace) {
			return fmt.Errorf("%w: persistent file is open", ErrInvalidState)
		}
	}

	files := make(map[string]*storageFile)
	directories := make(map[string]*storageDirectory)
	var used uint64
	for key, current := range s.files {
		if persistentNamespace(current.namespace) {
			continue
		}
		copyFile := *current
		copyFile.data = cloneBytes(current.data)
		if uint64(len(copyFile.data)) > s.limits.MaxStorageBytes-used {
			return fmt.Errorf("%w: live storage exceeds byte limit", ErrInvalidState)
		}
		files[key] = &copyFile
		used += uint64(len(copyFile.data))
	}
	for key, current := range s.directories {
		if persistentNamespace(current.namespace) {
			continue
		}
		copyDirectory := *current
		directories[key] = &copyDirectory
	}

	candidate := *s
	candidate.files = files
	candidate.directories = directories
	candidate.recordStores = make(map[ServiceID]*recordStore)
	candidate.recordNames = make(map[string]ServiceID)

	previousDirectoryKey := ""
	for index, saved := range state.Directories {
		normalized, err := candidate.normalizeDirectory(saved.Path)
		key := storageKey(saved.Namespace, normalized)
		if !persistentNamespace(saved.Namespace) || err != nil ||
			normalized == "/" || normalized != saved.Path ||
			saved.ReadOnly || saved.Modified < 0 ||
			(index != 0 && key <= previousDirectoryKey) ||
			candidate.files[key] != nil || candidate.directories[key] != nil {
			return fmt.Errorf("%w: invalid persistent directory %d", ErrInvalidState, index)
		}
		modified := time.Duration(saved.Modified)
		if modified > s.clock.Monotonic() {
			modified = s.clock.Monotonic()
		}
		candidate.directories[key] = &storageDirectory{
			namespace: saved.Namespace,
			path:      saved.Path,
			modified:  modified,
		}
		previousDirectoryKey = key
	}

	previousFileKey := ""
	for index, saved := range state.Files {
		normalized, err := candidate.validPath(saved.Namespace, saved.Path)
		key := storageKey(saved.Namespace, normalized)
		if !persistentNamespace(saved.Namespace) || err != nil ||
			normalized != saved.Path || saved.ReadOnly ||
			saved.Modified < 0 ||
			(index != 0 && key <= previousFileKey) ||
			candidate.files[key] != nil || candidate.directories[key] != nil ||
			uint64(len(saved.Data)) > s.limits.MaxFileBytes ||
			uint64(len(saved.Data)) > s.limits.MaxStorageBytes ||
			used > s.limits.MaxStorageBytes-uint64(len(saved.Data)) {
			return fmt.Errorf("%w: invalid persistent file %d", ErrInvalidState, index)
		}
		used += uint64(len(saved.Data))
		modified := time.Duration(saved.Modified)
		if modified > s.clock.Monotonic() {
			modified = s.clock.Monotonic()
		}
		candidate.files[key] = &storageFile{
			namespace: saved.Namespace,
			path:      saved.Path,
			data:      cloneBytes(saved.Data),
			modified:  modified,
		}
		previousFileKey = key
	}
	if uint64(len(candidate.files))+uint64(len(candidate.directories)) >
		uint64(s.limits.MaxFiles) {
		return fmt.Errorf("%w: persistent file count exceeds limit", ErrInvalidState)
	}
	for _, directory := range candidate.directories {
		parent := path.Dir(directory.path)
		if parent != "/" &&
			candidate.directories[storageKey(directory.namespace, parent)] == nil {
			return fmt.Errorf("%w: persistent directory has no parent", ErrInvalidState)
		}
	}
	for _, file := range candidate.files {
		parent := path.Dir(file.path)
		if parent != "/" &&
			candidate.directories[storageKey(file.namespace, parent)] == nil {
			return fmt.Errorf("%w: persistent file has no parent", ErrInvalidState)
		}
	}

	registryState := s.registry.Snapshot()
	candidateRegistry := NewRegistry(registryState.Limit)
	if err := candidateRegistry.Restore(registryState); err != nil {
		return fmt.Errorf("%w: invalid live registry: %v", ErrInvalidState, err)
	}
	for _, store := range s.Snapshot().RecordStores {
		if err := candidateRegistry.Destroy(
			store.ID,
			store.Owner,
			KindRecordBase,
		); err != nil {
			return fmt.Errorf("%w: invalid live record store: %v", ErrInvalidState, err)
		}
	}
	previousOwner := OwnerID(0)
	previousName := ""
	for index, saved := range state.RecordStores {
		name, err := normalizeRecordName(saved.Name, s.limits.MaxPathBytes)
		if err != nil || name != saved.Name || saved.NextID == 0 ||
			len(saved.Records) > int(s.limits.MaxRecords) ||
			(index != 0 &&
				(saved.Owner < previousOwner ||
					saved.Owner == previousOwner && name <= previousName)) {
			return fmt.Errorf("%w: invalid persistent record store %d", ErrInvalidState, index)
		}
		id, err := candidateRegistry.Create(saved.Owner, KindRecordBase)
		if err != nil {
			return err
		}
		store := &recordStore{
			id: id, owner: saved.Owner, name: name,
			nextID: saved.NextID, records: make(map[uint32][]byte),
		}
		var previousRecord uint32
		var recordBytes uint64
		for recordIndex, record := range saved.Records {
			if record.ID >= saved.NextID ||
				(recordIndex != 0 && record.ID <= previousRecord) ||
				uint64(len(record.Data)) > s.limits.MaxRecordBytes ||
				uint64(len(record.Data)) > s.limits.MaxRecordBytes-recordBytes {
				return fmt.Errorf(
					"%w: invalid persistent record %d in store %d",
					ErrInvalidState,
					recordIndex,
					index,
				)
			}
			recordBytes += uint64(len(record.Data))
			store.records[record.ID] = cloneBytes(record.Data)
			previousRecord = record.ID
		}
		key := recordStoreKey(saved.Owner, name)
		if candidate.recordNames[key] != 0 {
			return fmt.Errorf("%w: duplicate persistent record store", ErrInvalidState)
		}
		candidate.recordStores[id] = store
		candidate.recordNames[key] = id
		previousOwner, previousName = saved.Owner, name
	}

	if err := s.registry.Restore(candidateRegistry.Snapshot()); err != nil {
		return fmt.Errorf("%w: commit persistent registry: %v", ErrInvalidState, err)
	}
	s.files = candidate.files
	s.directories = candidate.directories
	s.recordStores = candidate.recordStores
	s.recordNames = candidate.recordNames
	return nil
}

func persistentNamespace(namespace Namespace) bool {
	return namespace == NamespacePrivate || namespace == NamespaceShared
}
