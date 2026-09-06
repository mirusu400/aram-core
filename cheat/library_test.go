package cheat

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func newTestLibrary(t *testing.T) (*Library, *testMemory) {
	t.Helper()
	memory := newTestMemory(16)
	check(t, memory.WriteMemory(
		testMemoryBase,
		[]byte{1, 2, 3, 4, 5, 6, 7, 8},
	))
	engine, err := New(memory, testOptions(16))
	check(t, err)
	library, err := NewLibrary(engine)
	check(t, err)
	return library, memory
}

func testCatalog() Catalog {
	return Catalog{
		Version: CatalogVersion,
		Title:   Title{ImageSHA256: strings.Repeat("ab", 32), Name: "Synthetic"},
		Cheats: []Cheat{{
			ID:               "skip-auth",
			Name:             "Skip server authentication",
			RestoreOnDisable: true,
			Patches: []Patch{
				{
					Address:  Address(testMemoryBase),
					Value:    Bytes{0xaa, 0xbb, 0xcc, 0xdd},
					Expected: Bytes{1, 2, 3, 4},
				},
				{
					Address:  Address(testMemoryBase + 4),
					Value:    Bytes{0xee, 0xff},
					Expected: Bytes{5, 6},
				},
			},
		}},
	}
}

func TestLibraryEnablesEveryPatchOfACheatTogether(t *testing.T) {
	t.Parallel()
	library, memory := newTestLibrary(t)
	check(t, library.Import(testCatalog()))
	entries := library.Entries()
	if len(entries) != 1 || entries[0].Enabled {
		t.Fatalf("imported entries = %+v", entries)
	}
	if library.Title().Name != "Synthetic" {
		t.Fatalf("library title = %+v", library.Title())
	}

	check(t, library.SetEnabled("skip-auth", true))
	got := make([]byte, 6)
	check(t, memory.ReadMemory(testMemoryBase, got))
	if !bytes.Equal(got, []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}) {
		t.Fatalf("memory after enable = %x", got)
	}
	if entries := library.Entries(); !entries[0].Enabled {
		t.Fatalf("entries after enable = %+v", entries)
	}

	check(t, library.SetEnabled("skip-auth", false))
	check(t, memory.ReadMemory(testMemoryBase, got))
	if !bytes.Equal(got, []byte{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("memory after disable = %x", got)
	}
}

func TestLibraryRollsBackAPartiallyAppliedCheat(t *testing.T) {
	t.Parallel()
	library, memory := newTestLibrary(t)
	check(t, library.Import(testCatalog()))
	// Break the second patch's expected original after import so enabling the
	// cheat fails halfway through.
	check(t, memory.WriteMemory(testMemoryBase+4, []byte{0x77, 0x88}))

	err := library.SetEnabled("skip-auth", true)
	if !errors.Is(err, ErrUnexpectedOriginal) {
		t.Fatalf("partial enable error = %v", err)
	}
	got := make([]byte, 4)
	check(t, memory.ReadMemory(testMemoryBase, got))
	if !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("memory after rollback = %x, want the original bytes", got)
	}
	if entries := library.Entries(); entries[0].Enabled {
		t.Fatalf("entries after failed enable = %+v", entries)
	}
}

func TestLibraryRejectsACatalogForAnotherTitle(t *testing.T) {
	t.Parallel()
	library, _ := newTestLibrary(t)
	catalog := testCatalog()
	catalog.Title.ImageSHA256 = strings.Repeat("cd", 32)
	if err := library.Import(catalog); !errors.Is(err, ErrWrongTarget) {
		t.Fatalf("import for another title = %v", err)
	}
	if entries := library.Entries(); len(entries) != 0 {
		t.Fatalf("entries after rejected import = %+v", entries)
	}
}

