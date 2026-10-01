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

// Replays the exact archives and deterministic input sessions reported in
// issues 387–389. The private title bytes remain in the authorized corpus.
func TestBREWFuzzIssues387To389Reference(t *testing.T) {
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
		issue                         int
		category                      string
		name                          string
		digest                        string
		seed                          int64
		reportedFrame, reportedInputs int
	}{
		{387, "기타", "[KTF BREW] 동전쌓기2(작화).zip", "8dcf9ad11776f010c1e7ac0bd33b13a2d1df9f8af075a057d0d2ae92ed57a9eb", 0, 77, 0},
		{388, "레이싱", "[KTF BREW] 드림랠리(작화).zip", "a99c50008ae00b9c71915ee3c708ccd3cc4fe895c1932b22e58540df983d9b87", 102, 492, 139},
		{389, "레이싱", "[KTF BREW] 미니카레이싱 GP+.zip", "85c56cc7f37253b673fcd1b28ac3ab7cfcb3362810e56a1af6acb0d2450b2b8e", 102, 408, 114},
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
				if frame == test.reportedFrame && presses != test.reportedInputs {
					t.Fatalf("reported frame %d has %d events, want %d", frame, presses, test.reportedInputs)
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
