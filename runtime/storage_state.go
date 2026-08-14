package runtime

import (
	"fmt"
	"path"
	"sort"
	"time"
)

func (s *Storage) Snapshot() StorageState {
	state := StorageState{Limits: s.limits}
	directoryKeys := make([]string, 0, len(s.directories))
	for key := range s.directories {
		directoryKeys = append(directoryKeys, key)
	}
	sort.Strings(directoryKeys)
	for _, key := range directoryKeys {
		current := s.directories[key]
		state.Directories = append(state.Directories, DirectoryState{
			Namespace: current.namespace,
			Path:      current.path,
			Modified:  int64(current.modified),
			ReadOnly:  current.readOnly,
		})
	}
	keys := make([]string, 0, len(s.files))
	for key := range s.files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		current := s.files[key]
		state.Files = append(state.Files, FileState{
			Namespace: current.namespace,
			Path:      current.path,
			Data:      cloneBytes(current.data),
			Modified:  int64(current.modified),
			ReadOnly:  current.readOnly,
		})
	}
	handleIDs := make([]ServiceID, 0, len(s.openFiles))
	for id := range s.openFiles {
		handleIDs = append(handleIDs, id)
	}
	sort.Slice(handleIDs, func(i, j int) bool { return handleIDs[i] < handleIDs[j] })
	for _, id := range handleIDs {
		handle := s.openFiles[id]
		state.OpenFiles = append(state.OpenFiles, OpenFileState{
			ID:        handle.id,
			Owner:     handle.owner,
			Namespace: handle.namespace,
			Path:      handle.path,
			Position:  handle.position,
			Mode:      handle.mode,
		})
	}
	storeIDs := make([]ServiceID, 0, len(s.recordStores))
	for id := range s.recordStores {
		storeIDs = append(storeIDs, id)
	}
	sort.Slice(storeIDs, func(i, j int) bool { return storeIDs[i] < storeIDs[j] })
	for _, id := range storeIDs {
		store := s.recordStores[id]
		saved := RecordStoreState{
			ID:     store.id,
			Owner:  store.owner,
			Name:   store.name,
			NextID: store.nextID,
		}
		recordIDs := make([]uint32, 0, len(store.records))
		for recordID := range store.records {
			recordIDs = append(recordIDs, recordID)
		}
		sort.Slice(recordIDs, func(i, j int) bool { return recordIDs[i] < recordIDs[j] })
		for _, recordID := range recordIDs {
			saved.Records = append(saved.Records, RecordState{
				ID:   recordID,
				Data: cloneBytes(store.records[recordID]),
			})
		}
		state.RecordStores = append(state.RecordStores, saved)
	}
	return state
}

