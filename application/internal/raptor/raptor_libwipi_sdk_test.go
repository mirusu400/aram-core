package raptor

import (
	"testing"

	"github.com/mirusu400/aram-core/wipi"
)

func TestLibwipiSDKExampleImportsResolveToPublicAPIs(t *testing.T) {
	expected := map[uint32]string{
		107:  "MC_knlExit",
		208:  "MC_grpPutPixel",
		224:  "MC_grpGetRGBFromPixel",
		228:  "MC_grpGetFontHeight",
		229:  "MC_grpGetFontAscent",
		230:  "MC_grpGetFontDescent",
		231:  "MC_grpGetStringWidth",
		1208: "MC_mdaClipGetVolume",
	}
	for ordinal, want := range expected {
		got, ok := raptorWIPIImportName(ordinal)
		if !ok || got != want {
			t.Errorf(
				"Raptor import %d = %q, %v; want %q, true",
				ordinal,
				got,
				ok,
				want,
			)
		}
	}
}

func TestLibwipiARAMSyntheticImportsCoverOnlyModeledLabFamilies(t *testing.T) {
	for _, api := range wipi.APIs() {
		ordinal := libwipiARAMImportBase + uint32(api.Ordinal)
		got, ok := raptorWIPIImportName(ordinal)
		wantMapped := api.Family == "MC_FS" || api.Family == "MC_DB" ||
			api.Family == "MC_MDA"
		if wantMapped {
			if !ok || got != api.Name {
				t.Errorf("ARAM import %#x = %q, %v; want %q, true", ordinal, got, ok, api.Name)
			}
			continue
		}
		if ok || got != "" {
			t.Errorf("uncontracted ARAM import %#x = %q, %v", ordinal, got, ok)
		}
	}
	for _, ordinal := range []uint32{
		libwipiARAMImportBase,
		libwipiARAMImportBase + 240,
		0xffff,
	} {
		if got, ok := raptorWIPIImportName(ordinal); ok || got != "" {
			t.Errorf("out-of-range ARAM import %#x = %q, %v", ordinal, got, ok)
		}
	}
}
