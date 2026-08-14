package raptor

import (
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

func TestRaptorObservedImports(t *testing.T) {
	if name, ok := raptorWIPIImportName(126); !ok || name != "MC_knlGetSystemProperty" {
		t.Fatalf("Raptor import 126 = %q, %t", name, ok)
	}
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	for ordinal, want := range map[uint32]string{
		1233: "RAPTOR.privateStartup1233",
		1400: "RAPTOR.privateRuntimeInit1400",
	} {
		result, name, handled, err := runtime.DispatchPrivateImport(ordinal)
		if err != nil || !handled || name != want || result != (guest.WIPIReturn{}) {
			t.Errorf(
				"Raptor import %d = %#v, %q, handled=%t, err=%v",
				ordinal,
				result,
				name,
				handled,
				err,
			)
		}
	}
}
