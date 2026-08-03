package cheat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Memory is the minimal backend needed by the host-side cheat engine.
type Memory interface {
	ReadMemory(address uint32, destination []byte) error
	WriteMemory(address uint32, source []byte) error
}

// Code is a persistent, title-keyed memory modification. An omitted
// TargetSHA256 is filled from the engine target. An omitted Expected value is
// captured when the code is added, so enabling it still performs an
// expected-original check.
type Code struct {
	ID               string `json:"id"`
	Description      string `json:"description,omitempty"`
	TargetSHA256     string `json:"target_sha256"`
	Address          uint32 `json:"address"`
	Value            []byte `json:"value"`
	Expected         []byte `json:"expected"`
	Freeze           bool   `json:"freeze"`
	RestoreOnDisable bool   `json:"restore_on_disable"`
}

// CodeState is an immutable snapshot of a registered code.
type CodeState struct {
	Code    Code `json:"code"`
	Enabled bool `json:"enabled"`
}

type storedCode struct {
	code     Code
	enabled  bool
	original []byte
}

type scanCandidate struct {
	address  uint32
	region   int
	previous []byte
}

type scanState struct {
	valueType  ValueType
	alignment  uint32
	candidates []scanCandidate
}

// Engine owns memory scans and cheat code state. It is safe for concurrent
// use; the supplied Memory implementation must serialize against guest
// execution.
type Engine struct {
	mu           sync.Mutex
	memory       Memory
	targetSHA256 string
	imageSHA256  string
	byteOrder    Endian
	regions      []Region
	maxScanBytes uint64
	maxResults   int
	codes        map[string]*storedCode
	scan         *scanState
}

func New(memory Memory, options Options) (*Engine, error) {
	if memory == nil {
		return nil, fmt.Errorf("cheat memory backend is nil")
	}
	if !options.ByteOrder.valid() {
		return nil, fmt.Errorf("invalid cheat byte order %d", options.ByteOrder)
	}
	target, err := normalizeSHA256(options.TargetSHA256)
	if err != nil {
		return nil, err
	}
	imageIdentity, err := normalizeSHA256(options.ImageSHA256)
	if err != nil {
		return nil, err
	}
	regions := append([]Region(nil), options.Regions...)
	names := make(map[string]bool, len(regions))
	for index := range regions {
		regions[index].Name = strings.TrimSpace(regions[index].Name)
		if err := regions[index].Validate(); err != nil {
			return nil, err
		}
		if names[regions[index].Name] {
			return nil, fmt.Errorf("duplicate cheat region %q", regions[index].Name)
		}
		names[regions[index].Name] = true
	}
	if len(regions) == 0 {
		return nil, fmt.Errorf("no cheat memory regions were configured")
	}
	maxScanBytes := options.MaxScanBytes
	if maxScanBytes == 0 {
		maxScanBytes = DefaultMaxScanBytes
	}
	maxResults := options.MaxResults
	if maxResults == 0 {
		maxResults = DefaultMaxResults
	}
	if maxResults < 0 {
		return nil, fmt.Errorf("negative cheat scan result limit %d", maxResults)
	}
	return &Engine{
		memory:       memory,
		targetSHA256: target,
		imageSHA256:  imageIdentity,
		byteOrder:    options.ByteOrder,
		regions:      regions,
		maxScanBytes: maxScanBytes,
		maxResults:   maxResults,
		codes:        make(map[string]*storedCode),
	}, nil
}

func normalizeSHA256(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if len(value) != sha256.Size*2 {
		return "", fmt.Errorf("target SHA-256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("invalid target SHA-256: %w", err)
	}
	return value, nil
}

func (e *Engine) TargetSHA256() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.targetSHA256
}

// ImageSHA256 is the loaded executable image's identity, empty when the host
// did not supply one.
func (e *Engine) ImageSHA256() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.imageSHA256
}

// Identities lists every hash a code may bind to, most specific first.
func (e *Engine) Identities() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.identitiesLocked()
}

