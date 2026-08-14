package runtime

import (
	"fmt"
	"math"
	"sort"
)

func (s *Storage) CreateRecordStore(owner OwnerID, name string) (ServiceID, error) {
	normalized, err := normalizeRecordName(name, s.limits.MaxPathBytes)
	if err != nil {
		return 0, err
	}
	key := recordStoreKey(owner, normalized)
	if existing := s.recordNames[key]; existing != 0 {
		return 0, fmt.Errorf("%w: record store %q already exists", ErrInvalidArgument, name)
	}
	if uint32(len(s.recordStores)) >= s.limits.MaxRecordStores {
		return 0, fmt.Errorf("%w: record stores reached %d", ErrLimitExceeded, s.limits.MaxRecordStores)
	}
	id, err := s.registry.Create(owner, KindRecordBase)
	if err != nil {
		return 0, err
	}
	s.recordStores[id] = &recordStore{
		id:      id,
		owner:   owner,
		name:    normalized,
		nextID:  1,
		records: make(map[uint32][]byte),
	}
	s.recordNames[key] = id
	return id, nil
}

func (s *Storage) OpenRecordStore(owner OwnerID, name string) (ServiceID, error) {
	normalized, err := normalizeRecordName(name, s.limits.MaxPathBytes)
	if err != nil {
		return 0, err
	}
	id := s.recordNames[recordStoreKey(owner, normalized)]
	if id == 0 {
		return 0, fmt.Errorf("%w: record store %q", ErrNotFound, name)
	}
	if err := s.registry.Validate(id, owner, KindRecordBase); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Storage) DeleteRecordStore(owner OwnerID, id ServiceID) error {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return err
	}
	if err := s.registry.Destroy(id, owner, KindRecordBase); err != nil {
		return err
	}
	delete(s.recordNames, recordStoreKey(owner, store.name))
	delete(s.recordStores, id)
	return nil
}

func (s *Storage) DeleteRecordStoreNamed(owner OwnerID, name string) error {
	id, err := s.OpenRecordStore(owner, name)
	if err != nil {
		return err
	}
	return s.DeleteRecordStore(owner, id)
}

func (s *Storage) RecordCount(owner OwnerID, id ServiceID) (uint32, error) {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return 0, err
	}
	return uint32(len(store.records)), nil
}

func (s *Storage) NextRecordID(owner OwnerID, id ServiceID) (uint32, error) {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return 0, err
	}
	if store.nextID == 0 || store.nextID == math.MaxUint32 {
		return 0, fmt.Errorf("%w: record ID exhausted", ErrLimitExceeded)
	}
	return store.nextID, nil
}

func (s *Storage) AddRecord(owner OwnerID, id ServiceID, data []byte) (uint32, error) {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return 0, err
	}
	if uint32(len(store.records)) >= s.limits.MaxRecords ||
		store.nextID == 0 || store.nextID == math.MaxUint32 {
		return 0, fmt.Errorf("%w: record count or ID exhausted", ErrLimitExceeded)
	}
	if err := s.checkRecordResize(store, 0, uint64(len(data))); err != nil {
		return 0, err
	}
	recordID := store.nextID
	store.nextID++
	store.records[recordID] = cloneBytes(data)
	return recordID, nil
}

func (s *Storage) Record(owner OwnerID, id ServiceID, recordID uint32) ([]byte, error) {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return nil, err
	}
	data, ok := store.records[recordID]
	if !ok {
		return nil, fmt.Errorf("%w: record %d", ErrNotFound, recordID)
	}
	return cloneBytes(data), nil
}

func (s *Storage) SetRecord(owner OwnerID, id ServiceID, recordID uint32, data []byte) error {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return err
	}
	current, ok := store.records[recordID]
	if !ok {
		return fmt.Errorf("%w: record %d", ErrNotFound, recordID)
	}
	if err := s.checkRecordResize(store, uint64(len(current)), uint64(len(data))); err != nil {
		return err
	}
	store.records[recordID] = cloneBytes(data)
	return nil
}

func (s *Storage) DeleteRecord(owner OwnerID, id ServiceID, recordID uint32) error {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return err
	}
	if _, ok := store.records[recordID]; !ok {
		return fmt.Errorf("%w: record %d", ErrNotFound, recordID)
	}
	delete(store.records, recordID)
	return nil
}

func (s *Storage) RecordIDs(owner OwnerID, id ServiceID) ([]uint32, error) {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return nil, err
	}
	ids := make([]uint32, 0, len(store.records))
	for recordID := range store.records {
		ids = append(ids, recordID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// ReplaceRecords atomically imports an adapter's record view. Record ID zero
// is accepted because some WIPI database ABIs use it even though Java RMS
// allocates IDs starting at one.
func (s *Storage) ReplaceRecords(
	owner OwnerID,
	id ServiceID,
	nextID uint32,
	records map[uint32][]byte,
) error {
	store, err := s.recordStore(owner, id)
	if err != nil {
		return err
	}
	if nextID == 0 || nextID == math.MaxUint32 ||
		len(records) > int(s.limits.MaxRecords) {
		return fmt.Errorf("%w: invalid record import", ErrInvalidArgument)
	}
	candidate := make(map[uint32][]byte, len(records))
	var total uint64
	for recordID, data := range records {
		if recordID >= nextID {
			return fmt.Errorf(
				"%w: record %d is not below next ID %d",
				ErrInvalidArgument,
				recordID,
				nextID,
			)
		}
		total += uint64(len(data))
		if total > s.limits.MaxRecordBytes {
			return fmt.Errorf("%w: record store byte quota", ErrLimitExceeded)
		}
		candidate[recordID] = cloneBytes(data)
	}
	store.nextID = nextID
	store.records = candidate
	return nil
}
