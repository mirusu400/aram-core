package wipi

import (
	"context"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// A CSTDLIB call that fails on a bad argument has to say which arguments it
// was given and where the guest called from. 부루마불 2009 reached memcpy with a
// 64 MiB length (issue #139): the refused size alone gave a reader nothing to
// chase, since the length is computed somewhere else entirely.
func TestCStdlibErrorNamesArgumentsAndCaller(t *testing.T) {
	runtime := newPublicRuntime(t)
	const link = uint32(0x0110a023)
	for index, value := range []uint32{0x00030000, 0x00040000, 0x04010002, 7} {
		if err := runtime.CPU.WriteRegister(uint32(index), value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterLR, link); err != nil {
		t.Fatal(err)
	}
	stub, ok := runtime.Layout.StubByName["memcpy"]
	if !ok {
		t.Fatal("memcpy has no stub")
	}
	_, err := runtime.dispatchTrap(context.Background(), stub&^1)
	if err == nil {
		t.Fatal("memcpy of 64 MiB was accepted")
	}
	message := err.Error()
	for _, want := range []string{
		"CSTDLIB.memcpy",
		"0x00030000",
		"0x00040000",
		"0x04010002",
		"lr 0x0110a023",
		"guest transfer size 67174402",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not name %q", message, want)
		}
	}
}
