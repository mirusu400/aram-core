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

// The bundled save predates the default emulator clock by six months. On that
// clock the game replays every missed minute after the title and appears stuck.
func TestJ2MEIssue361MiniGochiEntersGameFromBundledSave(t *testing.T) {
	const digest = "54fce8ed446d8a68affee15ba328c3ecdac5dc367e54828e251c35074cdf6501"
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
	// The JAD declares MIDletX-LCD-Size: 176,200.
	if got, want := machine.Framebuffer().Bounds().Size(), image.Pt(176, 200); got != want {
		t.Fatalf("handset canvas = %v, want %v", got, want)
	}
	if err := machine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 1200; frame++ {
		if frame == 500 || frame == 1000 {
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
	}
	const gameHash = "a5e835918463e3f7579c73e44c3625b60e67428ab082cba6c1bbfa57e5f7d627"
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != gameHash {
		t.Fatalf("game frame hash = %s, want %s", got, gameHash)
	}
}
