package runtime

import (
	"bytes"
	"testing"
)

// The tests below pin the bulk row-copy paths to the per-pixel path they
// replace. Both paths write the same bytes by construction, which is the point
// and also the difficulty: an output comparison alone cannot tell which one
// ran, so every case also asserts the fast-path counter.

var bulkFormats = []PixelFormat{
	PixelRGBA8888,
	PixelARGB8888,
	PixelXRGB8888,
	PixelBGRX8888,
	PixelRGB565,
	PixelRGB555,
	PixelGray8,
	PixelIndexed8,
}

func bulkPalette(format PixelFormat) []Color {
	if format != PixelIndexed8 {
		return nil
	}
	palette := make([]Color, 256)
	for index := range palette {
		palette[index] = Color{
			R: uint8(index),
			G: uint8(255 - index),
			B: uint8(index * 3),
			A: 0xff,
		}
	}
	return palette
}

func bulkSurface(
	tb testing.TB,
	width, height, padding int32,
	format PixelFormat,
) *surface {
	tb.Helper()
	stride := width*int32(format.BytesPerPixel()) + padding
	current := &surface{
		descriptor: SurfaceDescriptor{
			Width:   width,
			Height:  height,
			Stride:  stride,
			Format:  format,
			Palette: bulkPalette(format),
		},
		state:  defaultDrawState(width, height),
		pixels: make([]byte, int(stride)*int(height)),
	}
	for index := range current.pixels {
		current.pixels[index] = byte(index*37 + 11)
	}
	return current
}

// makeOpaque sets the alpha byte of every pixel that has one, walking rows so
// stride padding is not mistaken for pixel data.
func makeOpaque(current *surface) {
	var alphaByte int
	switch current.descriptor.Format {
	case PixelRGBA8888:
		alphaByte = 3
	case PixelARGB8888:
		alphaByte = 0
	default:
		return
	}
	stride := int(current.descriptor.Stride)
	for y := 0; y < int(current.descriptor.Height); y++ {
		for x := 0; x < int(current.descriptor.Width); x++ {
			current.pixels[y*stride+x*4+alphaByte] = 0xff
		}
	}
}

func cloneSurface(current *surface) *surface {
	copied := *current
	copied.descriptor = cloneDescriptor(current.descriptor)
	copied.pixels = cloneBytes(current.pixels)
	return &copied
}

// referenceClear is the per-pixel Clear that fillSurface replaced.
func referenceClear(current *surface, color Color) {
	for y := int32(0); y < current.descriptor.Height; y++ {
		for x := int32(0); x < current.descriptor.Width; x++ {
			encodeSurfaceColor(current, x, y, color)
		}
	}
	current.dirty = Rectangle{
		Width:  current.descriptor.Width,
		Height: current.descriptor.Height,
	}
}

