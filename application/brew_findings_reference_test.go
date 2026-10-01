package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Exact private-corpus regressions for the remaining 2026-10-01 findings.
// Archives stay outside the repository; only identities and input seeds are
// recorded here. The ordinary machine path must complete, not merely classify
// a guest fault or silently skip an applet that fails to start.
func TestBREWRemainingFindingsReference(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Skip("BREW finding regressions require an authorized corpus directory")
	}
	controls := []string{
		"up", "down", "left", "right", "ok", "soft-left", "soft-right", "menu",
		"back", "send", "end", "star", "hash", "num0", "num1", "num2", "num3",
		"num4", "num5", "num6", "num7", "num8", "num9",
	}
	for _, test := range []struct {
		title, digest string
		seed          int64
	}{
		{"림오브팬텀(작화)", "a6c8b836db6b3048b3553a5642f7cd755f2788a82ce370cdfceb4797b3e893d5", 101},
		{"메이플스토리 궁수편", "1464cdb037ffa77b5e07d4517164441cdcef4d30a6d17337f9790471ef25495e", 101},
		{"메이플스토리 전사편", "79e298bbb3842aa80ba8bcf7a5c037c90468e805976e45573a5b288b298e5249", 0},
		{"메이플스토리 법사편", "4f0352decc4572843d9ed05d87a284e90553a4c5bfb3f08a832ed47e3341d6bc", 101},
		{"소드마스터전기", "14258751ea5252a7987947f3fa7f3d129d0f0c2635b1165563805d838bab54e5", 101},
		{"어스토니시아 스토리 EP2", "a444c3b150c2c816cd3b47754966c3aeebc888a780a60b364ef9f55d585d2be3", 0},
		{"어스토니시아 스토리 EP3", "f6cef49ec0561b82cf30864a76a23db6de87e02535feeaf6f844c8943980d08d", 0},
		{"어스토니시아 스토리(EP1)", "74539d4f0d5a31a5facce3dffa293f4c67aef01604575eb66163a3ecdd3aafc3", 0},
		{"카샨", "114287328f1a4604eb605c227646866a1a44d502b663652fb04a3b640a4b6440", 101},
	} {
		t.Run(test.title, func(t *testing.T) {
			name := "[KTF BREW] " + test.title + ".zip"
			data, err := os.ReadFile(filepath.Join(root, "KTF BREW 게임파일", "RPG", name))
			if os.IsNotExist(err) {
				t.Skip("exact archive is not present in authorized corpus")
			}
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(data)
			if actual := hex.EncodeToString(digest[:]); actual != test.digest {
				t.Fatalf("archive SHA-256=%s, want %s", actual, test.digest)
			}
			factory := NewFactory()
			factory.AllowUntrustedBREW = true
			machine, err := factory.Create(context.Background(), machinecore.Source{
				Name: name, SHA256: test.digest, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer machine.Close()
			if err := machine.Start(context.Background()); err != nil {
				t.Fatalf("start exact title: %v", err)
			}
			random := rand.New(rand.NewSource(test.seed))
			held := make(map[string]bool)
			for frame := 0; frame < 600; frame++ {
				if test.seed != 0 && random.Intn(4) == 0 {
					control := controls[random.Intn(len(controls))]
					pressed := !held[control]
					if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: pressed}); err != nil {
						t.Fatal(err)
					}
					held[control] = pressed
				}
				if err := machine.StepFrame(context.Background()); err != nil {
					t.Fatalf("seed=%d frame=%d: %v", test.seed, frame, err)
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
			t.Logf("seed=%d completed 600 frames; guest presentations=%d", test.seed, stats.PresentCount)
		})
	}
}