func (s *Storage) Restore(state StorageState) error {
	if err := state.Limits.Validate(); err != nil ||
		uint64(len(state.Directories)) > uint64(state.Limits.MaxFiles) ||
		uint64(len(state.Files)) > uint64(state.Limits.MaxFiles) ||
		uint64(len(state.Directories))+uint64(len(state.Files)) >
			uint64(state.Limits.MaxFiles) ||
		uint64(len(state.OpenFiles)) > uint64(state.Limits.MaxOpenHandles) ||
		uint64(len(state.RecordStores)) > uint64(state.Limits.MaxRecordStores) {
		return fmt.Errorf("%w: invalid storage state limits", ErrInvalidState)
	}
	candidate, err := NewStorage(s.registry, s.clock, state.Limits)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidState, err)
	}
	previousDirectoryKey := ""
	for index, saved := range state.Directories {
		normalized, normalizeErr := candidate.normalizeDirectory(saved.Path)
		key := storageKey(saved.Namespace, normalized)
		if !saved.Namespace.Valid() ||
			normalizeErr != nil || normalized == "/" || normalized != saved.Path ||
			(index != 0 && key <= previousDirectoryKey) ||
			saved.Modified < 0 ||
			saved.Modified > int64(s.clock.Monotonic()) ||
			saved.ReadOnly != (saved.Namespace == NamespacePackage) {
			return fmt.Errorf("%w: invalid directory state %d", ErrInvalidState, index)
		}
		candidate.directories[key] = &storageDirectory{
			namespace: saved.Namespace,
			path:      normalized,
			modified:  time.Duration(saved.Modified),
			readOnly:  saved.ReadOnly,
		}
		previousDirectoryKey = key
	}
	previousFileKey := ""
	var storageBytes uint64
	for index, saved := range state.Files {
		normalized, normalizeErr := candidate.validPath(saved.Namespace, saved.Path)
		key := storageKey(saved.Namespace, normalized)
		if normalizeErr != nil || normalized != saved.Path ||
			(index != 0 && key <= previousFileKey) ||
			saved.Modified < 0 ||
			saved.Modified > int64(s.clock.Monotonic()) ||
			candidate.directories[key] != nil ||
			uint64(len(saved.Data)) > state.Limits.MaxFileBytes ||
			saved.ReadOnly != (saved.Namespace == NamespacePackage) {
			return fmt.Errorf("%w: invalid file state %d", ErrInvalidState, index)
		}
		dataSize := uint64(len(saved.Data))
		if dataSize > state.Limits.MaxStorageBytes ||
			storageBytes > state.Limits.MaxStorageBytes-dataSize {
			return fmt.Errorf("%w: saved files exceed storage quota", ErrInvalidState)
		}
		storageBytes += dataSize
		candidate.files[key] = &storageFile{
			namespace: saved.Namespace,
			path:      saved.Path,
			data:      cloneBytes(saved.Data),
			modified:  time.Duration(saved.Modified),
			readOnly:  saved.ReadOnly,
		}
		previousFileKey = key
	}
	for index, directory := range candidate.directories {
		parent := path.Dir(directory.path)
		if parent != "/" &&
			candidate.directories[storageKey(directory.namespace, parent)] == nil {
			return fmt.Errorf("%w: directory %q has no parent", ErrInvalidState, index)
		}
	}
	for index, current := range candidate.files {
		parent := path.Dir(current.path)
		if parent != "/" &&
			candidate.directories[storageKey(current.namespace, parent)] == nil {
			return fmt.Errorf("%w: file %q has no directory", ErrInvalidState, index)
		}
	}
	var previousID ServiceID
	for index, saved := range state.OpenFiles {
		key := storageKey(saved.Namespace, saved.Path)
		current := candidate.files[key]
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previousID) ||
			current == nil || !saved.Mode.Valid() ||
			saved.Position > state.Limits.MaxFileBytes ||
			(current.readOnly && saved.Mode&OpenWrite != 0) ||
			s.registry.Validate(saved.ID, saved.Owner, KindFile) != nil {
			return fmt.Errorf("%w: invalid open file state %d", ErrInvalidState, index)
		}
		candidate.openFiles[saved.ID] = &openFile{
			id:        saved.ID,
			owner:     saved.Owner,
			namespace: saved.Namespace,
			path:      saved.Path,
			position:  saved.Position,
			mode:      saved.Mode,
		}
		previousID = saved.ID
	}
	previousID = 0
	for index, saved := range state.RecordStores {
		name, nameErr := normalizeRecordName(saved.Name, state.Limits.MaxPathBytes)
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previousID) ||
			nameErr != nil || name != saved.Name || saved.NextID == 0 ||
			uint64(len(saved.Records)) > uint64(state.Limits.MaxRecords) ||
			s.registry.Validate(saved.ID, saved.Owner, KindRecordBase) != nil {
			return fmt.Errorf("%w: invalid record store state %d", ErrInvalidState, index)
		}
		key := recordStoreKey(saved.Owner, name)
		if candidate.recordNames[key] != 0 {
			return fmt.Errorf("%w: duplicate record store name", ErrInvalidState)
		}
		store := &recordStore{
			id:      saved.ID,
			owner:   saved.Owner,
			name:    name,
			nextID:  saved.NextID,
			records: make(map[uint32][]byte, len(saved.Records)),
		}
		var previousRecordID uint32
		var recordBytes uint64
		for recordIndex, record := range saved.Records {
			if record.ID >= saved.NextID ||
				(recordIndex != 0 && record.ID <= previousRecordID) {
				return fmt.Errorf(
					"%w: invalid record %d in store %d",
					ErrInvalidState,
					recordIndex,
					index,
				)
			}
			dataSize := uint64(len(record.Data))
			if dataSize > state.Limits.MaxRecordBytes ||
				recordBytes > state.Limits.MaxRecordBytes-dataSize {
				return fmt.Errorf("%w: record store exceeds byte quota", ErrInvalidState)
			}
			recordBytes += dataSize
			store.records[record.ID] = cloneBytes(record.Data)
			previousRecordID = record.ID
		}
		candidate.recordStores[saved.ID] = store
		candidate.recordNames[key] = saved.ID
		previousID = saved.ID
	}
	*s = *candidate
	return nil
}

func cloneStorageFiles(
	source map[string]*storageFile,
) map[string]*storageFile {
	result := make(map[string]*storageFile, len(source))
	for key, current := range source {
		result[key] = current
	}
	return result
}

func cloneStorageDirectories(
	source map[string]*storageDirectory,
) map[string]*storageDirectory {
	result := make(map[string]*storageDirectory, len(source))
	for key, current := range source {
		result[key] = current
	}
	return result
}