func TestLibraryImportReplacesThePreviousCatalog(t *testing.T) {
	t.Parallel()
	library, memory := newTestLibrary(t)
	check(t, library.Import(testCatalog()))
	check(t, library.SetEnabled("skip-auth", true))

	replacement := testCatalog()
	replacement.Cheats[0].ID = "infinite-gold"
	replacement.Cheats[0].Name = "Infinite gold"
	check(t, library.Import(replacement))
	entries := library.Entries()
	if len(entries) != 1 || entries[0].Cheat.ID != "infinite-gold" {
		t.Fatalf("entries after replacement = %+v", entries)
	}
	got := make([]byte, 6)
	check(t, memory.ReadMemory(testMemoryBase, got))
	if !bytes.Equal(got, []byte{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("memory after replacement = %x, want the original bytes", got)
	}
	if _, ok := library.Entry("skip-auth"); ok {
		t.Fatal("the replaced cheat is still registered")
	}
	if err := library.SetEnabled("skip-auth", true); !errors.Is(err, ErrCheatNotFound) {
		t.Fatalf("enable of a removed cheat = %v", err)
	}
}

func defaultOnCatalog() Catalog {
	catalog := testCatalog()
	catalog.Cheats[0].DefaultEnabled = true
	catalog.Cheats = append(catalog.Cheats, Cheat{
		ID:               "extra-gold",
		Name:             "Extra gold",
		RestoreOnDisable: true,
		Patches: []Patch{{
			Address:  Address(testMemoryBase + 6),
			Value:    Bytes{0x11},
			Expected: Bytes{7},
		}},
	})
	return catalog
}

func TestApplyStateTurnsOnDefaultsAndLeavesTheRest(t *testing.T) {
	t.Parallel()
	library, memory := newTestLibrary(t)
	check(t, library.Import(defaultOnCatalog()))
	if got := library.Defaults(); len(got) != 1 || got[0] != "skip-auth" {
		t.Fatalf("defaults = %v", got)
	}
	// Import alone changes nothing; the host decides when state is applied.
	if entries := library.Entries(); entries[0].Enabled {
		t.Fatalf("import enabled a cheat on its own: %+v", entries)
	}

	check(t, library.ApplyState(nil))
	got := make([]byte, 7)
	check(t, memory.ReadMemory(testMemoryBase, got))
	if !bytes.Equal(got, []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 7}) {
		t.Fatalf("memory after defaults = %x", got)
	}
	entries := library.Entries()
	if !entries[0].Enabled || entries[1].Enabled {
		t.Fatalf("entries after defaults = %+v", entries)
	}
}

func TestApplyStateLetsAChoiceOverrideTheDefault(t *testing.T) {
	t.Parallel()
	library, memory := newTestLibrary(t)
	check(t, library.Import(defaultOnCatalog()))
	// Someone turned the default-on cheat off and the other one on.
	if err := library.ApplyState(map[string]bool{
		"skip-auth":  false,
		"extra-gold": true,
	}); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 7)
	check(t, memory.ReadMemory(testMemoryBase, got))
	if !bytes.Equal(got, []byte{1, 2, 3, 4, 5, 6, 0x11}) {
		t.Fatalf("memory after overrides = %x", got)
	}
	entries := library.Entries()
	if entries[0].Enabled || !entries[1].Enabled {
		t.Fatalf("entries after overrides = %+v", entries)
	}
}

func TestApplyStateReportsAFailureWithoutDroppingTheRest(t *testing.T) {
	t.Parallel()
	library, memory := newTestLibrary(t)
	check(t, library.Import(defaultOnCatalog()))
	// Break the default-on cheat's expected original.
	check(t, memory.WriteMemory(testMemoryBase, []byte{0x99}))

	err := library.ApplyState(map[string]bool{"extra-gold": true})
	if !errors.Is(err, ErrUnexpectedOriginal) {
		t.Fatalf("apply error = %v", err)
	}
	entries := library.Entries()
	if entries[0].Enabled {
		t.Fatalf("a failed default stayed enabled: %+v", entries)
	}
	if !entries[1].Enabled {
		t.Fatalf("one failure dropped the other cheat: %+v", entries)
	}
}

func TestDefaultEnabledRequiresRestoreOnDisable(t *testing.T) {
	t.Parallel()
	catalog := testCatalog()
	catalog.Cheats[0].DefaultEnabled = true
	catalog.Cheats[0].RestoreOnDisable = false
	if err := catalog.Validate(); err == nil {
		t.Fatal("a default-on cheat without restore_on_disable was accepted")
	}
}