func (e *Engine) identitiesLocked() []string {
	identities := make([]string, 0, 2)
	if e.imageSHA256 != "" {
		identities = append(identities, e.imageSHA256)
	}
	if e.targetSHA256 != "" {
		identities = append(identities, e.targetSHA256)
	}
	return identities
}

// MatchIdentity returns the first candidate this engine accepts. Candidates
// are compared against the loaded image identity and the input file identity.
func (e *Engine) MatchIdentity(candidates []string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	identities := e.identitiesLocked()
	if len(identities) == 0 {
		return "", ErrTargetIdentityUnavailable
	}
	normalized := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		value, err := normalizeSHA256(candidate)
		if err != nil {
			return "", err
		}
		if value == "" {
			continue
		}
		for _, identity := range identities {
			if value == identity {
				return identity, nil
			}
		}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return "", ErrTargetIdentityUnavailable
	}
	return "", fmt.Errorf(
		"%w: got %s, want one of %s",
		ErrWrongTarget,
		strings.Join(normalized, ", "),
		strings.Join(identities, ", "),
	)
}

func (e *Engine) Regions() []Region {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Region(nil), e.regions...)
}

func (e *Engine) Read(address uint32, valueType ValueType) (Value, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !valueType.Valid() {
		return Value{}, fmt.Errorf("invalid value type %d", valueType)
	}
	data, err := e.readBytesLocked(address, valueType.Size())
	if err != nil {
		return Value{}, err
	}
	return Decode(valueType, data, e.byteOrder)
}

func (e *Engine) ReadBytes(address uint32, size int) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.readBytesLocked(address, size)
}

func (e *Engine) readBytesLocked(address uint32, size int) ([]byte, error) {
	if _, err := e.regionForLocked(address, size, false); err != nil {
		return nil, err
	}
	output := make([]byte, size)
	if err := e.memory.ReadMemory(address, output); err != nil {
		return nil, fmt.Errorf("read cheat memory at 0x%08x: %w", address, err)
	}
	return output, nil
}

func (e *Engine) Write(address uint32, value Value, expected *Value) error {
	encoded, err := value.Encode(e.byteOrder)
	if err != nil {
		return err
	}
	var expectedBytes []byte
	if expected != nil {
		if expected.Type != value.Type {
			return fmt.Errorf(
				"expected value type %d does not match write type %d",
				expected.Type,
				value.Type,
			)
		}
		expectedBytes, err = expected.Encode(e.byteOrder)
		if err != nil {
			return err
		}
	}
	return e.WriteBytes(address, encoded, expectedBytes)
}

func (e *Engine) WriteBytes(address uint32, value, expected []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.writeBytesLocked(address, value, expected)
}

func (e *Engine) writeBytesLocked(address uint32, value, expected []byte) error {
	if len(value) == 0 || len(value) > MaxCodeBytes {
		return fmt.Errorf("cheat write size %d is outside 1..%d", len(value), MaxCodeBytes)
	}
	if _, err := e.regionForLocked(address, len(value), true); err != nil {
		return err
	}
	if expected != nil {
		if len(expected) != len(value) {
			return fmt.Errorf(
				"expected byte count %d does not match write byte count %d",
				len(expected),
				len(value),
			)
		}
		current := make([]byte, len(value))
		if err := e.memory.ReadMemory(address, current); err != nil {
			return fmt.Errorf("read expected memory at 0x%08x: %w", address, err)
		}
		if !bytes.Equal(current, expected) {
			return fmt.Errorf(
				"%w at 0x%08x: got %x, want %x",
				ErrUnexpectedOriginal,
				address,
				current,
				expected,
			)
		}
	}
	if err := e.memory.WriteMemory(address, value); err != nil {
		return fmt.Errorf("write cheat memory at 0x%08x: %w", address, err)
	}
	return nil
}

