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

const nom3SHA256 = "3cbb25f886daf00c4b66de4f46f606d962efe1735f8e38205e3efbd8fb209202"

// Baseline frame hashes were captured before the receiver/plot optimizations,
// progressing through the title, menu, skin unlock, mission and gameplay.
func TestKTFIssue351Nom3GraphicsBridgeKeepsBaselineFrames(t *testing.T) {
	path, data := findAuthorizedPackage(t, nom3SHA256)
	factory := NewFactory()
	factory.NewCPU = newJITCPU
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	goldens := []string{
		"3b8431e9381dba322c0c96c8714eec1eb9c423aed854927f9b5ef275247b35ee",
		"3b602308fde2d95face5517b3cec9d1a69baab8f6af42f6a9943d46ee7426e5c",
		"30f8d5777bf39227ccd1dc41481b7907b1a028da3b1596e60f2a67b4feb1636b",
		"d1bfb2f01d8932831b4410a63412c04802edfaf2586310efe9228a9106d04a5d",
		"db95d6cd5970bf1779cd61d9a20f7b23773c01e51d182fa6ae1718100868f8c1",
		"02c68ef3877eafdc6365653619e7aa060f005ecb0a59e4e67b54a2441d110154",
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
