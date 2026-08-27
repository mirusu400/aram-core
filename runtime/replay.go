package runtime

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"time"
)

type ReplayMode uint8

const (
	ReplayOff ReplayMode = iota
	ReplayRecord
	ReplayPlayback
)

func (m ReplayMode) Valid() bool {
	return m <= ReplayPlayback
}

type ReplayKind string

const (
	ReplayClockAdvance    ReplayKind = "clock.advance"
	ReplayInput           ReplayKind = "input"
	ReplayNetworkResponse ReplayKind = "network.response"
	ReplayDeviceResponse  ReplayKind = "device.response"
	ReplaySchedulerChoice ReplayKind = "scheduler.choice"
)

func (k ReplayKind) valid() bool {
	switch k {
	case ReplayClockAdvance, ReplayInput, ReplayNetworkResponse,
		ReplayDeviceResponse, ReplaySchedulerChoice:
		return true
	default:
		return false
	}
}

type ReplayLimits struct {
	MaxEntries uint32
	MaxData    uint32
}

func DefaultReplayLimits() ReplayLimits {
	return ReplayLimits{MaxEntries: 100_000, MaxData: 8 << 20}
}

func (l ReplayLimits) Validate() error {
	if l.MaxEntries == 0 || l.MaxData == 0 {
		return fmt.Errorf("%w: invalid replay limits", ErrInvalidArgument)
	}
	return nil
}

type ReplayEntry struct {
	Sequence  uint64
	AtNS      int64
	Kind      ReplayKind
	Owner     OwnerID
	ServiceID ServiceID
	Name      string
	Value     int64
	Aux       int64
	Data      []byte
}

type ReplayState struct {
	Limits       ReplayLimits
	Mode         ReplayMode
	RandomSeed   uint64
	ProfileID    string
	ProfileHash  [32]byte
	NextSequence uint64
	Cursor       uint32
	Entries      []ReplayEntry
}

// Replay is a bounded semantic log. Trace collection remains independent and
// cannot affect this sequence.
type Replay struct {
	limits       ReplayLimits
	mode         ReplayMode
	randomSeed   uint64
	profileID    string
	profileHash  [32]byte
	nextSequence uint64
	cursor       uint32
	entries      []ReplayEntry
}

// replayAdvanceState is enough to undo Consume or RecordAdvance. Frame
// advancement never edits an existing replay entry or changes replay mode and
// profile identity, so cloning the entire log each tick is unnecessary.
type replayAdvanceState struct {
	nextSequence uint64
	cursor       uint32
	entries      int
}

