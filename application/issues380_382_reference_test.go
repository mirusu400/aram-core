package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

func TestIssue380RhythmStarUsesBundledHandsetProfile(t *testing.T) {
	path, data := findAuthorizedPackage(t, rhythmStar1RaptorSHA256)
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	assertHandsetProfile := func(run string) {
		if err := machine.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		for frame := 0; frame < 600; frame++ {
			if err := machine.StepFrame(context.Background()); err != nil {
				t.Fatalf("%s frame %d: %v", run, frame, err)
			}
		}
		properties := make(map[string]string)
		for _, call := range machine.raptor.ImportTrace {
			if call.Ordinal != 126 {
				continue
			}
			name, err := machine.wipi.ReadCString(call.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			value, err := machine.wipi.ReadCString(call.Args[1])
			if err != nil {
				t.Fatal(err)
			}
			properties[string(name)] = string(value)
		}
		if got := properties["PHONEMODEL"]; got != "CANU801EX" {
			t.Fatalf("%s PHONEMODEL = %q, want CANU801EX (properties=%v)", run, got, properties)
		}
	}
	assertHandsetProfile("initial run")
	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertHandsetProfile("reset run")
}

func TestIssues381And382ZenoniaUseFullPrimaryFramebuffer(t *testing.T) {
	for _, test := range []struct {
		name   string
		digest string
	}{
		{name: "issue 381 Zenonia 1", digest: zenonia1RaptorSHA256},
		{name: "issue 382 Zenonia 2", digest: zenonia2RaptorSHA256},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, data := findAuthorizedPackage(t, test.digest)
			created, err := NewFactory().Create(context.Background(), machinecore.Source{
				Name: filepath.Base(path), Path: path,
				ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
			})
			if err != nil {
				t.Fatal(err)
			}
			machine, ok := created.(*Machine)
			if !ok || machine.raptor == nil {
				t.Fatalf("machine type = %T, want Raptor", created)
			}
			t.Cleanup(func() { _ = machine.Close() })
			handle, err := machine.wipi.EnsureScreenFramebuffer()
			if err != nil {
				t.Fatal(err)
			}
			if err := machine.cpu.WriteRegister(cpu.RegisterR0, handle); err != nil {
				t.Fatal(err)
			}
			result, name, handled, err := machine.raptor.DispatchPrivateImport(52)
			if err != nil || !handled || name != "RAPTOR.grpGetFrameBufferHeight" {
				t.Fatalf("height import: handled=%t name=%q err=%v", handled, name, err)
			}
			if result.Low != 320 {
				t.Fatalf("primary framebuffer height = %d, want 320", result.Low)
			}
		})
	}
}
