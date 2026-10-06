package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Alive can request a second play on an SKT AudioClip that is still playing.
// The adapter must let that call join the current playback.
func TestSKVMIssue497RepeatedSKTAudioClipPlay(t *testing.T) {
	const digest = "38277d63b0ba2803076c82ff36f5bc1435d227d0401e4e9eedc455d56da5a68a"
	path, data := findAuthorizedPackage(t, digest)
	ctx := context.Background()
	created, err := NewFactory().Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	if err := created.Start(ctx); err != nil {
		t.Fatal(err)
	}
	events := map[int]machinecore.InputEvent{
		6: {Control: "ok", Pressed: true},
		8: {Control: "back", Pressed: true},
		10: {Control: "up", Pressed: true},
		15: {Control: "num3", Pressed: true},
		18: {Control: "send", Pressed: true},
		21: {Control: "menu", Pressed: true},
		27: {Control: "star", Pressed: true},
		28: {Control: "up", Pressed: false},
		34: {Control: "num8", Pressed: true},
		36: {Control: "num1", Pressed: true},
		43: {Control: "num6", Pressed: true},
		44: {Control: "num6", Pressed: false},
		45: {Control: "hash", Pressed: true},
		49: {Control: "num8", Pressed: false},
		56: {Control: "ok", Pressed: false},
		57: {Control: "num0", Pressed: true},
		58: {Control: "menu", Pressed: false},
		68: {Control: "num7", Pressed: true},
		76: {Control: "num5", Pressed: true},
		80: {Control: "num3", Pressed: false},
		81: {Control: "num8", Pressed: true},
		91: {Control: "send", Pressed: false},
		100: {Control: "num1", Pressed: false},
		101: {Control: "soft-right", Pressed: true},
		105: {Control: "down", Pressed: true},
		109: {Control: "soft-right", Pressed: false},
		111: {Control: "back", Pressed: false},
		114: {Control: "num4", Pressed: true},
		115: {Control: "soft-left", Pressed: true},
	}
	for frame := 0; frame < 1200; frame++ {
		if event, ok := events[frame]; ok {
			if err := created.QueueInput(event); err != nil {
				t.Fatalf("input at frame %d: %v", frame, err)
			}
		}
		if err := created.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
}
