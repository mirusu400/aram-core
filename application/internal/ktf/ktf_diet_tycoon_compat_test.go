package ktf

import (
	"crypto/sha256"
	"image"
	"image/color"
	"image/draw"
	"testing"

	"github.com/mirusu400/aram-core/loader/ktf"
)

func TestKTFMenuForegroundCompatibilityReplaysLabelsAboveLogo(t *testing.T) {
	label := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	label.SetNRGBA(0, 0, color.NRGBA{R: 0xff, A: 0xff})
	label.SetNRGBA(1, 0, color.NRGBA{R: 0xff, A: 0xff})
	logo := image.NewNRGBA(image.Rect(0, 0, 195, 107))
	draw.Draw(
		logo,
		logo.Bounds(),
		image.NewUniform(color.RGBA{B: 0xff, A: 0xff}),
		image.Point{},
		draw.Src,
	)
	labelHash := sha256.Sum256(ktfRGBABytes(label))
	logoHash := sha256.Sum256(ktfRGBABytes(logo))
	target := image.NewRGBA(image.Rect(0, 0, 240, 320))
	state := &ktfGraphics{Target: target, clip: target.Bounds()}
	runtime := &Runtime{
		images: map[uint32]image.Image{1: label, 2: logo},
		menuForegroundCompat: &ktfMenuForegroundCompat{
			labelHashes: map[[sha256.Size]byte]struct{}{labelHash: {}},
			overlayHash: logoHash,
		},
	}

	runtime.drawKTFJavaImage(state, 1, label, 30, 150, 0)
	runtime.drawKTFJavaImage(state, 2, logo, 23, 145, 0)
	if got := color.RGBAModel.Convert(target.At(30, 150)).(color.RGBA); got.R != 0xff ||
		got.G != 0 || got.B != 0 {
		t.Fatalf("replayed label pixel = %#v, want red", got)
	}
	if runtime.menuForegroundCompat.overlayImage != 2 ||
		len(runtime.menuForegroundCompat.pending) != 0 {
		t.Fatalf("compatibility state = %+v", runtime.menuForegroundCompat)
	}

	withoutCompat := image.NewRGBA(target.Bounds())
	plainState := &ktfGraphics{Target: withoutCompat, clip: withoutCompat.Bounds()}
	plainRuntime := &Runtime{}
	plainRuntime.drawKTFJavaImage(plainState, 1, label, 30, 150, 0)
	plainRuntime.drawKTFJavaImage(plainState, 2, logo, 23, 145, 0)
	if got := color.RGBAModel.Convert(withoutCompat.At(30, 150)).(color.RGBA); got.B != 0xff ||
		got.R != 0 || got.G != 0 {
		t.Fatalf("ordinary source-over pixel = %#v, want blue", got)
	}
}

func TestKTFMenuForegroundCompatibilityRequiresExactDietTycoonClient(t *testing.T) {
	if compat := newKTFMenuForegroundCompat(ktf.Package{
		Descriptor: ktf.Descriptor{AID: "01034DCD", MainClass: "Diet"},
		Client:     []byte("different client"),
	}); compat != nil {
		t.Fatal("a lookalike Diet package enabled title-specific compositing")
	}
}
