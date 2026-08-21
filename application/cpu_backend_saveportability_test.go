package application

import (
	"bytes"
	"context"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// renamedCPU is the interpreter under a different backend identity but the same
// architecture and context format — a stand-in for a fast backend, so we can
// test that a save is portable across backends without shipping a second core.
type renamedCPU struct {
	*interpreter.Backend
}

func (renamedCPU) Identity() cpu.Identity {
	return cpu.Identity{Name: "renamed-fast", Version: "1", Architecture: cpu.ARMv5TE}
}

// TestSaveStatePortableAcrossSameArchBackends proves the interface leak is
// fixed: a state saved under the precise interpreter loads under a
// differently-named backend of the same architecture. Restore now gates on
// architecture (and the backend's own RestoreContext), not on the backend name,
// so cores can be switched on an existing save.
func TestSaveStatePortableAcrossSameArchBackends(t *testing.T) {
	saver := newSyntheticMachine(t)
	if err := saver.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := saver.cpu.WriteRegister(cpu.RegisterR0, 0x1234); err != nil {
		t.Fatal(err)
	}
	var saved bytes.Buffer
	if err := saver.SaveState(&saved); err != nil {
		t.Fatal(err)
	}

	factory := NewFactory()
	factory.NewCPU = func() cpu.Backend { return renamedCPU{interpreter.New()} }
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     "synthetic.dat",
		ReaderAt: bytes.NewReader(syntheticEADS()),
		Size:     int64(len(syntheticEADS())),
	})
	if err != nil {
		t.Fatal(err)
	}
	loader := created.(*Machine)
	t.Cleanup(func() { _ = loader.Close() })
	if err := loader.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := loader.cpu.Identity().Name; got == interpreter.BackendName {
		t.Fatalf("loader backend was not renamed: %q", got)
	}
	if err := loader.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatalf("cross-backend restore failed: %v", err)
	}
	if got := register(t, loader, cpu.RegisterR0); got != 0x1234 {
		t.Fatalf("restored r0 = 0x%x, want 0x1234", got)
	}
}
