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

// Issue #77, 무한신맞고2009 and 검은방3: both draw all their text through
// ordinal 218 and both pass an M_Char byte run, not UCS-2 — 검은방3's model
// string arrives as the ASCII bytes "IM-S220L". Reading those as UTF-16 drew
// mojibake, so 218 is MC_grpDrawString and the raptor +1 divergence from the
// public catalog starts before it rather than at MC_grpFlushLcd. That also
// closes the hole the old mapping left at 221.
func TestRaptorTextOrdinalsFollowTheDivergedGraphicsBlock(t *testing.T) {
	expected := map[uint32]string{
		213: "MC_grpDrawImage",
		218: "MC_grpDrawString",
		219: "MC_grpDrawUnicodeString",
		220: "MC_grpGetRGBPixels",
		221: "MC_grpSetRGBPixels",
		222: "MC_grpFlushLcd",
		231: "MC_grpGetStringWidth",
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
