package wipi

import (
	"encoding/binary"
	"testing"
)

func TestLayoutBuildsRecoveredImportChain(t *testing.T) {
	layout, err := NewLayout()
	if err != nil {
		t.Fatal(err)
	}
	read := func(address uint32) uint32 {
		t.Helper()
		return binary.LittleEndian.Uint32(layout.System[address-SystemBase:])
	}
	if got := read(ImportPointerAddress); got != ProcessHolderAddress {
		t.Fatalf("*0x%08x = 0x%08x", ImportPointerAddress, got)
	}
	if got := read(ProcessHolderAddress); got != ImportRootAddress {
		t.Fatalf("*0x%08x = 0x%08x", ProcessHolderAddress, got)
	}
	for family, field := range RootFields() {
		table := layout.PackageByFamily[family]
		if got := read(ImportRootAddress + field); got != table {
			t.Errorf("%s root = 0x%08x, want 0x%08x", family, got, table)
		}
	}

	api, _ := Lookup("MC_grpFlushLcd")
	table := layout.PackageByFamily[api.Family]
	stub := layout.StubByName[api.Name]
	if got := read(table + api.Slot); got != stub {
		t.Fatalf("%s slot = 0x%08x, want 0x%08x", api.Name, got, stub)
	}
	if stub&1 == 0 {
		t.Fatalf("%s stub 0x%08x is not Thumb", api.Name, stub)
	}
	decoded, ok := layout.APIByStub[stub&^1]
	if !ok || decoded != api {
		t.Fatalf("stub reverse lookup = %+v, %v", decoded, ok)
	}
	offset := (stub &^ 1) - TrampolineBase
	if got := layout.Trampolines[offset : offset+4]; string(got) != string([]byte{0x00, 0xbe, 0x00, 0xbf}) {
		t.Fatalf("stub bytes = %x", got)
	}
}

func TestLayoutIncludesProviderTablesWithoutInventingRootFields(t *testing.T) {
	layout, err := NewLayout()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"MC_DB", "MC_MISC", "MC_PHN", "MC_SRL"} {
		if layout.PackageByFamily[family] == 0 {
			t.Errorf("%s provider table is absent", family)
		}
		if _, ok := RootFields()[family]; ok {
			t.Errorf("%s was invented as a public import-root field", family)
		}
	}
}
