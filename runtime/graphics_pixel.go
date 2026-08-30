package runtime

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
)

func (g *Graphics) get(id ServiceID, owner OwnerID) (*surface, error) {
	if err := g.registry.Validate(id, owner, KindSurface); err != nil {
		return nil, err
	}
	current := g.surfaces[id]
	if current == nil {
		return nil, fmt.Errorf("%w: surface %s", ErrInvalidState, id)
	}
	return current, nil
}

func cloneDescriptor(descriptor SurfaceDescriptor) SurfaceDescriptor {
	descriptor.Palette = append([]Color(nil), descriptor.Palette...)
	if descriptor.Transparent != nil {
		color := *descriptor.Transparent
		descriptor.Transparent = &color
	}
	return descriptor
}

func surfaceContains(current *surface, x, y int32) bool {
	return x >= 0 && y >= 0 &&
		x < current.descriptor.Width && y < current.descriptor.Height
}

func drawSurfacePixel(current *surface, x, y int32, color Color) error {
	translatedX := int64(x) + int64(current.state.TranslateX)
	translatedY := int64(y) + int64(current.state.TranslateY)
	if translatedX < math.MinInt32 || translatedX > math.MaxInt32 ||
		translatedY < math.MinInt32 || translatedY > math.MaxInt32 {
		return nil
	}
	x = int32(translatedX)
	y = int32(translatedY)
	if !surfaceContains(current, x, y) ||
		x < current.state.Clip.X || y < current.state.Clip.Y ||
		int64(x) >= current.state.Clip.Right() ||
		int64(y) >= current.state.Clip.Bottom() {
		return nil
	}
	if current.state.Transparency &&
		current.descriptor.Transparent != nil &&
		color == *current.descriptor.Transparent {
		return nil
	}
	destination := decodeSurfaceColor(current, x, y)
	color = applyRaster(current.state.Raster, destination, color)
	if current.state.GlobalAlpha != 0xff {
		color.A = uint8(uint16(color.A) * uint16(current.state.GlobalAlpha) / 0xff)
	}
	if color.A != 0xff {
		color = blendColor(destination, color)
	}
	encodeSurfaceColor(current, x, y, color)
	current.dirty = current.dirty.Union(Rectangle{X: x, Y: y, Width: 1, Height: 1})
	return nil
}

func applyRaster(operation RasterOperation, destination, source Color) Color {
	switch operation {
	case RasterXOR:
		return Color{
			R: destination.R ^ source.R,
			G: destination.G ^ source.G,
			B: destination.B ^ source.B,
			A: source.A,
		}
	case RasterAND:
		return Color{
			R: destination.R & source.R,
			G: destination.G & source.G,
			B: destination.B & source.B,
			A: source.A,
		}
	case RasterOR:
		return Color{
			R: destination.R | source.R,
			G: destination.G | source.G,
			B: destination.B | source.B,
			A: source.A,
		}
	default:
		return source
	}
}

func blendColor(destination, source Color) Color {
	alpha := uint32(source.A)
	inverse := uint32(0xff) - alpha
	return Color{
		R: uint8((uint32(source.R)*alpha + uint32(destination.R)*inverse + 127) / 255),
		G: uint8((uint32(source.G)*alpha + uint32(destination.G)*inverse + 127) / 255),
		B: uint8((uint32(source.B)*alpha + uint32(destination.B)*inverse + 127) / 255),
		A: uint8(alpha + (uint32(destination.A)*inverse+127)/255),
	}
}

func pixelOffset(current *surface, x, y int32) int {
	return int(y)*int(current.descriptor.Stride) +
		int(x)*current.descriptor.Format.BytesPerPixel()
}

func surfaceRGBA(current *surface) ([]byte, error) {
	return surfaceRGBAInto(current, nil)
}