// referenceScaledBlit is the per-pixel blit that blitFastPath short-circuits.
// It is kept here verbatim so a fast-path change is measured against the
// behavior that shipped, not against a later edit of the production loop.
func referenceScaledBlit(
	destination, source *surface,
	destinationRectangle, sourceRectangle Rectangle,
) error {
	count := int(destinationRectangle.Width) * int(destinationRectangle.Height)
	colors := make([]Color, count)
	for y := int32(0); y < destinationRectangle.Height; y++ {
		sourceY := sourceRectangle.Y +
			int32(int64(y)*int64(sourceRectangle.Height)/int64(destinationRectangle.Height))
		for x := int32(0); x < destinationRectangle.Width; x++ {
			sourceX := sourceRectangle.X +
				int32(int64(x)*int64(sourceRectangle.Width)/int64(destinationRectangle.Width))
			colors[int64(y)*int64(destinationRectangle.Width)+int64(x)] =
				decodeSurfaceColor(source, sourceX, sourceY)
		}
	}
	for y := int32(0); y < destinationRectangle.Height; y++ {
		for x := int32(0); x < destinationRectangle.Width; x++ {
			color := colors[int64(y)*int64(destinationRectangle.Width)+int64(x)]
			if source.descriptor.Transparent != nil && color == *source.descriptor.Transparent {
				continue
			}
			if err := drawSurfacePixel(
				destination,
				destinationRectangle.X+x,
				destinationRectangle.Y+y,
				color,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func compareSurfaces(tb testing.TB, label string, got, want *surface) {
	tb.Helper()
	if !bytes.Equal(got.pixels, want.pixels) {
		for index := range want.pixels {
			if got.pixels[index] != want.pixels[index] {
				tb.Errorf(
					"%s: pixel byte %d is 0x%02x, want 0x%02x",
					label,
					index,
					got.pixels[index],
					want.pixels[index],
				)
				break
			}
		}
	}
	if got.dirty != want.dirty {
		tb.Errorf("%s: dirty is %+v, want %+v", label, got.dirty, want.dirty)
	}
}

func TestClearMatchesPerPixelEncode(t *testing.T) {
	for _, format := range bulkFormats {
		for _, padding := range []int32{0, 5} {
			fast := bulkSurface(t, 7, 5, padding, format)
			slow := cloneSurface(fast)
			color := Color{R: 0x21, G: 0x87, B: 0xc3, A: 0xff}
			fillSurface(fast, color)
			fast.dirty = Rectangle{
				Width:  fast.descriptor.Width,
				Height: fast.descriptor.Height,
			}
			referenceClear(slow, color)
			compareSurfaces(t, "clear", fast, slow)
		}
	}
}

// TestClearLeavesStridePaddingAlone guards the one thing a row-doubling fill
// could get wrong that a same-stride comparison would hide.
func TestClearLeavesStridePaddingAlone(t *testing.T) {
	current := bulkSurface(t, 4, 3, 6, PixelRGBA8888)
	padding := make([]byte, 0, 18)
	stride := int(current.descriptor.Stride)
	for y := 0; y < 3; y++ {
		padding = append(padding, current.pixels[y*stride+16:y*stride+stride]...)
	}
	fillSurface(current, RGB(1, 2, 3))
	for y := 0; y < 3; y++ {
		got := current.pixels[y*stride+16 : y*stride+stride]
		want := padding[y*6 : y*6+6]
		if !bytes.Equal(got, want) {
			t.Fatalf("row %d padding changed: %v, want %v", y, got, want)
		}
	}
}

func TestClearTakesFastPath(t *testing.T) {
	graphics, err := NewGraphics(NewRegistry(8), GraphicsLimits{})
	check(t, err)
	id, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 16, Height: 16, Format: PixelRGB565,
	})
	check(t, err)
	before := graphics.bulkFastPathCalls
	check(t, graphics.Clear(1, id, RGB(9, 9, 9)))
	if graphics.bulkFastPathCalls != before+1 {
		t.Fatalf("Clear did not take the bulk path")
	}
}

type blitCase struct {
	name  string
	state func(state *SurfaceDrawState)
	// sourceKey and destinationKey install a transparency key on the
	// respective surface descriptor.
	sourceKey      *Color
	destinationKey *Color
	destination    Rectangle
	source         Rectangle
	sourceFormat   PixelFormat
	sourceAlpha    uint8
	fast           bool
}

func TestScaledBlitMatchesPerPixelPath(t *testing.T) {
	key := Color{R: 0x11, G: 0x22, B: 0x33, A: 0xff}
	cases := []blitCase{
		{
			name: "plain copy",
			fast: true,
		},
		{
			name: "translated",
			state: func(state *SurfaceDrawState) {
				state.TranslateX, state.TranslateY = 3, 2
			},
			fast: true,
		},
		{
			name: "partly outside the surface",
			destination: Rectangle{
				X: 20, Y: 18, Width: 8, Height: 8,
			},
			fast: true,
		},
		{
			name: "entirely outside the surface",
			destination: Rectangle{
				X: 200, Y: 200, Width: 8, Height: 8,
			},
			fast: true,
		},
		{
			name: "narrow clip",
			state: func(state *SurfaceDrawState) {
				state.Clip = Rectangle{X: 4, Y: 4, Width: 6, Height: 6}
			},
			fast: true,
		},
		{
			name: "empty clip",
			state: func(state *SurfaceDrawState) {
				state.Clip = Rectangle{}
			},
			fast: true,
		},
		{
			name: "raster xor",
			state: func(state *SurfaceDrawState) {
				state.Raster = RasterXOR
			},
		},
		{
			name: "raster and",
			state: func(state *SurfaceDrawState) {
				state.Raster = RasterAND
			},
		},
		{
			name: "raster or",
			state: func(state *SurfaceDrawState) {
				state.Raster = RasterOR
			},
		},
		{
			name: "global alpha",
			state: func(state *SurfaceDrawState) {
				state.GlobalAlpha = 0x80
			},
		},
		{
			name:      "source transparency key",
			sourceKey: &key,
		},
		{
			name:           "destination transparency key",
			destinationKey: &key,
			state: func(state *SurfaceDrawState) {
				state.Transparency = true
			},
		},
		{
			name:           "destination key with transparency off",
			destinationKey: &key,
			fast:           true,
		},
		{
			name:        "scaled up",
			destination: Rectangle{X: 2, Y: 2, Width: 16, Height: 16},
		},
		{
			name:        "scaled down",
			destination: Rectangle{X: 2, Y: 2, Width: 4, Height: 4},
		},
		{
			name:         "format mismatch",
			sourceFormat: PixelRGB565,
		},
		{
			name:        "translucent source",
			sourceAlpha: 0x40,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			destinationRectangle := testCase.destination
			if destinationRectangle.Empty() {
				destinationRectangle = Rectangle{X: 2, Y: 2, Width: 8, Height: 8}
			}
			sourceRectangle := testCase.source
			if sourceRectangle.Empty() {
				sourceRectangle = Rectangle{X: 1, Y: 1, Width: 8, Height: 8}
			}
			sourceFormat := testCase.sourceFormat
			if sourceFormat == 0 {
				sourceFormat = PixelRGBA8888
			}
			destination := bulkSurface(t, 24, 24, 4, PixelRGBA8888)
			source := bulkSurface(t, 12, 12, 3, sourceFormat)
			makeOpaque(source)
			if testCase.sourceAlpha != 0 && sourceFormat == PixelRGBA8888 {
				source.pixels[(1*int(source.descriptor.Stride))+1*4+3] = testCase.sourceAlpha
			}
			if testCase.sourceKey != nil {
				keyed := *testCase.sourceKey
				source.descriptor.Transparent = &keyed
				encodeSurfaceColor(source, sourceRectangle.X, sourceRectangle.Y, keyed)
			}
			if testCase.destinationKey != nil {
				keyed := *testCase.destinationKey
				destination.descriptor.Transparent = &keyed
				encodeSurfaceColor(
					source,
					sourceRectangle.X+1,
					sourceRectangle.Y,
					keyed,
				)
			}
			if testCase.state != nil {
				testCase.state(&destination.state)
			}
			slow := cloneSurface(destination)
			slowSource := cloneSurface(source)

			took := blitFastPath(
				destination,
				source,
				destinationRectangle,
				sourceRectangle,
			)
			if took != testCase.fast {
				t.Fatalf("fast path taken = %v, want %v", took, testCase.fast)
			}
			if !took {
				return
			}
			check(t, referenceScaledBlit(
				slow,
				slowSource,
				destinationRectangle,
				sourceRectangle,
			))
			compareSurfaces(t, testCase.name, destination, slow)
		})
	}
}

// TestScaledBlitFastPathAcrossFormats checks the row copy against the
// per-pixel path for every storage format, including the two that carry an
// unused padding byte the per-pixel path zeroed.
func TestScaledBlitFastPathAcrossFormats(t *testing.T) {
	for _, format := range bulkFormats {
		for _, padding := range []int32{0, 3} {
			destination := bulkSurface(t, 16, 12, padding, format)
			source := bulkSurface(t, 16, 12, padding, format)
			makeOpaque(source)
			slow := cloneSurface(destination)
			destinationRectangle := Rectangle{X: 1, Y: 2, Width: 9, Height: 7}
			sourceRectangle := Rectangle{X: 3, Y: 1, Width: 9, Height: 7}
			took := blitFastPath(
				destination,
				source,
				destinationRectangle,
				sourceRectangle,
			)
			// Indexed storage never takes the fast path: equal bytes only mean
			// equal colors when the palettes agree and every index is in range.
			if want := format != PixelIndexed8; took != want {
				t.Fatalf("format %d: fast path taken = %v, want %v", format, took, want)
			}
			if !took {
				continue
			}
			check(t, referenceScaledBlit(
				slow,
				source,
				destinationRectangle,
				sourceRectangle,
			))
			compareSurfaces(t, "format copy", destination, slow)
		}
	}
}

// TestScaledBlitSelfBlitUsesPerPixelPath pins the one correctness hazard of a
// row copy: a surface blitted onto itself is read whole before it is written.
func TestScaledBlitSelfBlitUsesPerPixelPath(t *testing.T) {
	graphics, err := NewGraphics(NewRegistry(8), GraphicsLimits{})
	check(t, err)
	id, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 8, Height: 8, Format: PixelRGBA8888,
	})
	check(t, err)
	current, err := graphics.get(id, 1)
	check(t, err)
	for index := range current.pixels {
		current.pixels[index] = byte(index)
	}
	for index := 3; index < len(current.pixels); index += 4 {
		current.pixels[index] = 0xff
	}
	expected := cloneSurface(current)
	before := graphics.bulkFastPathCalls
	check(t, graphics.Blit(1, id, id, 0, 1, Rectangle{Width: 8, Height: 7}))
	if graphics.bulkFastPathCalls != before {
		t.Fatalf("self blit took the bulk path")
	}
	check(t, referenceScaledBlit(
		expected,
		cloneSurface(expected),
		Rectangle{Y: 1, Width: 8, Height: 7},
		Rectangle{Width: 8, Height: 7},
	))
	compareSurfaces(t, "self blit", current, expected)
}

