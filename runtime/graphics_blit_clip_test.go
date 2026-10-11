package runtime

import (
	"fmt"
	"math"
	"testing"
)

func installBlitSurface(t *testing.T, graphics *Graphics, current *surface) ServiceID {
	t.Helper()
	id, err := graphics.CreateSurface(1, current.descriptor)
	check(t, err)
	current.id, current.owner = id, 1
	graphics.surfaces[id] = current
	return id
}

// Compare stored bytes, stride padding and dirty bounds to the original
// snapshot-before-drawing implementation, including overlapping self copies.
func TestScaledBlitClippedFallbackMatchesReference(t *testing.T) {
	cases := []struct {
		name        string
		destination Rectangle
		state       SurfaceDrawState
	}{
		{"translated", Rectangle{X: -3, Y: -2, Width: 7, Height: 6}, SurfaceDrawState{Clip: Rectangle{X: 1, Y: 1, Width: 5, Height: 4}, TranslateX: 2, TranslateY: 1, GlobalAlpha: 255}},
		{"scaled up", Rectangle{X: -5, Y: -4, Width: 17, Height: 13}, SurfaceDrawState{Clip: Rectangle{X: 2, Y: 1, Width: 4, Height: 5}, GlobalAlpha: 128}},
		{"opaque scaled up", Rectangle{X: -3, Y: -2, Width: 13, Height: 11}, SurfaceDrawState{Clip: Rectangle{X: 1, Y: 1, Width: 6, Height: 5}, TranslateX: 2, TranslateY: 1, GlobalAlpha: 255}},
		{"opaque scaled down", Rectangle{X: 2, Y: 1, Width: 3, Height: 2}, SurfaceDrawState{Clip: Rectangle{Width: 8, Height: 7}, GlobalAlpha: 255}},
		{"zero global alpha", Rectangle{X: -1, Y: -1, Width: 11, Height: 9}, SurfaceDrawState{Clip: Rectangle{Width: 8, Height: 7}}},
		{"scaled down", Rectangle{X: -1, Y: 2, Width: 4, Height: 3}, SurfaceDrawState{Clip: Rectangle{Width: 8, Height: 7}, Raster: RasterXOR, GlobalAlpha: 255}},
		{"raster and", Rectangle{X: 4, Y: -1, Width: 7, Height: 6}, SurfaceDrawState{Clip: Rectangle{Width: 8, Height: 7}, Raster: RasterAND, GlobalAlpha: 211}},
		{"empty clip", Rectangle{Width: 7, Height: 6}, SurfaceDrawState{GlobalAlpha: 255}},
		{"outside", Rectangle{X: 20, Y: 30, Width: 7, Height: 6}, SurfaceDrawState{Clip: Rectangle{Width: 8, Height: 7}, GlobalAlpha: 255}},
		{"extreme translation", Rectangle{X: math.MinInt32, Y: math.MinInt32, Width: 7, Height: 6}, SurfaceDrawState{Clip: Rectangle{Width: 8, Height: 7}, TranslateX: math.MaxInt32, TranslateY: math.MaxInt32, GlobalAlpha: 255}},
		{"256 alpha", Rectangle{X: -2, Y: -1, Width: 13, Height: 11}, SurfaceDrawState{Clip: Rectangle{X: 1, Y: 1, Width: 6, Height: 5}, Raster: RasterOR, GlobalAlpha: 183, GlobalTransparency256: 117}},
		{"transparent 256", Rectangle{Width: 7, Height: 6}, SurfaceDrawState{Clip: Rectangle{Width: 8, Height: 7}, GlobalAlpha: 255, GlobalTransparency256: 256}},
	}
	for _, sourceFormat := range bulkFormats {
		for _, destinationFormat := range bulkFormats {
			for _, self := range []bool{false, true} {
				if self && sourceFormat != destinationFormat {
					continue
				}
				for _, keyed := range []bool{false, true} {
					for _, tc := range cases {
						graphics, err := NewGraphics(NewRegistry(8), GraphicsLimits{})
						check(t, err)
						destination := bulkSurface(t, 8, 7, 5, destinationFormat)
						destination.state = tc.state
						destination.dirty = Rectangle{X: 7, Y: 6, Width: 1, Height: 1}
						source := bulkSurface(t, 12, 10, 3, sourceFormat)
						if self {
							source = destination
						}
						if keyed {
							key := decodeSurfaceColor(source, 2, 2)
							source.descriptor.Transparent = &key
							if !self {
								destinationKey := decodeSurfaceColor(source, 3, 3)
								destination.descriptor.Transparent = &destinationKey
							}
							destination.state.Transparency = true
						}
						want := cloneSurface(destination)
						sourceCopy := cloneSurface(source)
						destinationID := installBlitSurface(t, graphics, destination)
						sourceID := destinationID
						if !self {
							sourceID = installBlitSurface(t, graphics, source)
						}
						sourceRectangle := Rectangle{X: 1, Y: 1, Width: 7, Height: 6}
						check(t, graphics.ScaledBlit(1, destinationID, sourceID, tc.destination, sourceRectangle))
						check(t, referenceScaledBlit(want, sourceCopy, tc.destination, sourceRectangle))
						compareSurfaces(t, fmt.Sprintf("%s source=%d destination=%d self=%v keyed=%v", tc.name, sourceFormat, destinationFormat, self, keyed), destination, want)
					}
				}
			}
		}
	}
}

func TestScaledBlitClippedAwayDoesNotAllocate(t *testing.T) {
	graphics, err := NewGraphics(NewRegistry(8), GraphicsLimits{})
	check(t, err)
	destination := bulkSurface(t, 8, 7, 0, PixelRGBA8888)
	source := bulkSurface(t, 12, 10, 0, PixelRGBA8888)
	key := RGB(1, 2, 3)
	source.descriptor.Transparent = &key // Exclude the existing row-copy path.
	destinationID := installBlitSurface(t, graphics, destination)
	sourceID := installBlitSurface(t, graphics, source)
	allocations := testing.AllocsPerRun(100, func() {
		check(t, graphics.ScaledBlit(1, destinationID, sourceID,
			Rectangle{X: 100, Y: 100, Width: 1200, Height: 1000},
			Rectangle{Width: 12, Height: 10}))
	})
	if allocations != 0 {
		t.Fatalf("clipped blit allocates %g times", allocations)
	}
}

func TestScaledBlitSeparateSurfacesDoNotAllocate(t *testing.T) {
	graphics, err := NewGraphics(NewRegistry(8), GraphicsLimits{})
	check(t, err)
	destination := bulkSurface(t, 8, 7, 0, PixelRGBA8888)
	source := bulkSurface(t, 12, 10, 0, PixelRGBA8888)
	destinationID := installBlitSurface(t, graphics, destination)
	sourceID := installBlitSurface(t, graphics, source)
	allocations := testing.AllocsPerRun(100, func() {
		check(t, graphics.ScaledBlit(1, destinationID, sourceID,
			Rectangle{X: -3, Y: -2, Width: 17, Height: 13},
			Rectangle{Width: 12, Height: 10}))
	})
	if allocations != 0 {
		t.Fatalf("separate-surface blit allocates %g times", allocations)
	}
}