// surfaceRGBARowsInto converts the rows in [top, bottom) and returns them
// packed from the start of the destination, so a caller that only changed a
// band of the surface converts a band instead of the whole thing.
func surfaceRGBARowsInto(
	current *surface,
	top, bottom int,
	destination []byte,
) ([]byte, error) {
	height := int(current.descriptor.Height)
	top = max(top, 0)
	bottom = min(bottom, height)
	if top >= bottom {
		return destination[:0], nil
	}
	width := int(current.descriptor.Width)
	rows := bottom - top
	if uint64(width)*uint64(rows) > uint64(math.MaxInt/4) {
		return nil, fmt.Errorf("%w: RGBA conversion exceeds host address space", ErrLimitExceeded)
	}
	size := width * rows * 4
	rgba := destination
	if cap(destination) < size {
		rgba = make([]byte, size)
	} else {
		rgba = destination[:size]
	}
	stride := int(current.descriptor.Stride)
	switch current.descriptor.Format {
	case PixelRGB565:
		for y := top; y < bottom; y++ {
			row := y * stride
			offset := (y - top) * width * 4
			for x := 0; x < width; x++ {
				value := binary.LittleEndian.Uint16(current.pixels[row+x*2:])
				rgba[offset+0] = expand5(uint8((value >> 11) & 0x1f))
				rgba[offset+1] = expand6(uint8((value >> 5) & 0x3f))
				rgba[offset+2] = expand5(uint8(value & 0x1f))
				rgba[offset+3] = 0xff
				offset += 4
			}
		}
		return rgba, nil
	case PixelRGBA8888:
		for y := top; y < bottom; y++ {
			row := y * stride
			offset := (y - top) * width * 4
			copy(rgba[offset:offset+width*4], current.pixels[row:row+width*4])
		}
		return rgba, nil
	}
	for y := top; y < bottom; y++ {
		for x := 0; x < width; x++ {
			color := decodeSurfaceColor(current, int32(x), int32(y))
			offset := ((y-top)*width + x) * 4
			rgba[offset+0] = color.R
			rgba[offset+1] = color.G
			rgba[offset+2] = color.B
			rgba[offset+3] = color.A
		}
	}
	return rgba, nil
}

func surfaceRGBAInto(current *surface, destination []byte) ([]byte, error) {
	pixelCount := uint64(current.descriptor.Width) * uint64(current.descriptor.Height)
	if pixelCount > uint64(math.MaxInt/4) {
		return nil, fmt.Errorf("%w: RGBA conversion exceeds host address space", ErrLimitExceeded)
	}
	size := int(pixelCount) * 4
	var rgba []byte
	if cap(destination) < size {
		rgba = make([]byte, size)
	} else {
		rgba = destination[:size]
	}
	width := int(current.descriptor.Width)
	height := int(current.descriptor.Height)
	stride := int(current.descriptor.Stride)
	// The format is a property of the surface, not of the pixel, so it is
	// decided once here instead of inside decodeSurfaceColor for every pixel of
	// every presented frame. The two cases below are the ones the handset
	// profiles use - RGB565 is the KTF screen and RGBA8888 needs no conversion
	// at all - and both compute exactly what the general loop computed.
	switch current.descriptor.Format {
	case PixelRGB565:
		for y := 0; y < height; y++ {
			row := y * stride
			offset := y * width * 4
			for x := 0; x < width; x++ {
				value := binary.LittleEndian.Uint16(current.pixels[row+x*2:])
				rgba[offset+0] = expand5(uint8((value >> 11) & 0x1f))
				rgba[offset+1] = expand6(uint8((value >> 5) & 0x3f))
				rgba[offset+2] = expand5(uint8(value & 0x1f))
				rgba[offset+3] = 0xff
				offset += 4
			}
		}
		return rgba, nil
	case PixelRGBA8888:
		for y := 0; y < height; y++ {
			row := y * stride
			offset := y * width * 4
			copy(rgba[offset:offset+width*4], current.pixels[row:row+width*4])
		}
		return rgba, nil
	}
	for y := int32(0); y < current.descriptor.Height; y++ {
		for x := int32(0); x < current.descriptor.Width; x++ {
			color := decodeSurfaceColor(current, x, y)
			offset := (int(y)*int(current.descriptor.Width) + int(x)) * 4
			rgba[offset+0] = color.R
			rgba[offset+1] = color.G
			rgba[offset+2] = color.B
			rgba[offset+3] = color.A
		}
	}
	return rgba, nil
}

func decodeSurfaceColor(current *surface, x, y int32) Color {
	offset := pixelOffset(current, x, y)
	switch current.descriptor.Format {
	case PixelRGBA8888:
		return Color{
			R: current.pixels[offset],
			G: current.pixels[offset+1],
			B: current.pixels[offset+2],
			A: current.pixels[offset+3],
		}
	case PixelARGB8888:
		return Color{
			A: current.pixels[offset],
			R: current.pixels[offset+1],
			G: current.pixels[offset+2],
			B: current.pixels[offset+3],
		}
	case PixelXRGB8888:
		return Color{
			R: current.pixels[offset+1],
			G: current.pixels[offset+2],
			B: current.pixels[offset+3],
			A: 0xff,
		}
	case PixelBGRX8888:
		return Color{
			R: current.pixels[offset+2],
			G: current.pixels[offset+1],
			B: current.pixels[offset],
			A: 0xff,
		}
	case PixelRGB565:
		value := binary.LittleEndian.Uint16(current.pixels[offset:])
		return Color{
			R: expand5(uint8((value >> 11) & 0x1f)),
			G: expand6(uint8((value >> 5) & 0x3f)),
			B: expand5(uint8(value & 0x1f)),
			A: 0xff,
		}
	case PixelRGB555:
		value := binary.LittleEndian.Uint16(current.pixels[offset:])
		return Color{
			R: expand5(uint8((value >> 10) & 0x1f)),
			G: expand5(uint8((value >> 5) & 0x1f)),
			B: expand5(uint8(value & 0x1f)),
			A: 0xff,
		}
	case PixelGray8:
		value := current.pixels[offset]
		return Color{R: value, G: value, B: value, A: 0xff}
	case PixelIndexed8:
		index := int(current.pixels[offset])
		if index < len(current.descriptor.Palette) {
			return current.descriptor.Palette[index]
		}
	}
	return Color{}
}

