package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

const itarusSHA256 = "e40b5b8196d28e505d26ea1fc7fdc97d73d6282f34dbaf54ca2f1f470f1f5d97"

// These framebuffer hashes were captured before the private-data cache change.
// The script progresses from the title through menus into the game world.
func TestKTFIssue346ItarusKeepsBaselineFrames(t *testing.T) {
	path, data := findAuthorizedPackage(t, itarusSHA256)
	factory := NewFactory()
	factory.NewCPU = newJITCPU
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	goldens := []string{
		"1d7de765a92d5894ffc145d4357ac12a88b4321baeda1cb6f9990160bc2a2ff9",
		"32253d0870b1dcf6c8ecb7cb45dd0866e49f8188dd6a23c8661e476e5330da99",
		"81e78cdac1850854d3b12bbd9a28fdb2382255dc70922abdf22a9bedec6f732e",
		"6606e08d79006c3aa0f25eed3236eab5704d46bb3d7b0ac23daa7d3b9eebaac7",
		"f1da2b1dbe02bc601c188e4ee8ce461f207363249ffedd1d28b992581acf65c8",
		"44c08e1b23b01a3af44809f9e999a8de78f614a49eba36720c964eeb3085abe2",
	}
	started := time.Now()
	for frame := 1; frame <= 1800; frame++ {
		if frame >= 301 && frame <= 1506 {
			if frame%300 == 1 {
				if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: true}); err != nil {
					t.Fatal(err)
				}
			}
			if frame%300 == 6 {
				if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: false}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		_ = machine.DrainPublishedAudio()
		if frame%300 == 0 {
			sum := sha256.Sum256(machine.frame.Pix)
			if got := hex.EncodeToString(sum[:]); got != goldens[frame/300-1] {
				t.Fatalf("frame %d hash=%s, want %s", frame, got, goldens[frame/300-1])
			}
		}
	}
	t.Logf("1800 frames: %s, presents=%d", time.Since(started), machine.ktf.PresentCount)
}
