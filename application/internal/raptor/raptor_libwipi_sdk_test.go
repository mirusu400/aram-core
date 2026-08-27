package raptor

import "testing"

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
