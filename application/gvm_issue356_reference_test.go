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
		if frame == nil || frame.Bounds().Size() != image.Pt(int(operationalGVMWidth), int(operationalGVMHeight)) {
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
			const reportedFrame = "715769e157ca163315ccdc15d1a68c65cc2d693276625f26eed66cd23714bc83"
			if got := hash(); got != reportedFrame {
				t.Fatalf("pre-fault frame = %s, want %s", got, reportedFrame)
			}
		}
	}
	const cowSelectionFrame = "45fa9e6413eae0e968f8cdaee033a95d4801f3be64e01d12c5bbd0bd7493a9aa"
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
