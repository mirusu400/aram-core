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

// Continue without a save passes null to ByteArrayInputStream, then handles the
// guest exception and displays the game's own missing-save message.
func TestJ2MEIssue359MissingSaveShowsGuestWarning(t *testing.T) {
	const digest = "5e87f5b649860a6c3e80f794882e5dc2e57a96c268b1e479af7af101d17e0160"
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
	if got, want := machine.Framebuffer().Bounds().Size(), image.Pt(120, 160); got != want {
		t.Fatalf("handset canvas = %v, want %v", got, want)
	}
	if err := machine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 1800; frame++ {
		if frame == 600 || frame == 1000 || frame == 1500 || frame == 1600 {
			control := "select"
			if frame == 1500 {
				control = "down"
			}
			for _, event := range []machinecore.InputEvent{
				{Control: control, Pressed: true},
				{Control: control, Pressed: false},
			} {
				if err := machine.QueueInput(event); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := machine.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	const warningHash = "5e1c9bccddc77879cafad9c452529afd3bd2a7d9081acf163180cb2b9a0bbaad"
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != warningHash {
		t.Fatalf("missing-save frame hash = %s, want %s", got, warningHash)
	}
}
