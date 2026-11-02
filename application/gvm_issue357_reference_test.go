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

func TestGVMIssue357HackSignMenuStoryAndSelection(t *testing.T) {
	path, data := findAuthorizedPackage(t, GVMHackSignOperationalSHA256)
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
	step := func(frames int) {
		t.Helper()
		for i := 0; i < frames; i++ {
			if err := machine.StepFrame(ctx); err != nil {
				t.Fatalf("frame %d: %v", i, err)
			}
		}
	}
	press := func(key string, frames int) {
		t.Helper()
		for _, pressed := range []bool{true, false} {
			if err := machine.QueueInput(machinecore.InputEvent{Control: key, Pressed: pressed}); err != nil {
				t.Fatalf("%s pressed=%t: %v", key, pressed, err)
			}
		}
		step(frames)
	}
	hash := func() string {
		t.Helper()
		frame := machine.Framebuffer()
		if frame == nil || frame.Bounds().Size() != image.Pt(int(operationalGVMWidth), int(operationalGVMHeight)) {
			t.Fatalf("frame bounds = %v", frame)
		}
		return fmt.Sprintf("%x", brewFrameHash(frame))
	}
	step(120)
	press("select", 10)
	press("up", 10)
	const menuHash = "8ff1fe95c753aad04f4d3623046f537d52aac8c67d0d1fb7f69fe2d1a2041833"
	if got := hash(); got != menuHash {
		t.Fatalf("menu frame = %s, want %s", got, menuHash)
	}
	for i := 0; i < 58; i++ {
		press("select", 2)
	}
	const selectionHash = "d25f28af318f76b55f7362c3b45068d7f92889f50afd2f8b7082f307fa82696c"
	if got := hash(); got != selectionHash {
		t.Fatalf("post-story selection frame = %s, want %s", got, selectionHash)
	}
	press("down", 2)
	const movedHash = "db02a1766be69e953b79f400da2143f84a8817aab8690d7c42bd65703d5058b9"
	if got := hash(); got != movedHash {
		t.Fatalf("moved selection frame = %s, want %s", got, movedHash)
	}
	press("select", 2)
	if got := hash(); got != selectionHash {
		t.Fatalf("selected frame = %s, want %s", got, selectionHash)
	}
	diagnostics := machine.(interface {
		GVMInputDispatchDiagnostics() GVMInputDispatchDiagnostics
	}).GVMInputDispatchDiagnostics()
	if diagnostics.DispatchCount != 62 || diagnostics.GuestCode != 20 {
		t.Fatalf("input dispatch = %+v", diagnostics)
	}
}
