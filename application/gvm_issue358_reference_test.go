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
	// The title paces itself from its guest timer; its title menu is up by
	// frame 400.
	for frame := 0; frame < 480; frame++ {
		if frame == 400 {
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
		if frame == 399 {
			before = fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer()))
		}
		if frame == 479 {
			after = fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer()))
		}
	}
	if got, want := machine.Framebuffer().Bounds().Size(), image.Pt(int(operationalGVMWidth), int(operationalGVMHeight)); got != want {
		t.Fatalf("canvas = %v, want %v", got, want)
	}
	const initialHash = "a933ddb31c7ad13f3a8eb920c27fafbb73b7c6e70076501726fda9de8a44c97b"
	const selectedHash = "6644aaca6ab9f3901e5c8f326ce91caff509730e4d48fac5f25c709f4eb51cbc"
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
