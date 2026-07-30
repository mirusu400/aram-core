package runtime

import (
	"fmt"
	"sort"
)

type registryEntry struct {
	id    ServiceID
	kind  ObjectKind
	owner OwnerID
	refs  uint32
}

// RegistryState is a deterministic, representation-independent snapshot of a
// service registry.
type RegistryState struct {
	Limit       uint32
	NextSlot    uint32
	Generations []RegistryGenerationState
	Entries     []RegistryEntryState
}

type RegistryGenerationState struct {
	Slot       uint32
	Generation uint32
}

type RegistryEntryState struct {
	ID    ServiceID
	Kind  ObjectKind
	Owner OwnerID
	Refs  uint32
}

// Registry allocates deterministic generation-tagged IDs and validates
// ownership, kind, retain/release, and stale references.
type Registry struct {
	limit       uint32
	nextSlot    uint32
	generations map[uint32]uint32
	entries     map[uint32]registryEntry
	free        []uint32
}

func NewRegistry(limit uint32) *Registry {
	if limit == 0 {
		limit = DefaultMaxObjects
	}
	return &Registry{
		limit:       limit,
		nextSlot:    1,
		generations: make(map[uint32]uint32),
		entries:     make(map[uint32]registryEntry),
	}
}

func (r *Registry) Limit() uint32 {
	return r.limit
}

func (r *Registry) Len() int {
	return len(r.entries)
}

func (r *Registry) Create(owner OwnerID, kind ObjectKind) (ServiceID, error) {
	if err := kind.Validate(); err != nil {
		return 0, err
	}
	if uint32(len(r.entries)) >= r.limit {
		return 0, fmt.Errorf("%w: service object count reached %d", ErrLimitExceeded, r.limit)
	}

	var slot uint32
	freeIndex := -1
	for index, candidate := range r.free {
		if r.generations[candidate] != ^uint32(0) {
			slot = candidate
			freeIndex = index
			break
		}
	}
	if slot == 0 {
		slot = r.nextSlot
		if slot == 0 || slot == ^uint32(0) ||
			uint64(slot) > uint64(r.limit) {
			return 0, fmt.Errorf("%w: service ID slots exhausted", ErrLimitExceeded)
		}
	}
	generation := r.generations[slot] + 1
	if generation == 0 {
		return 0, fmt.Errorf("%w: service ID generation exhausted for slot %d", ErrLimitExceeded, slot)
	}
	if freeIndex >= 0 {
		copy(r.free[freeIndex:], r.free[freeIndex+1:])
		r.free = r.free[:len(r.free)-1]
	} else {
		r.nextSlot++
	}
	r.generations[slot] = generation
	id := makeServiceID(slot, generation)
	r.entries[slot] = registryEntry{
		id:    id,
		kind:  kind,
		owner: owner,
		refs:  1,
	}
	return id, nil
}

func (r *Registry) Validate(id ServiceID, owner OwnerID, kind ObjectKind) error {
	entry, err := r.lookup(id)
	if err != nil {
		return err
	}
	if kind != "" && entry.kind != kind {
		return fmt.Errorf("%w: ID %s is %q, want %q", ErrWrongKind, id, entry.kind, kind)
	}
	if entry.owner != owner {
		return fmt.Errorf("%w: ID %s belongs to owner %d, want %d", ErrWrongOwner, id, entry.owner, owner)
	}
	return nil
}

func (r *Registry) Kind(id ServiceID) (ObjectKind, error) {
	entry, err := r.lookup(id)
	if err != nil {
		return "", err
	}
	return entry.kind, nil
}

func (r *Registry) Owner(id ServiceID) (OwnerID, error) {
	entry, err := r.lookup(id)
	if err != nil {
		return 0, err
	}
	return entry.owner, nil
}

func (r *Registry) Transfer(id ServiceID, from, to OwnerID) error {
	entry, err := r.lookup(id)
	if err != nil {
		return err
	}
	if entry.owner != from {
		return fmt.Errorf("%w: ID %s belongs to owner %d, want %d", ErrWrongOwner, id, entry.owner, from)
	}
	entry.owner = to
	r.entries[id.Slot()] = entry
	return nil
}

func (r *Registry) Retain(id ServiceID) error {
	entry, err := r.lookup(id)
	if err != nil {
		return err
	}
	if entry.refs == ^uint32(0) {
		return fmt.Errorf("%w: retain count overflow for ID %s", ErrLimitExceeded, id)
	}
	entry.refs++
	r.entries[id.Slot()] = entry
	return nil
}

// Release decrements the retain count and reports whether the service object
// must now be destroyed by its owning service.
func (r *Registry) Release(id ServiceID) (bool, error) {
	entry, err := r.lookup(id)
	if err != nil {
		return false, err
	}
	entry.refs--
	if entry.refs != 0 {
		r.entries[id.Slot()] = entry
		return false, nil
	}
	delete(r.entries, id.Slot())
	r.insertFree(id.Slot())
	return true, nil
}

