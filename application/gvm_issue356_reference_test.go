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

func TestGVMIssue356JjayoTycoonContinuesPastReportedFault(t *testing.T) {
	path, data := findAuthorizedPackage(t, GVMOperationalSHA256)
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
	step := func(count int) {
		t.Helper()
		for i := 0; i < count; i++ {
			if err := machine.StepFrame(ctx); err != nil {
				t.Fatalf("frame %d: %v", i, err)
			}
		}
	}
	hash := func() string {
		t.Helper()
		frame := machine.Framebuffer()
		if frame == nil || frame.Bounds().Size() != image.Pt(120, 80) {
			t.Fatalf("frame bounds = %v", frame)
		}
		return fmt.Sprintf("%x", brewFrameHash(frame))
	}
	step(200)
	for i := 0; i < 11; i++ {
		for _, pressed := range []bool{true, false} {
			if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: pressed}); err != nil {
				t.Fatalf("select %d pressed=%t: %v", i+1, pressed, err)
			}
		}
		step(10)
		if i == 1 {
			const reportedFrame = "a16e4c1bd07244acf0b2ab40b750c5078b99fbdb6c471fe51b629cd031dd87b7"
			if got := hash(); got != reportedFrame {
				t.Fatalf("pre-fault frame = %s, want %s", got, reportedFrame)
			}
		}
	}
	const cowSelectionFrame = "498fb1f07b72164a341cbaa877ea7aa51fe17dbd1c7b4069305470326908c9ff"
	if got := hash(); got != cowSelectionFrame {
		t.Fatalf("cow selection frame = %s, want %s", got, cowSelectionFrame)
	}
	diagnostics := machine.(interface {
		GVMInputDispatchDiagnostics() GVMInputDispatchDiagnostics
	}).GVMInputDispatchDiagnostics()
	if diagnostics.DispatchCount != 11 || diagnostics.GuestCode != 20 {
		t.Fatalf("select dispatch = %+v", diagnostics)
	}
}
