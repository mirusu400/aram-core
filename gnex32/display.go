package gnex32

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"

	"github.com/mirusu400/aram-core/loader/gnex"
	shared "github.com/mirusu400/aram-core/runtime"
)

// Display is the bounded GNEX drawing surface. Drawing coordinates are LCD
// coordinates; the guest receives swWidth and swHeight from the handset.
type Display struct {
	working *image.RGBA
	latest  *image.RGBA
	saved   *image.RGBA
	clip    image.Rectangle
	gamma   uint8
}

func NewDisplay(width, height int) *Display {
	bounds := image.Rect(0, 0, width, height)
	return &Display{working: image.NewRGBA(bounds), clip: bounds, gamma: 3}
}

func (d *Display) SetGamma(gamma uint32) error {
	if gamma > 6 {
		return fmt.Errorf("gnex32: gamma %d is outside 0..6", gamma)
	}
	d.gamma = uint8(gamma)
	return nil
}

func (d *Display) Gamma() uint8 { return d.gamma }

func (d *Display) paletteColor(value uint32) (color.RGBA, error) {
	if value > 181 {
		return color.RGBA{}, fmt.Errorf("gnex32: palette selector %d is outside 0..181", value)
	}
	if value == 4 {
		return color.RGBA{}, nil
	}
	packed, ok := shared.SKTGammaColor(d.gamma, uint8(value))
	if !ok {
		return color.RGBA{}, fmt.Errorf("gnex32: unsupported palette selector %d at gamma %d", value, d.gamma)
	}
	return shared.SKTPackedColor(packed), nil
}

func (d *Display) gammaColor(value color.RGBA) color.RGBA {
	mapped, _ := shared.SKTGammaRGBA(d.gamma, value)
	return mapped
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
	return d.copyImageTransformed(x, y, data, 0, 0, 0, 0)
}

func (d *Display) CopyImageDir(x, y int32, data []byte, direction uint32) error {
	return d.copyImageTransformed(x, y, data, 0, 0, direction, 0)
}

func (d *Display) CopyImageEx(x, y int32, data []byte, alpha, zoom, mirror, rotation uint32) error {
	return d.copyImageTransformed(x, y, data, alpha, zoom, mirror, rotation)
}

func (d *Display) copyImageTransformed(x, y int32, data []byte, alpha, zoom, mirror, rotation uint32) error {
	if mirror > 1 {
		return fmt.Errorf("gnex32: unsupported CopyImage mirror %d", mirror)
	}
	if alpha > 5 {
		return fmt.Errorf("gnex32: unsupported CopyImage alpha %d", alpha)
	}
	if zoom > 1 {
		return fmt.Errorf("gnex32: unsupported CopyImage zoom %d", zoom)
	}
	if rotation > 3 {
		return fmt.Errorf("gnex32: unsupported CopyImage rotation %d", rotation)
	}
	if len(data) == 0 {
		return fmt.Errorf("gnex32: empty CopyImage resource")
	}
	var width, height, anchorX, anchorY int
	var pixels []byte
	var colors []color.RGBA
	transparentZero := false
	switch data[0] {
	case 2, 5:
		image, err := gnex.DecodeGNEX32MonoImage(data)
		if err != nil {
			return err
		}
		width, height, anchorX, anchorY = image.Width, image.Height, image.AnchorX, image.AnchorY
		pixels = image.Pixels
		color0, err := d.paletteColor(uint32(image.Color0))
		if err != nil {
			return err
		}
		color1, err := d.paletteColor(uint32(image.Color1))
		if err != nil {
			return err
		}
		colors = []color.RGBA{color0, color1}
		transparentZero = data[0] == 2
	case 0x06, 0x07, 0x09, 0x0a, 0x0b:
		image, err := gnex.DecodeGNEX32IndexedImage(data)
		if err != nil {
			return err
		}
		width, height, anchorX, anchorY = image.Width, image.Height, image.AnchorX, image.AnchorY
		pixels = image.Pixels
		transparentZero = image.Mode == 1
		if len(image.PaletteIndices) != 0 {
			for _, index := range image.PaletteIndices {
				mapped, err := d.paletteColor(uint32(index))
				if err != nil {
					return err
				}
				colors = append(colors, mapped)
			}
		} else {
			for i := 0; i < len(image.PaletteTriplets); i += 3 {
				colors = append(colors, d.gammaColor(color.RGBA{
					R: image.PaletteTriplets[i], G: image.PaletteTriplets[i+1],
					B: image.PaletteTriplets[i+2], A: 255,
				}))
			}
		}
	default:
		return fmt.Errorf("gnex32: unsupported CopyImage kind %#x", data[0])
	}
	transformedAnchorX, transformedAnchorY := transformGNEX32ImagePoint(
		anchorX, anchorY, width, height, mirror, rotation,
	)
	scale := int(zoom) + 1
	baseX := int(x) - transformedAnchorX*scale
	baseY := int(y) - transformedAnchorY*scale
	for index, paletteIndex := range pixels {
		if transparentZero && paletteIndex == 0 {
			continue
		}
		if colors[paletteIndex].A == 0 {
			continue
		}
		column, row := index%width, index/width
		column, row = transformGNEX32ImagePoint(column, row, width, height, mirror, rotation)
		for zoomY := 0; zoomY < scale; zoomY++ {
			for zoomX := 0; zoomX < scale; zoomX++ {
				destinationX := baseX + column*scale + zoomX
				destinationY := baseY + row*scale + zoomY
				if image.Pt(destinationX, destinationY).In(d.clip) {
					pixel := blendGNEX32ImagePixel(colors[paletteIndex], d.working.RGBAAt(destinationX, destinationY), alpha)
					d.working.SetRGBA(destinationX, destinationY, pixel)
				}
			}
		}
	}
	return nil
}

