package systemmachine

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/samsung"
)

func TestCompatibleCPUContextIdentityAllowsInterpreterTiers(t *testing.T) {
	precise := interpreter.New().Identity()
	jit := interpreter.NewJIT().Identity()
	jitLoops := interpreter.NewJITWithOptions(interpreter.JITOptions{LoopAcceleration: true}).Identity()
	if !compatibleCPUContextIdentity(precise, jit) ||
		!compatibleCPUContextIdentity(jit, precise) ||
		!compatibleCPUContextIdentity(precise, jitLoops) ||
		!compatibleCPUContextIdentity(jitLoops, jit) {
		t.Fatalf(
			"interpreter tier contexts are not portable: precise=%+v jit=%+v jit-loops=%+v",
			precise, jit, jitLoops,
		)
	}
	if compatibleCPUContextIdentity(precise, cpu.Identity{
		Name: "different-backend", Version: precise.Version, Architecture: precise.Architecture,
	}) {
		t.Fatal("unrelated backend was accepted as context-compatible")
	}
	wrongVersion := jit
	wrongVersion.Version = "different"
	if compatibleCPUContextIdentity(precise, wrongVersion) {
		t.Fatal("different interpreter context version was accepted")
	}
}

func TestSCHW830BatteryResponsesStayDL21Specific(t *testing.T) {
	dl21 := schw830BoardProfile(samsung.SCHW830DL21ProfileID)
	if len(dl21.BootControlSBIReadResponses) == 0 {
		t.Fatal("DL21 board profile has no battery SBI responses")
	}
	da18 := schw830BoardProfile(samsung.SCHW830DA18ProfileID)
	if len(da18.BootControlSBIReadResponses) != 0 {
		t.Fatalf("unevidenced DA18 battery SBI responses = %#v", da18.BootControlSBIReadResponses)
	}
}

func TestInterpreterBackendModeSelection(t *testing.T) {
	for _, test := range []struct {
		mode     CPUBackendMode
		wantName string
	}{
		{mode: "", wantName: interpreter.BackendName},
		{mode: CPUBackendPrecise, wantName: interpreter.BackendName},
		{mode: CPUBackendJIT, wantName: interpreter.BackendName + "-jit"},
		{mode: CPUBackendJITLoops, wantName: interpreter.BackendName + "-jit-loops"},
	} {
		backend, err := newInterpreterBackend(test.mode)
		if err != nil {
			t.Fatal(err)
		}
		if got := backend.Identity().Name; got != test.wantName {
			t.Fatalf("mode %q backend = %q, want %q", test.mode, got, test.wantName)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := newInterpreterBackend("unknown"); err == nil {
		t.Fatal("unknown CPU backend mode was accepted")
	}
}
