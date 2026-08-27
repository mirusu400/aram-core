package raptor

import "testing"

func TestRaptorObservedImports(t *testing.T) {
	if name, ok := raptorWIPIImportName(126); !ok || name != "MC_knlGetSystemProperty" {
		t.Fatalf("Raptor import 126 = %q, %t", name, ok)
	}
	for ordinal, want := range map[uint32]string{
		1233: "MC_mdaSetMuteState",
		1400: "MC_miscBackLight",
	} {
		name, handled := raptorWIPIImportName(ordinal)
		if !handled || name != want {
			t.Errorf("Raptor import %d = %q, handled=%t", ordinal, name, handled)
		}
	}
}