func (e *Engine) regionForLocked(
	address uint32,
	size int,
	writable bool,
) (Region, error) {
	for _, region := range e.regions {
		if !region.contains(address, size) {
			continue
		}
		if writable && !region.Writable {
			return Region{}, fmt.Errorf(
				"%w: %s at 0x%08x",
				ErrReadOnlyRegion,
				region.Name,
				address,
			)
		}
		return region, nil
	}
	return Region{}, fmt.Errorf(
		"%w: 0x%08x..0x%08x",
		ErrAddressOutsideRegions,
		address,
		uint64(address)+uint64(max(size, 0)),
	)
}

func (e *Engine) AddCode(code Code) (CodeState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	code.ID = strings.TrimSpace(code.ID)
	if code.ID == "" {
		return CodeState{}, fmt.Errorf("cheat code ID is empty")
	}
	if _, exists := e.codes[code.ID]; exists {
		return CodeState{}, fmt.Errorf("%w: %s", ErrCodeAlreadyExists, code.ID)
	}
	if len(code.Value) == 0 || len(code.Value) > MaxCodeBytes {
		return CodeState{}, fmt.Errorf(
			"cheat code %q byte count %d is outside 1..%d",
			code.ID,
			len(code.Value),
			MaxCodeBytes,
		)
	}
	if _, err := e.regionForLocked(code.Address, len(code.Value), true); err != nil {
		return CodeState{}, err
	}
	target, err := normalizeSHA256(code.TargetSHA256)
	if err != nil {
		return CodeState{}, fmt.Errorf("cheat code %q: %w", code.ID, err)
	}
	identities := e.identitiesLocked()
	if len(identities) == 0 {
		return CodeState{}, fmt.Errorf(
			"cheat code %q: %w",
			code.ID,
			ErrTargetIdentityUnavailable,
		)
	}
	if target == "" {
		// An omitted identity binds to the loaded image when one is known, so
		// a code written by hand follows the same rule published catalogs do.
		target = identities[0]
	}
	if !slices.Contains(identities, target) {
		return CodeState{}, fmt.Errorf(
			"cheat code %q: %w: got %s, want one of %s",
			code.ID,
			ErrWrongTarget,
			target,
			strings.Join(identities, ", "),
		)
	}
	code.TargetSHA256 = target
	code.Value = append([]byte(nil), code.Value...)

	current := make([]byte, len(code.Value))
	if err := e.memory.ReadMemory(code.Address, current); err != nil {
		return CodeState{}, fmt.Errorf(
			"read cheat code %q original at 0x%08x: %w",
			code.ID,
			code.Address,
			err,
		)
	}
	if code.Expected == nil {
		code.Expected = append([]byte(nil), current...)
	} else {
		if len(code.Expected) != len(code.Value) {
			return CodeState{}, fmt.Errorf(
				"cheat code %q expected byte count %d does not match value byte count %d",
				code.ID,
				len(code.Expected),
				len(code.Value),
			)
		}
		if !bytes.Equal(current, code.Expected) {
			return CodeState{}, fmt.Errorf(
				"cheat code %q: %w at 0x%08x: got %x, want %x",
				code.ID,
				ErrUnexpectedOriginal,
				code.Address,
				current,
				code.Expected,
			)
		}
		code.Expected = append([]byte(nil), code.Expected...)
	}
	stored := &storedCode{code: code}
	e.codes[code.ID] = stored
	return snapshotCode(stored), nil
}

func (e *Engine) EnableCode(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	stored, err := e.codeLocked(id)
	if err != nil {
		return err
	}
	if stored.enabled {
		return nil
	}
	current := make([]byte, len(stored.code.Value))
	if err := e.memory.ReadMemory(stored.code.Address, current); err != nil {
		return fmt.Errorf("read cheat code %q original: %w", stored.code.ID, err)
	}
	if !bytes.Equal(current, stored.code.Expected) {
		return fmt.Errorf(
			"enable cheat code %q: %w at 0x%08x: got %x, want %x",
			stored.code.ID,
			ErrUnexpectedOriginal,
			stored.code.Address,
			current,
			stored.code.Expected,
		)
	}
	if err := e.memory.WriteMemory(stored.code.Address, stored.code.Value); err != nil {
		return fmt.Errorf("enable cheat code %q: %w", stored.code.ID, err)
	}
	stored.original = current
	stored.enabled = true
	return nil
}

