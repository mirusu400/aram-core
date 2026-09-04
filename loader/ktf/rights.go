package ktf

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// RightsKeyEnv names the environment variable holding one or more rights-key
// files. Entries are separated by the platform list separator. The loader
// reads nothing unless this variable is set, so headless runs and corpus
// sweeps stay reproducible.
const RightsKeyEnv = "ARAM_KTF_RIGHTS_KEYS"

// RightsKeyBytes is the length of an OMA DRM content-encryption key. KTF only
// ever issued AES-128 keys.
const RightsKeyBytes = 16

// ErrMalformedRightsKeys reports a rights-key file the loader cannot read.
var ErrMalformedRightsKeys = errors.New("malformed KTF rights-key file")

// RightsKeys maps an OMA DRM content identifier to the content-encryption key
// a KTF Rights Object carried for it. KTF names WIPI content
// "00WIPI" + the application ID padded to 18 characters, so the eight-digit
// AID is accepted as a shorthand for the same entry.
type RightsKeys map[string][]byte

var (
	rightsMu     sync.RWMutex
	rightsLoaded bool
	rightsKeys   RightsKeys
)

// SetRightsKeys installs the content-encryption keys used to open protected
// KTF packages, replacing any keys loaded earlier. Passing nil clears the
// store and re-arms the environment load.
func SetRightsKeys(keys RightsKeys) {
	rightsMu.Lock()
	defer rightsMu.Unlock()
	if keys == nil {
		rightsKeys = nil
		rightsLoaded = false
		return
	}
	rightsKeys = normalizeRightsKeys(keys)
	rightsLoaded = true
}

// RightsKeyIDs lists the identifiers the store can currently open, in no
// particular order. It exists so callers can report what is available without
// exposing key material.
func RightsKeyIDs() []string {
	keys := activeRightsKeys()
	ids := make([]string, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	return ids
}

func activeRightsKeys() RightsKeys {
	rightsMu.RLock()
	if rightsLoaded {
		keys := rightsKeys
		rightsMu.RUnlock()
		return keys
	}
	rightsMu.RUnlock()

	rightsMu.Lock()
	defer rightsMu.Unlock()
	if rightsLoaded {
		return rightsKeys
	}
	rightsKeys = loadRightsKeysFromEnv(os.Getenv(RightsKeyEnv))
	rightsLoaded = true
	return rightsKeys
}

func loadRightsKeysFromEnv(value string) RightsKeys {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	merged := RightsKeys{}
	for _, entry := range filepath.SplitList(value) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		data, err := os.ReadFile(entry)
		if err != nil {
			continue
		}
		keys, err := ParseRightsKeys(data)
		if err != nil {
			continue
		}
		for id, key := range keys {
			merged[id] = key
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// ParseRightsKeys reads a rights-key file. Each non-empty line names one
// content identifier and its hexadecimal key, separated by whitespace or an
// equals sign; '#' starts a comment.
//
//	# 2009 화이트데이
//	00WIPI000000000001040928 = 000102030405060708090a0b0c0d0e0f
//	01041FE1 101112131415161718191a1b1c1d1e1f
func ParseRightsKeys(data []byte) (RightsKeys, error) {
	keys := RightsKeys{}
	for number, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if cut := strings.IndexByte(line, '#'); cut >= 0 {
			line = strings.TrimSpace(line[:cut])
		}
		if line == "" {
			continue
		}
		id, value, err := splitRightsKeyLine(line)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %v", ErrMalformedRightsKeys, number+1, err)
		}
		key, err := hex.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: line %d: key is not hexadecimal",
				ErrMalformedRightsKeys,
				number+1,
			)
		}
		if len(key) != RightsKeyBytes {
			return nil, fmt.Errorf(
				"%w: line %d: key is %d bytes, want %d",
				ErrMalformedRightsKeys,
				number+1,
				len(key),
				RightsKeyBytes,
			)
		}
		keys[normalizeRightsKeyID(id)] = key
	}
	if len(keys) == 0 {
		return nil, nil
	}
	return keys, nil
}

func splitRightsKeyLine(line string) (id, value string, err error) {
	separator := strings.IndexAny(line, "=\t ")
	if separator < 0 {
		return "", "", errors.New("expected \"<content id> <hex key>\"")
	}
	id = strings.TrimSpace(line[:separator])
	value = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line[separator:]), "="))
	if id == "" || value == "" {
		return "", "", errors.New("expected \"<content id> <hex key>\"")
	}
	return id, value, nil
}

func normalizeRightsKeys(keys RightsKeys) RightsKeys {
	normalized := make(RightsKeys, len(keys))
	for id, key := range keys {
		if len(key) != RightsKeyBytes {
			continue
		}
		normalized[normalizeRightsKeyID(id)] = key
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeRightsKeyID(id string) string {
	return strings.ToUpper(strings.TrimSpace(id))
}

// lookupRightsKey resolves the content-encryption key for one DCF content
// identifier. KTF derives the identifier from the AID, so a store keyed by
// either spelling opens the same package.
func lookupRightsKey(contentID string) []byte {
	keys := activeRightsKeys()
	if len(keys) == 0 {
		return nil
	}
	id := normalizeRightsKeyID(contentID)
	if key, ok := keys[id]; ok {
		return key
	}
	if aid := rightsKeyAID(id); aid != "" {
		if key, ok := keys[aid]; ok {
			return key
		}
	}
	return nil
}

// rightsKeyAID returns the AID a KTF content identifier ends with, or "" when
// the identifier does not follow the "00WIPI" + zero-padded AID shape.
func rightsKeyAID(id string) string {
	trimmed := strings.TrimPrefix(id, "00WIPI")
	if trimmed == id {
		return ""
	}
	trimmed = strings.TrimLeft(trimmed, "0")
	if len(trimmed) == 0 || len(trimmed) > 8 {
		return ""
	}
	return strings.Repeat("0", 8-len(trimmed)) + trimmed
}
