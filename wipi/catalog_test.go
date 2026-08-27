package wipi

import "testing"

func TestCatalogMatchesRecoveredPublicSurface(t *testing.T) {
	if got := len(APIs()); got != 239 {
		t.Fatalf("API count = %d, want 239", got)
	}
	wantCounts := map[string]int{
		"CSTDLIB": 31,
		"MC_DB":   13,
		"MC_FS":   17,
		"MC_GRP":  39,
		"MC_HTTP": 15,
		"MC_KNL":  30,
		"MC_MDA":  21,
		"MC_MISC": 4,
		"MC_NET":  15,
		"MC_PHN":  1,
		"MC_SRL":  6,
		"MC_UIC":  41,
		"MC_UTIL": 6,
	}
	gotCounts := FamilyCounts()
	if len(gotCounts) != len(wantCounts) {
		t.Fatalf("family count = %d, want %d", len(gotCounts), len(wantCounts))
	}
	for family, want := range wantCounts {
		if got := gotCounts[family]; got != want {
			t.Errorf("%s count = %d, want %d", family, got, want)
		}
	}

	var confirmed, candidates int
	for index, api := range APIs() {
		if api.Ordinal != index+1 {
			t.Fatalf("API %d ordinal = %d", index, api.Ordinal)
		}
		switch api.SelectorState {
		case "confirmed_firmware_selector":
			confirmed++
		case "candidate_selector":
			candidates++
		default:
			t.Fatalf("%s has unknown selector state %q", api.Name, api.SelectorState)
		}
	}
	if confirmed != 229 || candidates != 10 {
		t.Fatalf("selector evidence = %d confirmed, %d candidate", confirmed, candidates)
	}
}

func TestCatalogLookupAndResolve(t *testing.T) {
	api, ok := Lookup("MC_grpFlushLcd")
	if !ok {
		t.Fatal("MC_grpFlushLcd is absent")
	}
	if api.Family != "MC_GRP" || api.Slot != 0x54 {
		t.Fatalf("MC_grpFlushLcd = %+v", api)
	}
	resolved, ok := Resolve("MC_GRP", 0x54)
	if !ok || resolved != api {
		t.Fatalf("Resolve(MC_GRP, 0x54) = %+v, %v", resolved, ok)
	}
	if _, ok := Resolve("MC_GRP", 0x5f); ok {
		t.Fatal("misaligned selector resolved")
	}
	byOrdinal, ok := LookupOrdinal(46)
	if !ok || byOrdinal.Name != "MC_fsOpen" {
		t.Fatalf("LookupOrdinal(46) = %+v, %v", byOrdinal, ok)
	}
	for _, ordinal := range []int{0, -1, 240} {
		if api, found := LookupOrdinal(ordinal); found {
			t.Errorf("LookupOrdinal(%d) = %+v, true", ordinal, api)
		}
	}

	all := APIs()
	all[0].Name = "mutated"
	if original, _ := Lookup("MC_srlClose"); original.Name != "MC_srlClose" {
		t.Fatal("caller mutated catalog state")
	}
}