// Destroy releases every retain owned by the specified owner.
func (r *Registry) Destroy(id ServiceID, owner OwnerID, kind ObjectKind) error {
	if err := r.Validate(id, owner, kind); err != nil {
		return err
	}
	delete(r.entries, id.Slot())
	r.insertFree(id.Slot())
	return nil
}

func (r *Registry) lookup(id ServiceID) (registryEntry, error) {
	if !id.Valid() {
		return registryEntry{}, fmt.Errorf("%w: invalid service ID %s", ErrNotFound, id)
	}
	entry, ok := r.entries[id.Slot()]
	if !ok {
		if generation := r.generations[id.Slot()]; generation != 0 && generation != id.Generation() {
			return registryEntry{}, fmt.Errorf("%w: service ID %s", ErrStaleID, id)
		}
		return registryEntry{}, fmt.Errorf("%w: service ID %s", ErrNotFound, id)
	}
	if entry.id != id {
		return registryEntry{}, fmt.Errorf("%w: service ID %s", ErrStaleID, id)
	}
	return entry, nil
}

func (r *Registry) insertFree(slot uint32) {
	index := sort.Search(len(r.free), func(index int) bool { return r.free[index] >= slot })
	if index < len(r.free) && r.free[index] == slot {
		return
	}
	r.free = append(r.free, 0)
	copy(r.free[index+1:], r.free[index:])
	r.free[index] = slot
}

func (r *Registry) Snapshot() RegistryState {
	state := RegistryState{
		Limit:    r.limit,
		NextSlot: r.nextSlot,
	}
	slots := make([]uint32, 0, len(r.generations))
	for slot := range r.generations {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	for _, slot := range slots {
		state.Generations = append(state.Generations, RegistryGenerationState{
			Slot:       slot,
			Generation: r.generations[slot],
		})
	}
	entrySlots := make([]uint32, 0, len(r.entries))
	for slot := range r.entries {
		entrySlots = append(entrySlots, slot)
	}
	sort.Slice(entrySlots, func(i, j int) bool { return entrySlots[i] < entrySlots[j] })
	for _, slot := range entrySlots {
		entry := r.entries[slot]
		state.Entries = append(state.Entries, RegistryEntryState{
			ID:    entry.id,
			Kind:  entry.kind,
			Owner: entry.owner,
			Refs:  entry.refs,
		})
	}
	return state
}

func (r *Registry) Restore(state RegistryState) error {
	candidate, err := registryFromState(state)
	if err != nil {
		return err
	}
	*r = *candidate
	return nil
}

func registryFromState(state RegistryState) (*Registry, error) {
	if state.Limit == 0 || state.NextSlot == 0 ||
		uint64(state.NextSlot) > uint64(state.Limit)+1 ||
		uint64(len(state.Generations)) != uint64(state.NextSlot)-1 ||
		len(state.Generations) > int(state.Limit) ||
		len(state.Entries) > int(state.Limit) {
		return nil, fmt.Errorf("%w: invalid registry limits", ErrInvalidState)
	}
	candidate := NewRegistry(state.Limit)
	candidate.nextSlot = state.NextSlot
	var previousSlot uint32
	for index, saved := range state.Generations {
		if saved.Slot == 0 || saved.Generation == 0 ||
			(index != 0 && saved.Slot <= previousSlot) ||
			saved.Slot != uint32(index)+1 ||
			saved.Slot >= state.NextSlot {
			return nil, fmt.Errorf("%w: invalid registry generation %d", ErrInvalidState, index)
		}
		candidate.generations[saved.Slot] = saved.Generation
		previousSlot = saved.Slot
	}
	previousSlot = 0
	for index, saved := range state.Entries {
		if !saved.ID.Valid() || saved.Refs == 0 ||
			(index != 0 && saved.ID.Slot() <= previousSlot) {
			return nil, fmt.Errorf("%w: invalid registry entry %d", ErrInvalidState, index)
		}
		if err := saved.Kind.Validate(); err != nil {
			return nil, fmt.Errorf("%w: registry entry %d: %v", ErrInvalidState, index, err)
		}
		generation, ok := candidate.generations[saved.ID.Slot()]
		if !ok || generation != saved.ID.Generation() {
			return nil, fmt.Errorf("%w: registry entry %d has an invalid generation", ErrInvalidState, index)
		}
		candidate.entries[saved.ID.Slot()] = registryEntry{
			id:    saved.ID,
			kind:  saved.Kind,
			owner: saved.Owner,
			refs:  saved.Refs,
		}
		previousSlot = saved.ID.Slot()
	}
	for _, saved := range state.Generations {
		if _, active := candidate.entries[saved.Slot]; active {
			continue
		}
		candidate.free = append(candidate.free, saved.Slot)
	}
	return candidate, nil
}
