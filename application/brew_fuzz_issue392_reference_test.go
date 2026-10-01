package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Replays issue 392's deterministic input session against its exact archive.
// The private title bytes remain in the authorized corpus.
func TestBREWFuzzIssue392Reference(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	controls := []string{
		"up", "down", "left", "right", "ok", "soft-left", "soft-right", "menu",
		"back", "send", "end", "star", "hash", "num0", "num1", "num2", "num3",
		"num4", "num5", "num6", "num7", "num8", "num9",
	}
	for _, test := range []struct {
		issue    int
		category string
		name     string
		digest   string
		seed     int64
	}{
		{392, "슈팅", "[KTF BREW] 스트라이커즈1945 Plus(작화).zip", "69c354b5350cf383cfd4fddc20bc8f39fe3f724d4e3ec0f5f22f805c9d7355ed", 102},
	} {
		t.Run(fmt.Sprint(test.issue), func(t *testing.T) {
			path := filepath.Join(root, "KTF BREW 게임파일", test.category, test.name)
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				t.Skip("exact archive is not present in the authorized corpus")
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != test.digest {
				t.Fatalf("archive SHA-256=%s, want %s", got, test.digest)
			}
			factory := NewFactory()
			factory.AllowUntrustedBREW = true
			machine, err := factory.Create(context.Background(), machinecore.Source{
				Name: test.name, SHA256: test.digest, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer machine.Close()
			if err := machine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			random := rand.New(rand.NewSource(test.seed))
			held := make(map[string]bool)
			presses := 0
			for frame := 0; frame < 600; frame++ {
				if test.seed != 0 && random.Intn(4) == 0 {
					control := controls[random.Intn(len(controls))]
					pressed := !held[control]
					if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: pressed}); err != nil {
						t.Fatal(err)
					}
					held[control] = pressed
					presses++
				}
				if err := machine.StepFrame(context.Background()); err != nil {
					t.Fatalf("seed=%d frame=%d presses=%d: %v", test.seed, frame, presses, err)
				}
				if frame == 488 && presses != 137 {
					t.Fatalf("reported frame has %d events, want 137", presses)
				}
			}
			if machine.State() != machinecore.StateRunning {
				t.Fatalf("state=%s after 600 frames, want running", machine.State())
			}
			brew, ok := machine.(*brewMachine)
			if !ok {
				t.Fatalf("exact BREW archive selected %T", machine)
			}
			stats, available := brew.BREWFrameStats()
			if !available || stats.PresentCount == 0 || !stats.FrameValid {
				t.Fatalf("no guest rendering evidence: stats=%+v available=%v", stats, available)
			}
			t.Logf("completed 600 frames with %d events and %d guest presentations", presses, stats.PresentCount)
		})
	}
}
