package cheat

import (
	"errors"
	"strings"
	"testing"
)

func identityOptions() Options {
	options := testOptions(16)
	options.TargetSHA256 = strings.Repeat("ab", 32)
	options.ImageSHA256 = strings.Repeat("cd", 32)
	return options
}

func TestEngineAcceptsEitherTheImageOrTheFileIdentity(t *testing.T) {
	t.Parallel()
	engine, err := New(newTestMemory(16), identityOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got := engine.Identities(); len(got) != 2 ||
		got[0] != strings.Repeat("cd", 32) ||
		got[1] != strings.Repeat("ab", 32) {
		t.Fatalf("identities = %v, want the image identity first", got)
	}

	for name, target := range map[string]string{
		"image": strings.Repeat("cd", 32),
		"file":  strings.Repeat("ab", 32),
		"empty": "",
	} {
		state, err := engine.AddCode(Code{
			ID:           name,
			TargetSHA256: target,
			Address:      testMemoryBase,
			Value:        []byte{1},
		})
		if err != nil {
			t.Fatalf("%s identity: %v", name, err)
		}
		if name == "empty" && state.Code.TargetSHA256 != strings.Repeat("cd", 32) {
			t.Fatalf("omitted identity bound to %s", state.Code.TargetSHA256)
		}
	}

	if _, err := engine.AddCode(Code{
		ID:           "other",
		TargetSHA256: strings.Repeat("ef", 32),
		Address:      testMemoryBase,
		Value:        []byte{1},
	}); !errors.Is(err, ErrWrongTarget) {
		t.Fatalf("foreign identity error = %v", err)
	}
}

func TestMatchIdentityPrefersTheImageOverTheContainer(t *testing.T) {
	t.Parallel()
	engine, err := New(newTestMemory(16), identityOptions())
	if err != nil {
		t.Fatal(err)
	}
	got, err := engine.MatchIdentity([]string{
		strings.Repeat("cd", 32),
		strings.Repeat("ab", 32),
	})
	if err != nil || got != strings.Repeat("cd", 32) {
		t.Fatalf("match = %q, %v", got, err)
	}
	if got, err := engine.MatchIdentity([]string{strings.Repeat("ab", 32)}); err != nil ||
		got != strings.Repeat("ab", 32) {
		t.Fatalf("container-only match = %q, %v", got, err)
	}
	if _, err := engine.MatchIdentity([]string{strings.Repeat("ef", 32)}); !errors.Is(
		err,
		ErrWrongTarget,
	) {
		t.Fatalf("foreign match error = %v", err)
	}
}

// A repacked container keeps the image identity, so its catalog still applies.
func TestLibraryImportsACatalogThatOnlyNamesTheImage(t *testing.T) {
	t.Parallel()
	memory := newTestMemory(16)
	if err := memory.WriteMemory(testMemoryBase, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	engine, err := New(memory, identityOptions())
	if err != nil {
		t.Fatal(err)
	}
	library, err := NewLibrary(engine)
	if err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{
		Version: CatalogVersion,
		Title: Title{
			ImageSHA256: strings.Repeat("cd", 32),
			FileSHA256:  []string{strings.Repeat("99", 32)},
			Name:        "Repacked",
		},
		Cheats: []Cheat{{
			ID:      "patch",
			Name:    "Patch",
			Patches: []Patch{{Address: Address(testMemoryBase), Value: Bytes{9}, Expected: Bytes{1}}},
		}},
	}
	if err := library.Import(catalog); err != nil {
		t.Fatal(err)
	}
	if entries := library.Entries(); len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
}
