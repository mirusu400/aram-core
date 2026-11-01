package application

import (
	"bytes"
	"context"
	"image"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/brewrt"
	machinecore "github.com/mirusu400/aram-core/core"
)

// The canvas of a package without an exact policy comes from what the title
// draws, never from its file name: two copies of one package get one canvas.
// This package faults before drawing anything, which is no evidence of a
// small-handset layout, so both stay on the large canvas.
func TestBREWCanvasIgnoresPackageName(t *testing.T) {
	data := genericBREWArchiveForTest(t)
	for _, name := range []string{"[KTF BREW] 신시티.zip", "[KTF BREW] 드래곤판타지(작화).zip", "generic.zip"} {
		factory := NewFactory()
		factory.AllowUntrustedBREW = true
		created, matched, err := factory.createBREWMachine(context.Background(), machinecore.Source{
			Name: name, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
		})
		if err != nil || !matched {
			t.Fatalf("%s: matched=%v err=%v", name, matched, err)
		}
		if got, want := created.Framebuffer().Bounds(), image.Rect(0, 0, 176, 220); got != want {
			t.Fatalf("%s canvas = %v, want %v", name, got, want)
		}
		created.Close()
	}
}

func TestBREWCanvasDecisionShrinksOnlyOnEvidence(t *testing.T) {
	small, large := brewrt.SmallHandsetCanvas, brewrt.LargeHandsetCanvas
	for _, test := range []struct {
		name               string
		area, right        int64
		frameRight, corner float64
		want               image.Point
	}{
		{"artwork within 120 columns", 400_000, 8_000, 0.05, 0.6, small},
		{"artwork past column 120", 400_000, 120_000, 0.5, 0.6, large},
		{"artwork just over the line", 400_000, 40_000, 0.05, 0.6, large},
		{"narrow artwork, busy right edge", 400_000, 8_000, 0.2, 0.6, large},
		{"some artwork within 120 columns", 24_000, 500, 0.03, 0.8, small},
		{"some artwork, buttons past column 120", 46_000, 3_900, 0.07, 0.2, large},
		{"directly painted, corner only", 1_000, 0, 0.01, 0.4, small},
		{"directly painted, whole screen", 1_000, 500, 0.3, 0.4, large},
		{"nothing drawn at all", 0, 0, 0, 0, large},
	} {
		probe := brewrt.CanvasProbe{ImageArea: test.area, ImageRight: test.right}
		if got := brewCanvasDecision(probe, test.frameRight, test.corner); got != test.want {
			t.Errorf("%s: canvas = %v, want %v", test.name, got, test.want)
		}
	}
}
