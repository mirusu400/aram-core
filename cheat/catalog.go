package cheat

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// CatalogVersion is the catalog schema revision this package reads and writes.
const CatalogVersion = 1

var (
	ErrUnsupportedCatalogVersion = errors.New("unsupported cheat catalog version")
	ErrCheatNotFound             = errors.New("cheat was not found")
)

// Address is a guest address. It serializes as a lowercase 0x-prefixed
// hexadecimal string so a hand-maintained catalog reads like a disassembly
// listing, and it accepts a plain JSON number as well.
type Address uint32

func (a Address) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("\"0x%08x\"", uint32(a))), nil
}

func (a *Address) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		return nil
	}
	if !strings.HasPrefix(trimmed, "\"") {
		var number uint32
		if err := json.Unmarshal(data, &number); err != nil {
			return fmt.Errorf("invalid guest address %s: %w", trimmed, err)
		}
		*a = Address(number)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	base := 16
	if lowered := strings.ToLower(text); strings.HasPrefix(lowered, "0x") {
		text = text[2:]
	} else {
		base = 10
	}
	value, err := strconv.ParseUint(text, base, 32)
	if err != nil {
		return fmt.Errorf("invalid guest address %s: %w", trimmed, err)
	}
	*a = Address(value)
	return nil
}

// Bytes is a guest byte string serialized as lowercase hexadecimal.
type Bytes []byte

func (b Bytes) MarshalJSON() ([]byte, error) {
	return json.Marshal(hex.EncodeToString(b))
}

func (b *Bytes) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	text = strings.ReplaceAll(strings.TrimSpace(text), " ", "")
	decoded, err := hex.DecodeString(text)
	if err != nil {
		return fmt.Errorf("invalid hexadecimal byte string %q: %w", text, err)
	}
	*b = decoded
	return nil
}

// Title identifies the application a catalog belongs to. SHA256 is the hash of
// the input file, the same identity the engine binds codes to.
type Title struct {
	SHA256    string `json:"sha256"`
	Name      string `json:"name,omitempty"`
	Carrier   string `json:"carrier,omitempty"`
	Format    string `json:"format,omitempty"`
	ProfileID string `json:"profile_id,omitempty"`
}

// Patch is one guarded byte edit. Expected is required: the catalog is keyed by
// the input hash, so the original bytes are known exactly and a mismatch means
// the patch is being applied to memory it was not authored against.
type Patch struct {
	Address  Address `json:"address"`
	Value    Bytes   `json:"value"`
	Expected Bytes   `json:"expected"`
	Note     string  `json:"note,omitempty"`
}

// Cheat is a user-visible entry that applies its patches as one unit.
type Cheat struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Description      string  `json:"description,omitempty"`
	Category         string  `json:"category,omitempty"`
	Author           string  `json:"author,omitempty"`
	Reference        string  `json:"reference,omitempty"`
	Freeze           bool    `json:"freeze,omitempty"`
	RestoreOnDisable bool    `json:"restore_on_disable,omitempty"`
	Patches          []Patch `json:"patches"`
}

// Catalog is the per-title cheat document stored in the cheat database.
type Catalog struct {
	Version int     `json:"version"`
	Title   Title   `json:"title"`
	Cheats  []Cheat `json:"cheats"`
}

// ParseCatalog decodes and validates one catalog document. Unknown fields are
// rejected so a newer schema fails loudly instead of silently dropping data.
func ParseCatalog(data []byte) (Catalog, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode cheat catalog: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func (c Catalog) Validate() error {
	if c.Version != CatalogVersion {
		return fmt.Errorf(
			"%w: got %d, want %d",
			ErrUnsupportedCatalogVersion,
			c.Version,
			CatalogVersion,
		)
	}
	target, err := normalizeSHA256(c.Title.SHA256)
	if err != nil {
		return fmt.Errorf("cheat catalog title: %w", err)
	}
	if target == "" {
		return fmt.Errorf("cheat catalog title: %w", ErrTargetIdentityUnavailable)
	}
	if len(c.Cheats) == 0 {
		return fmt.Errorf("cheat catalog for %s contains no cheats", target)
	}
	seen := make(map[string]bool, len(c.Cheats))
	for index, entry := range c.Cheats {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("cheat catalog entry %d: %w", index, err)
		}
		if seen[entry.ID] {
			return fmt.Errorf("duplicate cheat ID %q in cheat catalog", entry.ID)
		}
		seen[entry.ID] = true
	}
	return nil
}

func (c Cheat) Validate() error {
	if err := validateCheatID(c.ID); err != nil {
		return err
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("cheat %q has no name", c.ID)
	}
	if len(c.Patches) == 0 {
		return fmt.Errorf("cheat %q has no patches", c.ID)
	}
	for index, patch := range c.Patches {
		if len(patch.Value) == 0 || len(patch.Value) > MaxCodeBytes {
			return fmt.Errorf(
				"cheat %q patch %d byte count %d is outside 1..%d",
				c.ID,
				index,
				len(patch.Value),
				MaxCodeBytes,
			)
		}
		if len(patch.Expected) != len(patch.Value) {
			return fmt.Errorf(
				"cheat %q patch %d expects %d bytes but writes %d",
				c.ID,
				index,
				len(patch.Expected),
				len(patch.Value),
			)
		}
	}
	return nil
}

// validateCheatID keeps catalog IDs stable across filesystems and URLs, and
// reserves the separator used to derive per-patch code IDs.
func validateCheatID(id string) error {
	if id == "" {
		return errors.New("cheat ID is empty")
	}
	if len(id) > 64 {
		return fmt.Errorf("cheat ID %q is longer than 64 characters", id)
	}
	for _, character := range id {
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9',
			character == '-', character == '.', character == '_':
		default:
			return fmt.Errorf(
				"cheat ID %q contains %q; use lowercase letters, digits, '-', '.', or '_'",
				id,
				character,
			)
		}
	}
	return nil
}

// CodeID is the engine code identity for one patch of a cheat. The index is
// always present so adding a patch to a catalog entry never renames the codes
// that already exist.
func CodeID(cheatID string, patch int) string {
	return fmt.Sprintf("%s#%d", cheatID, patch)
}

// Codes converts a catalog entry into the engine codes that carry it.
func (c Cheat) Codes(targetSHA256 string) ([]Code, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	codes := make([]Code, 0, len(c.Patches))
	for index, patch := range c.Patches {
		description := c.Name
		if patch.Note != "" {
			description = c.Name + ": " + patch.Note
		}
		codes = append(codes, Code{
			ID:               CodeID(c.ID, index),
			Description:      description,
			TargetSHA256:     targetSHA256,
			Address:          uint32(patch.Address),
			Value:            append([]byte(nil), patch.Value...),
			Expected:         append([]byte(nil), patch.Expected...),
			Freeze:           c.Freeze,
			RestoreOnDisable: c.RestoreOnDisable,
		})
	}
	return codes, nil
}
