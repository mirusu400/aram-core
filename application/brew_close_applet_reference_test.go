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

// These seeded sessions request IShell::CloseApplet after END is omitted.
// The shell must stop each applet before dispatching another keypad action.
func TestBREWCloseAppletReference(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	controls := []string{"up", "down", "left", "right", "ok", "soft-left", "soft-right", "menu", "back", "send", "end", "star", "hash", "num0", "num1", "num2", "num3", "num4", "num5", "num6", "num7", "num8", "num9"}
	for _, tc := range []struct {
		issue                int
		folder, name, digest string
		stopFrame            int
	}{
		{390, "리듬", "[KTF BREW] EZ2DJ(작화).zip", "94653274c88704e2f5e9c2d4beb867fe776f79d2312e7802db65001589fb74e8", 156},
		{391, "슈팅", "[KTF BREW] 건버드+(작화).zip", "c23aac3be59e08d0b4973743b4c9ec3172b69f078df322464a5b3f6d8799c223", 155},
		{401, "타이쿤", "[KTF BREW] 영차영차타이쿤(작화).zip", "bb02e1a4691f26cc2c7a0970b2d104a2650e663fddbc47f806917240ff5d85cc", 552},
		{403, "퍼즐-카드", "[KTF BREW] PushPush 주차장(작화).zip", "3f94b416d03163834ff21f3c2ec9f1b3986de925968c22a60d5ef0d60452a3fa", 296},
	} {
		t.Run(fmt.Sprint(tc.issue), func(t *testing.T) {
			path := filepath.Join(root, "KTF BREW 게임파일", tc.folder, tc.name)
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				t.Skip("exact archive is not present in the authorized corpus")
			}
			if err != nil {
				t.Fatal(err)
			}
			if actual := fmt.Sprintf("%x", sha256.Sum256(data)); actual != tc.digest {
				t.Fatalf("archive SHA-256=%s, want %s", actual, tc.digest)
			}
			factory := NewFactory()
			factory.AllowUntrustedBREW = true
			machine, err := factory.Create(context.Background(), machinecore.Source{
				Name: filepath.Base(path), SHA256: tc.digest, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer machine.Close()
			if err := machine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			random := rand.New(rand.NewSource(102))
			held := make(map[string]bool)
			for frame := 0; frame <= tc.stopFrame; frame++ {
				if random.Intn(4) == 0 {
					control := controls[random.Intn(len(controls))]
					pressed := !held[control]
					if control != "end" {
						if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: pressed}); err != nil {
							t.Fatal(err)
						}
						held[control] = pressed
					}
				}
				if err := machine.StepFrame(context.Background()); err != nil {
					t.Fatalf("frame=%d: %v", frame, err)
				}
				want := machinecore.StateRunning
				if frame == tc.stopFrame {
					want = machinecore.StateStopped
				}
				if machine.State() != want {
					t.Fatalf("frame=%d state=%s, want %s", frame, machine.State(), want)
				}
			}
			brew := machine.(*brewMachine)
			if count := brew.runtime.EventCount(1); count != 1 {
				t.Fatalf("EVT_APP_STOP count=%d, want 1", count)
			}
			if len(brew.input) != 0 {
				t.Fatalf("%d keypad events remain after CloseApplet", len(brew.input))
			}
			stats, available := brew.BREWFrameStats()
			if !available || stats.PresentCount == 0 || !stats.FrameValid {
				t.Fatalf("no guest presentation: %+v available=%v", stats, available)
			}
			t.Logf("stopped at frame %d after %d guest presentations", tc.stopFrame, stats.PresentCount)
		})
	}
}
