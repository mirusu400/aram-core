package application

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

func TestJ2MEIssue360DoUsesDeclaredHandsetCanvas(t *testing.T) {
	const digest = "b7b2e9579545bf8e0ae06c48b57ad05187dcbcb423a4838ef811edf51df58b52"
	path, data := findAuthorizedPackage(t, digest)
	ctx := context.Background()
	machine, err := NewFactory().Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	if got, want := machine.Framebuffer().Bounds().Size(), image.Pt(176, 220); got != want {
		t.Fatalf("handset canvas = %v, want %v", got, want)
	}
	if err := machine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 1500; frame++ {
		if frame == 1200 {
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
		if frame == 599 {
			const titleHash = "dae2533e0d4b23c6b6b9eee1d40e71d49aa3d921077993a9373cfec17bd7f525"
			if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != titleHash {
				t.Fatalf("title hash = %s, want %s", got, titleHash)
			}
		}
	}
	const menuHash = "e86c0bca5673157fabfbe9cb136f4da3fbad4e046603d0e1bae652084aef8a71"
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != menuHash {
		t.Fatalf("menu hash = %s, want %s", got, menuHash)
	}
}
