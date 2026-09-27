package application

import (
	"image"
	"image/color"
	"testing"
)

func TestBREWIssue316MenuTransparency(t *testing.T) {
	const digest = "7750a55a69259aa3743b048f301a9fad39ffa0014090f08fdc24338442c2b92a"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 180)
	title := machine.Framebuffer()
	if frameIsUniform(title) {
		t.Fatal("title screen is uniform")
	}
	if got := countBREWMagenta(title); got != 0 {
		t.Fatalf("title screen contains %d transparent-key pixels", got)
	}
	titleHash := brewFrameHash(title)
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 120)
	menu := machine.Framebuffer()
	if frameIsUniform(menu) {
		t.Fatal("menu screen is uniform")
	}
	if got := countBREWMagenta(menu); got != 0 {
		t.Fatalf("menu screen contains %d transparent-key pixels", got)
	}
	if menuHash := brewFrameHash(menu); menuHash == titleHash {
		t.Fatal("menu did not replace the title screen")
	}
}

func countBREWMagenta(frame image.Image) int {
	count := 0
	bounds := frame.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.RGBAModel.Convert(frame.At(x, y)).(color.RGBA)
			if pixel.R >= 240 && pixel.G <= 20 && pixel.B >= 240 {
				count++
			}
		}
	}
	return count
}
