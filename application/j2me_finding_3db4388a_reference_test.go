package application

import (
	"bytes"
	"context"
	"image"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

func TestJ2MEMiniGameChuringUsesDeclaredLGTCanvas(t *testing.T) {
	const digest = "cd18ad615cbf2bd2f14c3e711002019582a2512247f2ce10b7b8eef2abb387c3"
	path, data := findAuthorizedPackage(t, digest)
	machine, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	if got := machine.Framebuffer().Bounds().Size(); got != image.Pt(176, 200) {
		t.Fatalf("declared LGT canvas = %v, want 176x200", got)
	}
}
