package ktf

import (
	"image"
	"image/color"
	"image/draw"
)

// The annunciator is the handset's own status bar. A KTF title that calls
// AnnunciatorComponent.show() asks the phone to keep the top of the screen for
// it and lays its Card out underneath, which is why CardOriginY moves the card
// down. ARAM had nothing to put there, so 110 of the 218 KTF corpus titles
// carried a dead black band across the top of the screen for the whole
// session — issue #80 reported it as a letterbox.
//
// Paint the bar instead. It is deliberately generic — signal strength and a
// battery, no carrier mark, no clock — so it reads as the phone chrome the
// layout already reserves room for and stays byte-identical from frame to
// frame, which keeps a title's rendering deterministic.
var (
	annunciatorBackground = color.RGBA{R: 0x14, G: 0x1b, B: 0x24, A: 0xff}
	annunciatorEdge       = color.RGBA{R: 0x2c, G: 0x3a, B: 0x4a, A: 0xff}
	annunciatorInk        = color.RGBA{R: 0xc8, G: 0xd8, B: 0xe8, A: 0xff}
	annunciatorDim        = color.RGBA{R: 0x46, G: 0x58, B: 0x6c, A: 0xff}
)

// paintKTFAnnunciator draws the status bar into strip. A strip too small to
// hold the indicators keeps the plain background, which is still better than
// the bare framebuffer it replaces.
func paintKTFAnnunciator(target draw.Image, strip image.Rectangle) {
	strip = strip.Intersect(target.Bounds())
	if strip.Empty() {
		return
	}
	draw.Draw(
		target,
		strip,
		image.NewUniform(annunciatorBackground),
		image.Point{},
		draw.Src,
	)
	fillAnnunciatorRect(
		target,
		image.Rect(strip.Min.X, strip.Max.Y-1, strip.Max.X, strip.Max.Y),
		annunciatorEdge,
	)
	if strip.Dx() < 48 || strip.Dy() < 9 {
		return
	}
	paintAnnunciatorSignal(target, strip)
	paintAnnunciatorBattery(target, strip)
}

// paintAnnunciatorSignal draws four rising reception bars against the left
// edge, sitting on the same baseline as the battery.
func paintAnnunciatorSignal(target draw.Image, strip image.Rectangle) {
	baseline := strip.Max.Y - 1 - annunciatorMargin(strip)
	left := strip.Min.X + annunciatorMargin(strip) + 1
	for index := 0; index < 4; index++ {
		height := 3 + index*2
		if baseline-height < strip.Min.Y+1 {
			height = baseline - strip.Min.Y - 1
		}
		if height <= 0 {
			continue
		}
		fillAnnunciatorRect(
			target,
			image.Rect(
				left+index*3,
				baseline-height,
				left+index*3+2,
				baseline,
			),
			annunciatorInk,
		)
	}
}

// paintAnnunciatorBattery draws a full battery against the right edge.
func paintAnnunciatorBattery(target draw.Image, strip image.Rectangle) {
	margin := annunciatorMargin(strip)
	baseline := strip.Max.Y - 1 - margin
	const (
		bodyWidth  = 14
		bodyHeight = 8
		nubWidth   = 2
		nubHeight  = 4
	)
	height := min(bodyHeight, baseline-strip.Min.Y-1)
	if height < 3 {
		return
	}
	right := strip.Max.X - margin - nubWidth
	body := image.Rect(right-bodyWidth, baseline-height, right, baseline)
	fillAnnunciatorRect(target, body, annunciatorInk)
	fillAnnunciatorRect(target, body.Inset(1), annunciatorBackground)
	// The charge sits inside the shell with a one-pixel gap, so a full battery
	// still reads as a battery rather than a solid block.
	fillAnnunciatorRect(target, body.Inset(2), annunciatorInk)
	nubTop := body.Min.Y + (height-min(nubHeight, height))/2
	fillAnnunciatorRect(
		target,
		image.Rect(right, nubTop, right+nubWidth, nubTop+min(nubHeight, height)),
		annunciatorDim,
	)
}

// annunciatorMargin keeps the indicators off the bar's own edges without
// letting a very short bar squeeze them out entirely.
func annunciatorMargin(strip image.Rectangle) int {
	if strip.Dy() >= 16 {
		return 4
	}
	return 2
}

func fillAnnunciatorRect(target draw.Image, area image.Rectangle, fill color.RGBA) {
	area = area.Intersect(target.Bounds())
	if area.Empty() {
		return
	}
	draw.Draw(target, area, image.NewUniform(fill), image.Point{}, draw.Src)
}
