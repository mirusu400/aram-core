package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
)

// The exact private corpus is optional, while the yielding-initializer unit
// test in skvm always runs with a synthetic class file.
func TestJ2MELGTYeongwoongSeogiIanReachesMenu(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	path := filepath.Join(root, "LGT 다운타운 게임파일", "RPG", "[LGT 다운타운] 영웅서기-이안편(공용).zip")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if got := hex.EncodeToString(digest[:]); got != "3a0287b149b471b9bda023f3593d1bec598b876c0eee700654fe2dad17d92da7" {
		t.Fatalf("issue #306 archive SHA-256 = %s, want reported build", got)
	}
	ctx := context.Background()
	created, err := NewFactory().Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer created.Close()
	machine := created.(*skvmhost.Machine)
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
	frameHash := func() [32]byte {
		t.Helper()
		frame := machine.Framebuffer()
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
	pressSelect := func() {
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
	step(600)
	notice := frameHash()
	pressSelect()
	step(160)
	title := frameHash()
	if title == notice {
		t.Fatal("select did not advance from the notice to the title")
	}
	pressSelect()
	step(240)
	if menu := frameHash(); menu == title || menu == notice {
		t.Fatal("select did not advance from the title to the menu")
	}
	step(4000)
	if menu := frameHash(); menu == title || menu == notice {
		t.Fatal("title left the menu before the 5000-frame milestone")
	}
}
