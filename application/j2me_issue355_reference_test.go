package application

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

func TestJ2MEIssue355GradiusNeoPassesLoadingIntoMenu(t *testing.T) {
	const digest = "70d03766a9c6ed734d0332ee58be90dc4ece0a2f265ef4fbd6271751dc4c4459"
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
	if err := machine.Start(ctx); err != nil {
		t.Fatal(err)
	}

	for frame := 0; frame <= 930; frame++ {
		if control, ok := map[int]string{600: "select", 605: "select", 900: "num5", 905: "num5"}[frame]; ok {
			if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: frame == 600 || frame == 900}); err != nil {
				t.Fatalf("frame %d input: %v", frame, err)
			}
		}
		if err := machine.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	// The JAD declares MIDletX-LCD-Size: 176,200; the menu is the same
	// six-item KONAMI menu the 240x320 canvas showed, laid out for 176x200.
	const menuFrame = "14fddb91c0f2099d5860d38dca680cb8b86485a93a1e71d87a5bf6194fcb89f7"
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != menuFrame {
		t.Fatalf("main menu frame = %s, want %s", got, menuFrame)
	}
}
