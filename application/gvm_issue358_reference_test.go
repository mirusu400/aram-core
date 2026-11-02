package application

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader"
)

func TestGVMIssue358RagnarokStartsAndAcceptsSelect(t *testing.T) {
	path, data := findAuthorizedPackage(t, GVMRagnarokOperationalSHA256)
	ctx := context.Background()
	machine, err := NewFactory().Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
		Format: string(loader.KindJava),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	if err := machine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var before, after string
	for frame := 0; frame < 120; frame++ {
		if frame == 80 {
			for _, event := range []machinecore.InputEvent{
				{Control: "select", Pressed: true},
				{Control: "select", Pressed: false},
			} {
				if err := machine.QueueInput(event); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := machine.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if frame == 79 {
			before = fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer()))
		}
		if frame == 119 {
			after = fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer()))
		}
	}
	if got, want := machine.Framebuffer().Bounds().Size(), image.Pt(int(operationalGVMWidth), int(operationalGVMHeight)); got != want {
		t.Fatalf("canvas = %v, want %v", got, want)
	}
	const initialHash = "0e1b4a7a70415173c3b92679fe2d8b0d8b53bc55739c931bdd55baa86da6e621"
	const selectedHash = "baa20aeb2369c06e31cdf133f8574df6d20a132d7f1e3e84013f06e4c7357897"
	if before != initialHash || after != selectedHash {
		t.Fatalf("initial/selected frame hashes = %s/%s, want %s/%s", before, after, initialHash, selectedHash)
	}
	diagnostics := machine.(interface {
		GVMInputDispatchDiagnostics() GVMInputDispatchDiagnostics
	}).GVMInputDispatchDiagnostics()
	if diagnostics.DispatchCount != 1 || diagnostics.GuestCode != 20 {
		t.Fatalf("select dispatch = %+v", diagnostics)
	}
}
