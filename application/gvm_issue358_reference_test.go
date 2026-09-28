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
	if got, want := machine.Framebuffer().Bounds().Size(), image.Pt(120, 120); got != want {
		t.Fatalf("canvas = %v, want %v", got, want)
	}
	const initialHash = "51e4461e47c271158fc4213f32d8ac710ee6b3199b3c9ae4411d8ffd10544092"
	const selectedHash = "41351d1a20e6daba17cd25698d649ff71c62f63809c4b3306e867213e9fee163"
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
