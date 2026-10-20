package runtime

import (
	"image/color"
	"testing"
)

func TestSKTGammaRGBAPreservesNeutralAndAppliesExtremes(t *testing.T) {
	source := color.RGBA{R: 201, G: 129, B: 67, A: 255}
	if got, ok := SKTGammaRGBA(3, source); !ok || got != source {
		t.Fatalf("neutral gamma = %#v, %v; want %#v, true", got, ok, source)
	}
	if got, ok := SKTGammaRGBA(6, source); !ok || got != (color.RGBA{A: 255}) {
		t.Fatalf("darkest gamma = %#v, %v; want black, true", got, ok)
	}
	if _, ok := SKTGammaRGBA(7, source); ok {
		t.Fatal("accepted gamma 7")
	}
	transparent := color.RGBA{R: 1, G: 2, B: 3}
	if got, ok := SKTGammaRGBA(0, transparent); !ok || got != transparent {
		t.Fatalf("transparent gamma = %#v, %v", got, ok)
	}
}
