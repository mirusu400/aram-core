package gnex32

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"

	"github.com/mirusu400/aram-core/loader/gnex"
)

// Display is the bounded GNEX drawing surface. Drawing coordinates are LCD
// coordinates; the guest receives swWidth and swHeight from the handset.
type Display struct {
	working *image.RGBA
	latest  *image.RGBA
	saved   *image.RGBA
	clip    image.Rectangle
}

func NewDisplay(width, height int) *Display {
	bounds := image.Rect(0, 0, width, height)
	return &Display{working: image.NewRGBA(bounds), clip: bounds}
}

func (d *Display) SetClip(x1, y1, x2, y2 int32) {
	if x1 == 0 && y1 == 0 && x2 == -1 && y2 == -1 {
		d.ResetClip()
		return
	}
	d.clip = image.Rect(int(x1), int(y1), int(x2)+1, int(y2)+1).Intersect(d.working.Bounds())
}

func (d *Display) ResetClip() { d.clip = d.working.Bounds() }

func (d *Display) Clear(fill color.Color) {
	draw.Draw(d.working, d.working.Bounds(), image.NewUniform(fill), image.Point{}, draw.Src)
}

func (d *Display) FillRect(x1, y1, x2, y2 int32, fill color.Color) {
	if x2 < x1 || y2 < y1 {
		return
	}
	rect := image.Rect(int(x1), int(y1), int(x2)+1, int(y2)+1).Intersect(d.clip)
	draw.Draw(d.working, rect, image.NewUniform(fill), rect.Min, draw.Src)
}

func (d *Display) FillRectAlpha(x1, y1, x2, y2 int32, fill color.RGBA, alpha uint32) {
	if x2 < x1 || y2 < y1 || alpha >= 4 {
		return
	}
	rect := image.Rect(int(x1), int(y1), int(x2)+1, int(y2)+1).Intersect(d.clip)
	if alpha == 0 {
		draw.Draw(d.working, rect, image.NewUniform(fill), rect.Min, draw.Src)
		return
	}
	weight := uint32(4 - alpha)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			old := d.working.RGBAAt(x, y)
			d.working.SetRGBA(x, y, color.RGBA{
				R: uint8((weight*uint32(fill.R) + alpha*uint32(old.R)) / 4),
				G: uint8((weight*uint32(fill.G) + alpha*uint32(old.G)) / 4),
				B: uint8((weight*uint32(fill.B) + alpha*uint32(old.B)) / 4), A: 255,
			})
		}
	}
}

// DrawRectRound and FillRectRound use inclusive LCD coordinates and clip each
// scan line before writing to the working buffer.
func (d *Display) DrawRectRound(x1, y1, x2, y2, radius int32, fill color.RGBA) {
	d.roundRect(x1, y1, x2, y2, radius, fill, false)
}

func (d *Display) FillRectRound(x1, y1, x2, y2, radius int32, fill color.RGBA) {
	d.roundRect(x1, y1, x2, y2, radius, fill, true)
}

func (d *Display) roundRect(x1, y1, x2, y2, radius int32, fill color.RGBA, filled bool) {
	if x2 < x1 || y2 < y1 {
		return
	}
	if radius < 0 {
		radius = 0
	}
	if maximum := (x2 - x1 + 1) / 2; radius > maximum {
		radius = maximum
	}
	if maximum := (y2 - y1 + 1) / 2; radius > maximum {
		radius = maximum
	}
	firstY := max(y1, int32(d.clip.Min.Y))
	lastY := min(y2, int32(d.clip.Max.Y-1))
	for y := firstY; y <= lastY; y++ {
		inset := int32(0)
		if radius > 0 {
			fromEdge := min(y-y1, y2-y)
			if fromEdge < radius {
				distance := radius - fromEdge
				for inset = radius; inset > 0 && (radius-inset+1)*(radius-inset+1)+distance*distance <= radius*radius; inset-- {
				}
			}
		}
		left, right := x1+inset, x2-inset
		if filled || y == y1 || y == y2 {
			d.FillRect(left, y, right, y, fill)
			continue
		}
		d.FillRect(left, y, left, y, fill)
		d.FillRect(right, y, right, y, fill)
	}
}

func (d *Display) DrawLine(x1, y1, x2, y2 int32, fill color.RGBA) {
	dx := x2 - x1
	if dx < 0 {
		dx = -dx
	}
	sx := int32(1)
	if x1 > x2 {
		sx = -1
	}
	dy := y2 - y1
	if dy < 0 {
		dy = -dy
	}
	sy := int32(1)
	if y1 > y2 {
		sy = -1
	}
	err := dx - dy
	for {
		if image.Pt(int(x1), int(y1)).In(d.clip) {
			d.working.SetRGBA(int(x1), int(y1), fill)
		}
		if x1 == x2 && y1 == y2 {
			break
		}
		twice := 2 * err
		if twice > -dy {
			err -= dy
			x1 += sx
		}
		if twice < dx {
			err += dx
			y1 += sy
		}
	}
}

