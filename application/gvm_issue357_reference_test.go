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
		if frame == nil || frame.Bounds().Size() != image.Pt(120, 80) {
			t.Fatalf("frame bounds = %v", frame)
		}
		return fmt.Sprintf("%x", brewFrameHash(frame))
	}
	step(120)
	press("select", 10)
	press("up", 10)
	const menuHash = "68ce3bfb959151bbc0e1bfce8409743fb81ff5f7b32c1b79f3fc34c3f969f84e"
	if got := hash(); got != menuHash {
		t.Fatalf("menu frame = %s, want %s", got, menuHash)
	}
	for i := 0; i < 58; i++ {
		press("select", 2)
	}
	const selectionHash = "1cd895b773089753ebf7446f555fa83dacbaf6f774e3788c46c2a0af98efc751"
	if got := hash(); got != selectionHash {
		t.Fatalf("post-story selection frame = %s, want %s", got, selectionHash)
	}
	press("down", 2)
	const movedHash = "00944a04b1ede07b5f71e44a1d23f95adcfeb6224714f2050e8e54cb4620a38b"
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