func transformGNEX32ImagePoint(x, y, width, height int, mirror, rotation uint32) (int, int) {
	if mirror == 1 {
		x = width - 1 - x
	}
	switch rotation {
	case 1: // 90 degrees clockwise
		return height - 1 - y, x
	case 2:
		return width - 1 - x, height - 1 - y
	case 3: // 270 degrees clockwise
		return y, width - 1 - x
	default:
		return x, y
	}
}

func blendGNEX32ImagePixel(source, destination color.RGBA, alpha uint32) color.RGBA {
	switch alpha {
	case 1, 2, 3:
		sourceWeight := 4 - alpha
		return color.RGBA{
			R: uint8((sourceWeight*uint32(source.R) + alpha*uint32(destination.R)) / 4),
			G: uint8((sourceWeight*uint32(source.G) + alpha*uint32(destination.G)) / 4),
			B: uint8((sourceWeight*uint32(source.B) + alpha*uint32(destination.B)) / 4), A: 255,
		}
	case 4:
		return color.RGBA{R: ^source.R, G: ^source.G, B: ^source.B, A: 255}
	case 5:
		return color.RGBA{R: source.R ^ destination.R, G: source.G ^ destination.G, B: source.B ^ destination.B, A: 255}
	default:
		source.A = 255
		return source
	}
}

func monoColor(value byte) color.RGBA {
	if value == 4 {
		return color.RGBA{}
	}
	packed, ok := shared.SKTGammaColor(3, value)
	if !ok {
		return color.RGBA{A: 255}
	}
	return shared.SKTPackedColor(packed)
}

func (d *Display) Flush() image.Image {
	d.latest = image.NewRGBA(d.working.Bounds())
	copy(d.latest.Pix, d.working.Pix)
	return d.latest
}

// FlushPartial publishes an inclusive LCD rectangle while preserving the
// previously presented pixels outside it. A first partial flush starts from a
// cleared physical LCD.
func (d *Display) FlushPartial(x1, y1, x2, y2 int32) image.Image {
	if d.latest == nil {
		d.latest = image.NewRGBA(d.working.Bounds())
	}
	if x2 < x1 || y2 < y1 {
		return d.latest
	}
	rect := image.Rect(int(x1), int(y1), int(x2)+1, int(y2)+1).Intersect(d.working.Bounds())
	draw.Draw(d.latest, rect, d.working, rect.Min, draw.Src)
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
