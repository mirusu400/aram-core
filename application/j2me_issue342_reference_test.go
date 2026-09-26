package application

import (
	"bytes"
	"context"
	"image"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

func TestJ2MEIssue342UsesWorldTennisHandsetCanvas(t *testing.T) {
	const digest = "c065052a8ea712cbe970ee42e591c1f65189bde57521a24879a0bce8d2ff0837"
	path, data := findAuthorizedPackage(t, digest)
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer created.Close()
	if got := created.Framebuffer().Bounds().Size(); got != image.Pt(120, 160) {
		t.Fatalf("World Tennis canvas = %v, want 120x160", got)
	}
	if err := created.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 180; frame++ {
		if err := created.StepFrame(context.Background()); err != nil {
			t.Fatalf("step World Tennis frame %d: %v", frame, err)
		}
	}
	if frameIsUniform(created.Framebuffer()) {
		t.Fatal("World Tennis title screen remained uniform")
	}
}