func (d *Display) CopyImage(x, y int32, data []byte) error {
	return d.copyImageWithAlpha(x, y, data, 0, 0)
}

func (d *Display) CopyImageDir(x, y int32, data []byte, direction uint32) error {
	return d.copyImageWithAlpha(x, y, data, direction, 0)
}

func (d *Display) CopyImageEx(x, y int32, data []byte, alpha, direction uint32) error {
	return d.copyImageWithAlpha(x, y, data, direction, alpha)
}

func (d *Display) copyImageWithAlpha(x, y int32, data []byte, direction, alpha uint32) error {
	if direction > 1 {
		return fmt.Errorf("gnex32: unsupported CopyImage direction %d", direction)
	}
	if alpha > 3 {
		return fmt.Errorf("gnex32: unsupported CopyImage alpha %d", alpha)
	}
	if len(data) == 0 {
		return fmt.Errorf("gnex32: empty CopyImage resource")
	}
	var width, anchorX, anchorY int
	var pixels []byte
	var colors []color.RGBA
	transparentZero := false
	switch data[0] {
	case 2, 5:
		image, err := gnex.DecodeGNEX32MonoImage(data)
		if err != nil {
			return err
		}
		width, anchorX, anchorY = image.Width, image.AnchorX, image.AnchorY
		pixels = image.Pixels
		colors = []color.RGBA{monoColor(image.Color0), monoColor(image.Color1)}
		transparentZero = data[0] == 2
	case 0x06, 0x07, 0x09, 0x0a, 0x0b:
		image, err := gnex.DecodeGNEX32IndexedImage(data)
		if err != nil {
			return err
		}
		width, anchorX, anchorY = image.Width, image.AnchorX, image.AnchorY
		pixels = image.Pixels
		transparentZero = image.Mode == 1
		if len(image.PaletteIndices) != 0 {
			for _, index := range image.PaletteIndices {
				colors = append(colors, monoColor(index))
			}
		} else {
			for i := 0; i < len(image.PaletteTriplets); i += 3 {
				colors = append(colors, color.RGBA{R: image.PaletteTriplets[i], G: image.PaletteTriplets[i+1], B: image.PaletteTriplets[i+2], A: 255})
			}
		}
	default:
		return fmt.Errorf("gnex32: unsupported CopyImage kind %#x", data[0])
	}
	baseX := int(x) - anchorX
	baseY := int(y) - anchorY
	if direction == 1 {
		baseX = int(x) - (width - 1 - anchorX)
	}
	for index, paletteIndex := range pixels {
		if transparentZero && paletteIndex == 0 {
			continue
		}
		if colors[paletteIndex].A == 0 {
			continue
		}
		column := index % width
		if direction == 1 {
			column = width - 1 - column
		}
		destinationX := baseX + column
		destinationY := baseY + index/width
		if image.Pt(destinationX, destinationY).In(d.clip) {
			pixel := colors[paletteIndex]
			if alpha != 0 {
				old := d.working.RGBAAt(destinationX, destinationY)
				pixel = color.RGBA{
					R: uint8(((4-alpha)*uint32(pixel.R) + alpha*uint32(old.R)) / 4),
					G: uint8(((4-alpha)*uint32(pixel.G) + alpha*uint32(old.G)) / 4),
					B: uint8(((4-alpha)*uint32(pixel.B) + alpha*uint32(old.B)) / 4), A: 255,
				}
			}
			d.working.SetRGBA(destinationX, destinationY, pixel)
		}
	}
	return nil
}

func monoColor(value byte) color.RGBA {
	switch value {
	case 0:
		return color.RGBA{R: 255, G: 255, B: 255, A: 255}
	case 1:
		return color.RGBA{R: 192, G: 192, B: 192, A: 255}
	case 2:
		return color.RGBA{R: 128, G: 128, B: 128, A: 255}
	case 3:
		return color.RGBA{A: 255}
	case 4:
		return color.RGBA{}
	}
	if value >= 16 && value <= 139 {
		packed := int(value) - 15
		if value >= 47 {
			packed += 32
		}
		if value >= 56 {
			packed++
		}
		if value >= 78 {
			packed += 32
		}
		if value >= 96 {
			packed++
		}
		if value >= 109 {
			packed += 64
		}
		return color.RGBA{R: uint8((packed >> 5) * 255 / 7), G: uint8(((packed >> 2) & 7) * 255 / 7), B: uint8((packed & 3) * 255 / 3), A: 255}
	}
	return color.RGBA{A: 255}
}

func (d *Display) Flush() image.Image {
	d.latest = image.NewRGBA(d.working.Bounds())
	copy(d.latest.Pix, d.working.Pix)
	return d.latest
}

func (d *Display) SaveLCD() {
	d.saved = image.NewRGBA(d.working.Bounds())
	copy(d.saved.Pix, d.working.Pix)
}

func (d *Display) RestoreLCD() {
	if d.saved != nil {
		copy(d.working.Pix, d.saved.Pix)
	}
}

func (d *Display) Frame() image.Image {
	if d.latest == nil {
		return nil
	}
	return d.latest
}
