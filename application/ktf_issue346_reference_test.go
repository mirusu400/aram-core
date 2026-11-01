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
// Itarus is a native Clet. Since issue #570 its card gets each key's press
// edge as a press, so it acts on the press 5 frames before the release it used
// to act on. The checkpoints from frame 900 on were re-captured then. They
// show the same screens as before, at a slightly different animation phase.
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
		"3041669b8d92ac639198b2552947aede197134cd258a5ef20f6e783a8a4a35cc",
		"cf7ecd5c86629918c4e8d98156c734bd94fe3dbd31c70f1fc8f6724eae939cac",
		"126549d20d490bc372a960670dc3516af11bd5877c99d9a34cdd510f0bae0b92",
		"0d8fa8833a93f1b4ab6c90341774a6d2749668bec48b5f7a3c5f69a4c7f35c61",
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