func encodeSurfaceColor(current *surface, x, y int32, color Color) {
	offset := pixelOffset(current, x, y)
	switch current.descriptor.Format {
	case PixelRGBA8888:
		current.pixels[offset+0] = color.R
		current.pixels[offset+1] = color.G
		current.pixels[offset+2] = color.B
		current.pixels[offset+3] = color.A
	case PixelARGB8888:
		current.pixels[offset+0] = color.A
		current.pixels[offset+1] = color.R
		current.pixels[offset+2] = color.G
		current.pixels[offset+3] = color.B
	case PixelXRGB8888:
		current.pixels[offset+0] = 0
		current.pixels[offset+1] = color.R
		current.pixels[offset+2] = color.G
		current.pixels[offset+3] = color.B
	case PixelBGRX8888:
		current.pixels[offset+0] = color.B
		current.pixels[offset+1] = color.G
		current.pixels[offset+2] = color.R
		current.pixels[offset+3] = 0
	case PixelRGB565:
		value := uint16(color.R>>3)<<11 |
			uint16(color.G>>2)<<5 |
			uint16(color.B>>3)
		binary.LittleEndian.PutUint16(current.pixels[offset:], value)
	case PixelRGB555:
		value := uint16(color.R>>3)<<10 |
			uint16(color.G>>3)<<5 |
			uint16(color.B>>3)
		binary.LittleEndian.PutUint16(current.pixels[offset:], value)
	case PixelGray8:
		current.pixels[offset] = uint8(
			(uint32(color.R)*77 + uint32(color.G)*150 + uint32(color.B)*29 + 128) >> 8,
		)
	case PixelIndexed8:
		current.pixels[offset] = nearestPalette(current.descriptor.Palette, color)
	}
}

func nearestPalette(palette []Color, color Color) uint8 {
	best := 0
	bestDistance := uint64(math.MaxUint64)
	for index, candidate := range palette {
		red := int64(candidate.R) - int64(color.R)
		green := int64(candidate.G) - int64(color.G)
		blue := int64(candidate.B) - int64(color.B)
		alpha := int64(candidate.A) - int64(color.A)
		distance := uint64(red*red + green*green + blue*blue + alpha*alpha)
		if distance < bestDistance {
			best = index
			bestDistance = distance
		}
	}
	return uint8(best)
}

func expand5(value uint8) uint8 {
	return value<<3 | value>>2
}

func expand6(value uint8) uint8 {
	return value<<2 | value>>4
}

func abs64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func ellipseContains(
	dx, dy int64,
	radiusXSquared, radiusYSquared uint64,
) bool {
	dxSquared := uint64(dx * dx)
	dySquared := uint64(dy * dy)
	leftHigh, leftLow := bits.Mul64(dxSquared, radiusYSquared)
	rightHigh, rightLow := bits.Mul64(dySquared, radiusXSquared)
	sumLow, carry := bits.Add64(leftLow, rightLow, 0)
	sumHigh, _ := bits.Add64(leftHigh, rightHigh, carry)
	limitHigh, limitLow := bits.Mul64(radiusXSquared, radiusYSquared)
	return sumHigh < limitHigh ||
		sumHigh == limitHigh && sumLow <= limitLow
}

func PointInArc(
	dx, dy int64,
	startDegrees, sweepDegrees int32,
) bool {
	sweep := int64(sweepDegrees)
	if sweep >= 360 || sweep <= -360 {
		return true
	}
	if sweep == 0 {
		return false
	}
	if dx == 0 && dy == 0 {
		return true
	}
	absoluteX, absoluteY := abs64(dx), abs64(dy)
	angle := absoluteY * 90 / (absoluteX + absoluteY)
	switch {
	case dx < 0 && dy >= 0:
		angle = 180 - angle
	case dx < 0 && dy < 0:
		angle = 180 + angle
	case dx >= 0 && dy < 0:
		angle = 360 - angle
	}
	normalize := func(value int64) int64 {
		value %= 360
		if value < 0 {
			value += 360
		}
		return value
	}
	begin := normalize(int64(startDegrees))
	end := normalize(int64(startDegrees) + sweep)
	angle = normalize(angle)
	if sweep > 0 {
		if begin <= end {
			return angle >= begin && angle <= end
		}
		return angle >= begin || angle <= end
	}
	if end <= begin {
		return angle >= end && angle <= begin
	}
	return angle >= end || angle <= begin
}
