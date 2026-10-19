package application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"os"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader"
)

// TestReferencePXO1Machine uses a user-authorized local executable and keeps
// both its bytes and path outside the repository.
func TestReferencePXO1Machine(t *testing.T) {
	path := os.Getenv("ARAM_PXO_DATA")
	if path == "" {
		t.Skip("ARAM_PXO_DATA is not set")
	}
	frames := make(map[string]string)
	for _, test := range []struct {
		name string
		down bool
	}{
		{name: "sanakan"},
		{name: "3d-block", down: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			created, err := NewFactory().Create(context.Background(), machinecore.Source{
				Name:     "reference.pxo",
				Format:   string(loader.KindPXO),
				ReaderAt: file,
				Size:     info.Size(),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer created.Close()
			if err := created.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			counter := created.(interface{ PXO1PresentCount() uint64 })
			if counter.PXO1PresentCount() == 0 {
				t.Fatal("PXO1 launcher did not request a presentation")
			}
			if test.down {
				if err := created.QueueInput(machinecore.InputEvent{Control: "down", Pressed: true}); err != nil {
					t.Fatal(err)
				}
			}
			if err := created.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true}); err != nil {
				t.Fatal(err)
			}
			frame := created.Framebuffer()
			if frame == nil || frame.Bounds() != image.Rect(0, 0, 176, 220) {
				t.Fatalf("frame bounds = %v", frame)
			}
			rgba, ok := frame.(*image.RGBA)
			if !ok {
				t.Fatalf("frame type = %T, want *image.RGBA", frame)
			}
			frames[test.name] = fmt.Sprintf("%x", sha256.Sum256(rgba.Pix))
		})
	}
	if frames["sanakan"] == frames["3d-block"] {
		t.Fatalf("both game selections rendered the same frame %s", frames["sanakan"])
	}
}