func NewReplay(
	limits ReplayLimits,
	mode ReplayMode,
	randomSeed uint64,
	profileID string,
) (*Replay, error) {
	if limits == (ReplayLimits{}) {
		limits = DefaultReplayLimits()
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	if !mode.Valid() || strings.TrimSpace(profileID) == "" || len(profileID) > 255 {
		return nil, fmt.Errorf("%w: invalid replay configuration", ErrInvalidArgument)
	}
	if strings.IndexByte(profileID, 0) >= 0 {
		return nil, fmt.Errorf("%w: invalid replay configuration", ErrInvalidArgument)
	}
	return &Replay{
		limits: limits, mode: mode, randomSeed: randomSeed,
		profileID: profileID, nextSequence: 1,
	}, nil
}

func (r *Replay) Mode() ReplayMode {
	return r.mode
}

func (r *Replay) SetMode(mode ReplayMode) error {
	if !mode.Valid() {
		return fmt.Errorf("%w: invalid replay mode", ErrInvalidArgument)
	}
	if mode == ReplayRecord && r.cursor != 0 {
		return fmt.Errorf("%w: cannot record after playback began", ErrInvalidState)
	}
	r.mode = mode
	return nil
}

func (r *Replay) setProfileHash(hash [32]byte) {
	r.profileHash = hash
}

func (r *Replay) Record(entry ReplayEntry) (uint64, error) {
	if r.mode != ReplayRecord {
		return 0, fmt.Errorf("%w: replay is not recording", ErrInvalidState)
	}
	if err := r.validateEntry(entry, false); err != nil {
		return 0, err
	}
	if uint32(len(r.entries)) >= r.limits.MaxEntries ||
		r.nextSequence == 0 || r.nextSequence == math.MaxUint64 {
		return 0, fmt.Errorf("%w: replay log exhausted", ErrLimitExceeded)
	}
	used := replayDataBytes(r.entries)
	if used > uint64(r.limits.MaxData) ||
		uint64(len(entry.Data)) > uint64(r.limits.MaxData)-used {
		return 0, fmt.Errorf("%w: replay data exceeds %d bytes", ErrLimitExceeded, r.limits.MaxData)
	}
	entry.Sequence = r.nextSequence
	r.nextSequence++
	entry.Data = cloneBytes(entry.Data)
	r.entries = append(r.entries, entry)
	return entry.Sequence, nil
}

func (r *Replay) RecordAdvance(owner OwnerID, at, delta time.Duration) error {
	if r.mode != ReplayRecord {
		return nil
	}
	_, err := r.Record(ReplayEntry{
		AtNS: int64(at), Kind: ReplayClockAdvance, Owner: owner,
		Value: int64(delta),
	})
	return err
}

func (r *Replay) captureAdvance(destination *replayAdvanceState) {
	destination.nextSequence = r.nextSequence
	destination.cursor = r.cursor
	destination.entries = len(r.entries)
}

func (r *Replay) restoreAdvance(saved *replayAdvanceState) {
	if saved == nil {
		return
	}
	r.nextSequence = saved.nextSequence
	r.cursor = saved.cursor
	if saved.entries < len(r.entries) {
		clear(r.entries[saved.entries:])
		r.entries = r.entries[:saved.entries]
	}
}

func (r *Replay) RecordInput(
	owner OwnerID,
	at time.Duration,
	control string,
	pressed bool,
) error {
	if r.mode != ReplayRecord {
		return nil
	}
	_, err := r.Record(ReplayEntry{
		AtNS: int64(at), Kind: ReplayInput, Owner: owner,
		Name: control, Value: boolValue(pressed),
	})
	return err
}

func (r *Replay) Peek() (ReplayEntry, bool) {
	if int(r.cursor) >= len(r.entries) {
		return ReplayEntry{}, false
	}
	return cloneReplayEntry(r.entries[r.cursor]), true
}

func (r *Replay) Consume(expected ReplayEntry) error {
	if r.mode != ReplayPlayback {
		return fmt.Errorf("%w: replay is not playing", ErrInvalidState)
	}
	current, ok := r.Peek()
	if !ok {
		return fmt.Errorf("%w: replay log ended", ErrNotFound)
	}
	expected.Sequence = current.Sequence
	if !replayEntriesEqual(current, expected) {
		return fmt.Errorf(
			"%w: replay entry %d mismatch (%s/%s)",
			ErrInvalidState,
			current.Sequence,
			current.Kind,
			expected.Kind,
		)
	}
	r.cursor++
	return nil
}

func (r *Replay) Entries() []ReplayEntry {
	result := make([]ReplayEntry, len(r.entries))
	for index, entry := range r.entries {
		result[index] = cloneReplayEntry(entry)
	}
	return result
}

func (r *Replay) Snapshot() ReplayState {
	return ReplayState{
		Limits: r.limits, Mode: r.mode, RandomSeed: r.randomSeed,
		ProfileID: r.profileID, ProfileHash: r.profileHash,
		NextSequence: r.nextSequence, Cursor: r.cursor, Entries: r.Entries(),
	}
}

func (r *Replay) Restore(state ReplayState) error {
	if err := state.Limits.Validate(); err != nil ||
		!state.Mode.Valid() || strings.TrimSpace(state.ProfileID) == "" ||
		len(state.ProfileID) > 255 ||
		strings.IndexByte(state.ProfileID, 0) >= 0 ||
		state.RandomSeed != r.randomSeed ||
		state.ProfileID != r.profileID ||
		state.ProfileHash != r.profileHash ||
		state.NextSequence == 0 ||
		uint64(state.NextSequence) != uint64(len(state.Entries))+1 ||
		len(state.Entries) > int(state.Limits.MaxEntries) ||
		state.Cursor > uint32(len(state.Entries)) {
		return fmt.Errorf("%w: invalid replay state", ErrInvalidState)
	}
	entries := make([]ReplayEntry, len(state.Entries))
	var previous uint64
	var dataBytes uint64
	for index, entry := range state.Entries {
		if entry.Sequence != uint64(index)+1 ||
			entry.Sequence >= state.NextSequence ||
			(index != 0 && entry.Sequence <= previous) ||
			r.validateEntryWithLimits(entry, state.Limits, true) != nil {
			return fmt.Errorf("%w: invalid replay entry %d", ErrInvalidState, index)
		}
		dataBytes += uint64(len(entry.Data))
		if dataBytes > uint64(state.Limits.MaxData) {
			return fmt.Errorf("%w: replay data exceeds limit", ErrInvalidState)
		}
		entries[index] = cloneReplayEntry(entry)
		previous = entry.Sequence
	}
	r.limits = state.Limits
	r.mode = state.Mode
	r.randomSeed = state.RandomSeed
	r.profileID = state.ProfileID
	r.profileHash = state.ProfileHash
	r.nextSequence = state.NextSequence
	r.cursor = state.Cursor
	r.entries = entries
	return nil
}

func replayDataBytes(entries []ReplayEntry) uint64 {
	var total uint64
	for _, entry := range entries {
		total += uint64(len(entry.Data))
	}
	return total
}

func (r *Replay) validateEntry(entry ReplayEntry, saved bool) error {
	return r.validateEntryWithLimits(entry, r.limits, saved)
}

func (r *Replay) validateEntryWithLimits(
	entry ReplayEntry,
	limits ReplayLimits,
	saved bool,
) error {
	if !entry.Kind.valid() || entry.AtNS < 0 ||
		len(entry.Name) > 255 || strings.IndexByte(entry.Name, 0) >= 0 ||
		(!saved && entry.Sequence != 0) {
		return fmt.Errorf("%w: invalid replay entry", ErrInvalidArgument)
	}
	if len(entry.Data) > int(limits.MaxData) {
		return fmt.Errorf("%w: replay entry data exceeds limit", ErrLimitExceeded)
	}
	switch entry.Kind {
	case ReplayClockAdvance:
		if entry.Value < 0 || entry.ServiceID != 0 ||
			entry.Name != "" || len(entry.Data) != 0 {
			return fmt.Errorf("%w: invalid clock replay entry", ErrInvalidArgument)
		}
	case ReplayInput:
		if strings.TrimSpace(entry.Name) == "" ||
			entry.ServiceID != 0 ||
			(entry.Value != 0 && entry.Value != 1) || len(entry.Data) != 0 {
			return fmt.Errorf("%w: invalid input replay entry", ErrInvalidArgument)
		}
	case ReplayNetworkResponse:
		if !entry.ServiceID.Valid() || strings.TrimSpace(entry.Name) == "" {
			return fmt.Errorf("%w: invalid network replay entry", ErrInvalidArgument)
		}
	case ReplayDeviceResponse, ReplaySchedulerChoice:
		if strings.TrimSpace(entry.Name) == "" {
			return fmt.Errorf("%w: invalid replay entry name", ErrInvalidArgument)
		}
	}
	return nil
}

func cloneReplayEntry(entry ReplayEntry) ReplayEntry {
	entry.Data = cloneBytes(entry.Data)
	return entry
}

func replayEntriesEqual(left, right ReplayEntry) bool {
	return left.Sequence == right.Sequence && left.AtNS == right.AtNS &&
		left.Kind == right.Kind && left.Owner == right.Owner &&
		left.ServiceID == right.ServiceID &&
		left.Name == right.Name && left.Value == right.Value &&
		left.Aux == right.Aux && bytes.Equal(left.Data, right.Data)
}
