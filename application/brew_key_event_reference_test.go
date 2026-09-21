package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

func TestBREWLegacyKeyEventAdvancesReportedRPGs(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	for _, test := range []struct {
		issue, name, digest string
	}{
		{"313", "[KTF BREW] 레전드 오브 엘로스.zip", "9d1bf51748ac93d6008d2a55f29a5c26c13d778486b981c258c2c8ef9dedf9ef"},
		{"314", "[KTF BREW] 리니지 몬퀘스트+(작화).zip", "08d439acc62dda49995218275464cb04ee7338bbcffcb261558290f80be4eb32"},
	} {
		t.Run(test.issue, func(t *testing.T) {
			path := filepath.Join(root, "KTF BREW 게임파일", "RPG", test.name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != test.digest {
				t.Fatalf("archive SHA-256 = %s, want issue #%s build", got, test.issue)
			}
			factory := NewFactory()
			factory.AllowUntrustedBREW = true
			ctx := context.Background()
			created, err := factory.Create(ctx, machinecore.Source{
				Name: filepath.Base(path), Path: path,
				ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer created.Close()
			machine, ok := created.(*brewMachine)
			if !ok {
				t.Fatalf("machine type = %T, want BREW", created)
			}
			if err := machine.Start(ctx); err != nil {
				t.Fatal(err)
			}
			step := func(count int) {
				t.Helper()
				for frame := 0; frame < count; frame++ {
					if err := machine.StepFrame(ctx); err != nil {
						t.Fatalf("step frame %d/%d: %v", frame, count, err)
					}
				}
			}
			tap := func() {
				t.Helper()
				for _, event := range []machinecore.InputEvent{
					{Control: "select", Pressed: true},
					{Control: "select", Pressed: false, At: time.Millisecond},
				} {
					if err := machine.QueueInput(event); err != nil {
						t.Fatal(err)
					}
				}
			}
			step(239)
			before := brewFrameHash(machine.Framebuffer())
			tap()
			step(120)
			after := brewFrameHash(machine.Framebuffer())
			if before == after {
				t.Fatal("first select tap left the guest screen unchanged")
			}
			tap()
			step(240)
			if second := brewFrameHash(machine.Framebuffer()); second == after {
				t.Fatal("second select tap left the guest screen unchanged")
			}
			if got := machine.runtime.EventCount(0x100); got != 2 {
				t.Fatalf("EVT_KEY dispatches = %d, want 2", got)
			}
		})
	}
}

func brewFrameHash(frame image.Image) [32]byte {
	bounds := frame.Bounds()
	pixels := make([]byte, 0, bounds.Dx()*bounds.Dy()*4)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := frame.At(x, y).RGBA()
			pixels = append(pixels, byte(r>>8), byte(g>>8), byte(b>>8), byte(a>>8))
		}
	}
	return sha256.Sum256(pixels)
}
