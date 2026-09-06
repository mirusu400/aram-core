package ktf

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

// TestKTFKernelExitRequestsTermination covers issue #53: MC_knlExit (kernel
// interface slot 7 / offset 0x1c) must tear the Clet down. 에픽크로니클PE calls
// it from its game-loop timer after the first-run "restart required" notice; a
// no-op left the timer armed and the handset appeared frozen.
func TestKTFKernelExitRequestsTermination(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, 0))
	if runtime.terminationRequested {
		t.Fatal("termination requested before MC_knlExit")
	}
	if _, err := ktfKernelExit(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if !runtime.terminationRequested {
		t.Fatal("MC_knlExit did not request termination")
	}
	if runtime.CanAwaitEvents() {
		t.Fatal("runtime still awaits events after MC_knlExit")
	}
}

// TestKTFFindResourceFallsBackToPrivateFile covers the second half of issue
// #53: a Clet's own MC_fsWrite output must be visible again through
// MC_knlGetResource so the relaunch can reload its saved calibration and skip
// the notice. Bundled jar resources still take precedence.
func TestKTFFindResourceFallsBackToPrivateFile(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	if runtime.Pkg.Resources == nil {
		runtime.Pkg.Resources = map[string][]byte{}
	}

	// A save the Clet persisted through MC_fs* lands in NamespacePrivate.
	check(t, runtime.Services.Storage.WriteFile(
		shared.NamespacePrivate, "/gopt.sav", []byte{0x20, 0x00, 0x00, 0x00},
	))
	data, ok := runtime.findKTFResource("gopt.sav")
	if !ok {
		t.Fatal("findKTFResource did not surface the private save")
	}
	if len(data) != 4 || data[0] != 0x20 {
		t.Fatalf("private save contents = %x", data)
	}

	// A bundled jar resource of the same name must win over a private file.
	runtime.Pkg.Resources["dup.dat"] = []byte("jar")
	check(t, runtime.Services.Storage.WriteFile(
		shared.NamespacePrivate, "/dup.dat", []byte("private"),
	))
	dup, ok := runtime.findKTFResource("dup.dat")
	if !ok || string(dup) != "jar" {
		t.Fatalf("bundled resource precedence broken: ok=%v data=%q", ok, dup)
	}
}