func TestBlitFastPathCounter(t *testing.T) {
	graphics, err := NewGraphics(NewRegistry(8), GraphicsLimits{})
	check(t, err)
	destination, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 32, Height: 32, Format: PixelRGB565,
	})
	check(t, err)
	source, err := graphics.CreateSurface(1, SurfaceDescriptor{
		Width: 16, Height: 16, Format: PixelRGB565,
	})
	check(t, err)
	before := graphics.bulkFastPathCalls
	check(t, graphics.Blit(1, destination, source, 0, 0, Rectangle{Width: 16, Height: 16}))
	if graphics.bulkFastPathCalls != before+1 {
		t.Fatalf("unscaled blit did not take the bulk path")
	}
	check(t, graphics.ScaledBlit(
		1,
		destination,
		source,
		Rectangle{Width: 32, Height: 32},
		Rectangle{Width: 16, Height: 16},
	))
	if graphics.bulkFastPathCalls != before+1 {
		t.Fatalf("scaled blit took the bulk path")
	}
}

// TestMarkSurfaceDirtyMatchesUnion checks the containment short-circuit
// against the union it skips.
func TestMarkSurfaceDirtyMatchesUnion(t *testing.T) {
	starts := []Rectangle{
		{},
		{X: 4, Y: 4, Width: 1, Height: 1},
		{X: 2, Y: 3, Width: 6, Height: 5},
	}
	for _, start := range starts {
		for y := int32(0); y < 12; y++ {
			for x := int32(0); x < 12; x++ {
				current := &surface{dirty: start}
				markSurfaceDirty(current, x, y)
				want := start.Union(Rectangle{X: x, Y: y, Width: 1, Height: 1})
				if current.dirty != want {
					t.Fatalf(
						"start %+v pixel (%d,%d): dirty %+v, want %+v",
						start,
						x,
						y,
						current.dirty,
						want,
					)
				}
			}
		}
	}
}
