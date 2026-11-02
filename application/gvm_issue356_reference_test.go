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
	// The title menu follows the publisher logo, paced by the guest timer.
	step(420)
	// Each story page waits for the guest timer before it takes the next select.
	for i := 0; i < 20; i++ {
		for _, pressed := range []bool{true, false} {
			if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: pressed}); err != nil {
				t.Fatalf("select %d pressed=%t: %v", i+1, pressed, err)
			}
		}
		step(30)
		if i == 1 {
			const reportedFrame = "fa46774673be0628fc7531ab10020b317b06aedc4304d9400da9018faf38552d"
			if got := hash(); got != reportedFrame {
				t.Fatalf("pre-fault frame = %s, want %s", got, reportedFrame)
			}
		}
	}
	const farmFrame = "2600f7bba474d2cc3cbe5b559f228fd8144960643672b139a81610f5ff9a7174"
	if got := hash(); got != farmFrame {
		t.Fatalf("farm frame = %s, want %s", got, farmFrame)
	}
	diagnostics := machine.(interface {
		GVMInputDispatchDiagnostics() GVMInputDispatchDiagnostics
	}).GVMInputDispatchDiagnostics()
	if diagnostics.DispatchCount != 20 || diagnostics.GuestCode != 20 {
		t.Fatalf("select dispatch = %+v", diagnostics)
	}
}