func (e *Engine) DisableCode(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	stored, err := e.codeLocked(id)
	if err != nil {
		return err
	}
	if !stored.enabled {
		return nil
	}
	if stored.code.RestoreOnDisable && stored.original != nil {
		if err := e.memory.WriteMemory(stored.code.Address, stored.original); err != nil {
			return fmt.Errorf("restore cheat code %q original: %w", stored.code.ID, err)
		}
	}
	stored.enabled = false
	stored.original = nil
	return nil
}

func (e *Engine) SetCodeEnabled(id string, enabled bool) error {
	if enabled {
		return e.EnableCode(id)
	}
	return e.DisableCode(id)
}

func (e *Engine) RemoveCode(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	stored, err := e.codeLocked(id)
	if err != nil {
		return err
	}
	if stored.enabled && stored.code.RestoreOnDisable && stored.original != nil {
		if err := e.memory.WriteMemory(stored.code.Address, stored.original); err != nil {
			return fmt.Errorf("restore removed cheat code %q original: %w", stored.code.ID, err)
		}
	}
	delete(e.codes, stored.code.ID)
	return nil
}

func (e *Engine) Codes() []CodeState {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := e.sortedCodeIDsLocked(false)
	output := make([]CodeState, 0, len(ids))
	for _, id := range ids {
		output = append(output, snapshotCode(e.codes[id]))
	}
	return output
}

func (e *Engine) ApplyFrozen() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.applyFrozenLocked()
}

func (e *Engine) applyFrozenLocked() error {
	for _, id := range e.sortedCodeIDsLocked(true) {
		stored := e.codes[id]
		if !stored.code.Freeze {
			continue
		}
		if err := e.memory.WriteMemory(stored.code.Address, stored.code.Value); err != nil {
			return fmt.Errorf("freeze cheat code %q: %w", stored.code.ID, err)
		}
	}
	return nil
}

func (e *Engine) ReapplyEnabled() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reapplyEnabledLocked()
}

func (e *Engine) reapplyEnabledLocked() error {
	for _, id := range e.sortedCodeIDsLocked(true) {
		stored := e.codes[id]
		if err := e.memory.WriteMemory(stored.code.Address, stored.code.Value); err != nil {
			return fmt.Errorf("reapply cheat code %q: %w", stored.code.ID, err)
		}
	}
	return nil
}

type applyMode uint8

const (
	applyNone applyMode = iota
	applyFrozen
	applyAllEnabled
)

// runMachine serializes guest execution and state changes with every direct
// cheat memory operation. Callers must use the wrapped Machine rather than the
// underlying machine after attachment for this guarantee to hold.
func (e *Engine) runMachine(action func() error, mode applyMode) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := action(); err != nil {
		return err
	}
	switch mode {
	case applyFrozen:
		return e.applyFrozenLocked()
	case applyAllEnabled:
		return e.reapplyEnabledLocked()
	default:
		return nil
	}
}

func (e *Engine) codeLocked(id string) (*storedCode, error) {
	id = strings.TrimSpace(id)
	stored := e.codes[id]
	if stored == nil {
		return nil, fmt.Errorf("%w: %s", ErrCodeNotFound, id)
	}
	return stored, nil
}

func (e *Engine) sortedCodeIDsLocked(enabledOnly bool) []string {
	ids := make([]string, 0, len(e.codes))
	for id, stored := range e.codes {
		if enabledOnly && !stored.enabled {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func snapshotCode(stored *storedCode) CodeState {
	code := stored.code
	code.Value = append([]byte(nil), code.Value...)
	code.Expected = append([]byte(nil), code.Expected...)
	return CodeState{Code: code, Enabled: stored.enabled}
}
