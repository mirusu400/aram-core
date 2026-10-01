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
	"github.com/mirusu400/aram-core/cpu"
)

// The exact #405 archive is optional; the deterministic reported input session
// remains covered whenever the authorized corpus is configured.
func TestKTFFuzzIssue405Reference(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	const name = "[KT][WIPI1.2]수호지무쌍전.zip"
	const digest = "9a2d11c489dc6e26946b6af95dc58d4cece32a68fe9e96f77f36fe13b51fbe2a"
	path := filepath.Join(root, "f-app", "KTF", "미분류", name)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("exact archive is not present in the authorized corpus")
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != digest {
		t.Fatalf("archive SHA-256=%s, want %s", got, digest)
	}
	machine, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: name, SHA256: digest, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	controls := []string{
		"up", "down", "left", "right", "ok", "soft-left", "soft-right", "menu",
		"back", "send", "end", "star", "hash", "num0", "num1", "num2", "num3",
		"num4", "num5", "num6", "num7", "num8", "num9",
	}
	random := rand.New(rand.NewSource(102))
	held := make(map[string]bool)
	inputs := 0
	for frame := 0; frame < 600; frame++ {
		if random.Intn(4) == 0 {
			control := controls[random.Intn(len(controls))]
			pressed := !held[control]
			if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: pressed}); err != nil {
				t.Fatal(err)
			}
			held[control] = pressed
			inputs++
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("seed=102 frame=%d inputs=%d: %v", frame, inputs, err)
		}
		if frame == 130 && inputs != 38 {
			t.Fatalf("reported frame 130 has %d inputs, want 38", inputs)
		}
		if machine.State() == machinecore.StateStopped {
			if frame != 130 || inputs != 38 {
				t.Fatalf("stopped at frame %d after %d inputs, want frame 130 after 38", frame, inputs)
			}
			ktf, ok := machine.(*Machine)
			if !ok {
				t.Fatalf("exact KTF archive selected %T", machine)
			}
			if result := ktf.LastResult(); result.Reason != cpu.StopExited || result.Err != nil {
				t.Fatalf("stopped with result %+v, want clean guest exit", result)
			}
			return
		}
	}
	if machine.State() != machinecore.StateRunning {
		t.Fatalf("state=%s after 600 frames, want running", machine.State())
	}
}
