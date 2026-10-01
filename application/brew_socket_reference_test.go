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

// The exact seeded menu path opened a network socket during initialization.
// Returning a null socket caused cleanup and a later null-interface branch.
func TestBREWOfflineSocketReference(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA unset")
	}
	path := filepath.Join(root, "KTF BREW 게임파일", "전략-SRPG", "[KTF BREW] 진짜장기.zip")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("exact archive is not present in the authorized corpus")
	}
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if digest != "06369c8db97fb4e28165297ac2557c0de050dfb6986ea429d3411ffedfa891b9" {
		t.Fatalf("SHA-256=%s", digest)
	}
	factory := NewFactory()
	factory.AllowUntrustedBREW = true
	machine, err := factory.Create(context.Background(), machinecore.Source{Name: filepath.Base(path), SHA256: digest, ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	controls := []string{"up", "down", "left", "right", "ok", "soft-left", "soft-right", "menu", "back", "send", "end", "star", "hash", "num0", "num1", "num2", "num3", "num4", "num5", "num6", "num7", "num8", "num9"}
	rng := rand.New(rand.NewSource(102))
	held := map[string]bool{}
	for frame := 0; frame < 600; frame++ {
		if rng.Intn(4) == 0 {
			control := controls[rng.Intn(len(controls))]
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
		if machine.State() != machinecore.StateRunning {
			t.Fatalf("frame=%d state=%s, want running", frame, machine.State())
		}
	}
	stats, available := machine.(*brewMachine).BREWFrameStats()
	if !available || !stats.FrameValid || stats.PresentCount == 0 {
		t.Fatalf("no guest presentation: %+v available=%v", stats, available)
	}
	t.Logf("reached frame=600 presentations=%d", stats.PresentCount)
}
