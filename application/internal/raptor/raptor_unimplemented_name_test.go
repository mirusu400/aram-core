package raptor

import "testing"

// TestRaptorUnimplementedImportNameIsInterned pins the label an unimplemented
// import is recorded under, and that it is built once per module/ordinal pair.
// 붕어빵타이쿤3 issues around a hundred and fifty imports a frame and almost
// all of them are unimplemented, so formatting the label on every call was a
// tenth of its frame (issue #75).
func TestRaptorUnimplementedImportNameIsInterned(t *testing.T) {
	runtime := &Runtime{}
	key := raptorImportKey{Module: 100, Ordinal: 84}
	first := runtime.unimplementedImportName(key)
	if first != "RAPTOR.module100#84" {
		t.Fatalf("name = %q, want %q", first, "RAPTOR.module100#84")
	}
	second := runtime.unimplementedImportName(key)
	if second != first {
		t.Fatalf("second name = %q, want %q", second, first)
	}
	if len(runtime.unimplementedNames) != 1 {
		t.Fatalf("intern table holds %d entries, want 1", len(runtime.unimplementedNames))
	}
	other := runtime.unimplementedImportName(raptorImportKey{Module: 100, Ordinal: 85})
	if other != "RAPTOR.module100#85" {
		t.Fatalf("second key name = %q, want %q", other, "RAPTOR.module100#85")
	}
	if len(runtime.unimplementedNames) != 2 {
		t.Fatalf("intern table holds %d entries, want 2", len(runtime.unimplementedNames))
	}
}

// TestRaptorUnimplementedImportNameStopsInterning keeps a runaway import table
// from growing the intern map without limit; past the cap the label is still
// correct, it just costs a formatting.
func TestRaptorUnimplementedImportNameStopsInterning(t *testing.T) {
	runtime := &Runtime{}
	for ordinal := uint32(0); ordinal < maxRaptorUnimplementedNames; ordinal++ {
		runtime.unimplementedImportName(raptorImportKey{Ordinal: ordinal})
	}
	if len(runtime.unimplementedNames) != maxRaptorUnimplementedNames {
		t.Fatalf("intern table holds %d entries, want %d",
			len(runtime.unimplementedNames), maxRaptorUnimplementedNames)
	}
	beyond := runtime.unimplementedImportName(raptorImportKey{Module: 7, Ordinal: 999999})
	if beyond != "RAPTOR.module7#999999" {
		t.Fatalf("name past the cap = %q", beyond)
	}
	if len(runtime.unimplementedNames) != maxRaptorUnimplementedNames {
		t.Fatalf("intern table grew past the cap to %d entries",
			len(runtime.unimplementedNames))
	}
}
