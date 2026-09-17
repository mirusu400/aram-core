package application

import (
	"bytes"
	"context"
	"image/color"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

const apocalypseSHA256 = "9babd9789bae476aa3c340544220340b1e1feff0bc440ca707cbb491d94d2786"

// TestKTFApocalypseFinishesCountryLoading is an optional authorized-corpus
// regression for issue #265. The second country's opening used to remain on
// its loading card because serviceRepaints discarded a live paint continuation
// at the scene transition.
func TestKTFApocalypseFinishesCountryLoading(t *testing.T) {
	t.Setenv("ARAM_CPU", "jit")
	path, data := findAuthorizedPackage(t, apocalypseSHA256)
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.Start(context.Background()))

	step := func(frames int) {
		for range frames {
			check(t, machine.StepFrame(context.Background()))
		}
	}
	press := func(control string) {
		check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
		step(2)
		check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}))
		step(30)
	}
	chromaticPixels := func() int {
		image := machine.Framebuffer()
		count := 0
		for y := 120; y < image.Bounds().Dy(); y++ {
			for x := 0; x < image.Bounds().Dx(); x++ {
				pixel := color.RGBAModel.Convert(image.At(x, y)).(color.RGBA)
				low, high := pixel.R, pixel.R
				if pixel.G < low {
					low = pixel.G
				}
				if pixel.B < low {
					low = pixel.B
				}
				if pixel.G > high {
					high = pixel.G
				}
				if pixel.B > high {
					high = pixel.B
				}
				if high-low > 40 {
					count++
				}
			}
		}
		return count
	}

	step(600)
	press("fire")
	step(120)
	press("fire")
	step(120)
	press("down")
	step(120)
	press("fire")
	step(120)
	for range 3 {
		press("fire")
	}
	press("down")
	press("fire")
	maxChroma := 0
	for range 20 {
		press("fire")
		maxChroma = max(maxChroma, chromaticPixels())
	}
	if maxChroma < 500 {
		t.Fatalf("second-country loading never reached the next dialogue: max chromatic pixels = %d", maxChroma)
	}
}
