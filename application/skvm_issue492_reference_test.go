package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Alive loads its first screen image from Canvas.showNotify. Without that
// callback the second paint draws a null image and fails at frame 1.
func TestSKVMIssue492AliveShowsCanvasBeforePainting(t *testing.T) {
	const digest = "38277d63b0ba2803076c82ff36f5bc1435d227d0401e4e9eedc455d56da5a68a"
	path, data := findAuthorizedPackage(t, digest)
	ctx := context.Background()
	created, err := NewFactory().Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	if err := created.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 600; frame++ {
		if err := created.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
}
