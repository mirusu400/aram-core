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

// The handset ends the applet on END and never delivers the following key-up.
// The same randomized session without END still exercises 600 gameplay frames.
func TestBREWEndKeyStopsAppletReference(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	controls := []string{"up", "down", "left", "right", "ok", "soft-left", "soft-right", "menu", "back", "send", "end", "star", "hash", "num0", "num1", "num2", "num3", "num4", "num5", "num6", "num7", "num8", "num9"}
	for _, test := range []struct {
		issue  int
		path   string
		digest string
	}{
		{397, filepath.Join("KTF BREW 게임파일", "액션", "[KTF BREW] 질주쾌감 스케쳐.zip"), "e20329e1e7767dcc93106f167bf7de4d5f4db4315326cba9bd4c3ebf1b441c55"},
		{402, filepath.Join("KTF BREW 게임파일", "타이쿤", "[KTF BREW] 초밥의달인3(작화).ZIP"), "8e10051b73c12f544918cdbbd72fb14c0d8050e003f62d19706a6b7af4124aa6"},
	} {
		t.Run(fmt.Sprint(test.issue), func(t *testing.T) {
			path := filepath.Join(root, test.path)
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				t.Skip("exact archive is not present in the authorized corpus")
			}
			if err != nil {
				t.Fatal(err)
			}
			if actual := fmt.Sprintf("%x", sha256.Sum256(data)); actual != test.digest {
				t.Fatalf("archive SHA-256=%s, want %s", actual, test.digest)
			}
			for _, skipEnd := range []bool{false, true} {
				name := "handset-stop"
				if skipEnd {
					name = "continued-gameplay"
				}
				t.Run(name, func(t *testing.T) {
					factory := NewFactory()
					factory.AllowUntrustedBREW = true
					machine, err := factory.Create(context.Background(), machinecore.Source{Name: filepath.Base(path), SHA256: test.digest, ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
					if err != nil {
						t.Fatal(err)
					}
					defer machine.Close()
					if err := machine.Start(context.Background()); err != nil {
						t.Fatal(err)
					}
					random := rand.New(rand.NewSource(102))
					held := make(map[string]bool)
					generated := 0
					for frame := 0; frame < 600; frame++ {
						if random.Intn(4) == 0 {
							control := controls[random.Intn(len(controls))]
							pressed := !held[control]
							if !skipEnd || control != "end" {
								if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: pressed}); err != nil {
									t.Fatal(err)
								}
								held[control] = pressed
							}
							generated++
						}
						if err := machine.StepFrame(context.Background()); err != nil {
							t.Fatalf("frame=%d generated=%d: %v", frame, generated, err)
						}
						if !skipEnd && frame == 128 {
							if generated != 37 || machine.State() != machinecore.StateStopped {
								t.Fatalf("END frame generated=%d state=%s, want 37/stopped", generated, machine.State())
							}
							brew := machine.(*brewMachine)
							if count := brew.runtime.EventCount(1); count != 1 {
								t.Fatalf("EVT_APP_STOP count=%d, want 1", count)
							}
							return
						}
					}
					if machine.State() != machinecore.StateRunning {
						t.Fatalf("state=%s after 600 gameplay frames", machine.State())
					}
					brew := machine.(*brewMachine)
					stats, available := brew.BREWFrameStats()
					if !available || stats.PresentCount == 0 || !stats.FrameValid {
						t.Fatalf("no guest presentation: %+v available=%v", stats, available)
					}
					t.Logf("completed 600 frames with %d generated actions and %d guest presentations", generated, stats.PresentCount)
				})
			}
		})
	}
}
