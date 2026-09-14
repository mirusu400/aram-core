package runtime

// Bulk pixel work - a full-screen clear, a sprite blit, a full-frame copy -
// used to run entirely through drawSurfacePixel, which pays a translate, a
// bounds test, a clip test, a transparency-key comparison, a raster-operation
// switch, an alpha blend and a dirty-rectangle union for every single pixel.
// Most of that work is decided by surface and draw state, not by the pixel, so
// the helpers here decide it once per call. When a call turns out to be a
// plain copy - same format, no scaling, default draw state, no key, opaque
// source - the bytes move a row at a time with copy() and the touched
// rectangle joins the dirty region exactly once.
//
// Anything the fast path does not cover falls back to the per-pixel path
// unchanged. A raster operation, a narrowed clip, a live transparency key, a
// global alpha, a scaled blit and a format mismatch all still produce exactly
// the bytes they produced before.

// formatAlwaysOpaque reports whether decodeSurfaceColor returns A=0xff for
// every pixel of the format, so a copy can skip the alpha-blend path without
// inspecting the pixels.
func formatAlwaysOpaque(format PixelFormat) bool {
	switch format {
	case PixelXRGB8888, PixelBGRX8888, PixelRGB565, PixelRGB555, PixelGray8:
		return true
	default:
		return false
	}
}

// encodedPixel encodes one color in the surface's storage format.
// encodeSurfaceColor writes the same bytes wherever the pixel sits, so a fill
// encodes once instead of once per pixel.
func encodedPixel(current *surface, color Color) []byte {
	scratch := &surface{
		descriptor: current.descriptor,
		pixels:     make([]byte, current.descriptor.Format.BytesPerPixel()),
	}
	encodeSurfaceColor(scratch, 0, 0, color)
	return scratch.pixels
}

// fillSurface writes color over every pixel of the surface, leaving any stride
// padding untouched exactly as the per-pixel loop did. The first row is filled
// by doubling an encoded pixel and the remaining rows are copies of it.
func fillSurface(current *surface, color Color) {
	pixel := encodedPixel(current, color)
	if len(pixel) == 0 {
		return
	}
	width := int(current.descriptor.Width)
	height := int(current.descriptor.Height)
	stride := int(current.descriptor.Stride)
	rowBytes := width * len(pixel)
	row := current.pixels[:rowBytes]
	copy(row, pixel)
	for filled := len(pixel); filled < rowBytes; filled *= 2 {
		copy(row[filled:], row[:filled])
	}
	for y := 1; y < height; y++ {
		offset := y * stride
		copy(current.pixels[offset:offset+rowBytes], row)
	}
}

// markSurfaceDirty grows the dirty rectangle to cover one pixel. Once a fill
// or a line has plotted its first pixel most of the rest land inside the
// rectangle already, so the common case is four comparisons and
// Rectangle.Union only runs when the rectangle actually has to grow.
func markSurfaceDirty(current *surface, x, y int32) {
	dirty := current.dirty
	if dirty.Width > 0 && dirty.Height > 0 &&
		x >= dirty.X && y >= dirty.Y &&
		int64(x) < dirty.Right() && int64(y) < dirty.Bottom() {
		return
	}
	current.dirty = dirty.Union(Rectangle{X: x, Y: y, Width: 1, Height: 1})
}

// blitCopyable reports whether a blit is a plain copy: one whose result is the
// source bytes, unexamined, at the destination. Every condition is a property
// of the two surfaces, their draw state and the rectangles, so it is answered
// once per call rather than once per pixel.
func blitCopyable(
	destination, source *surface,
	destinationRectangle, sourceRectangle Rectangle,
) bool {
	if destination == source {
		// An overlapping self-blit reads the whole source before writing, which
		// row copies would not reproduce. It is rare enough to leave alone.
		return false
	}
	if destinationRectangle.Width != sourceRectangle.Width ||
		destinationRectangle.Height != sourceRectangle.Height {
		return false
	}
	format := source.descriptor.Format
	if format != destination.descriptor.Format || format == PixelIndexed8 {
		// Indexed storage is excluded because equal bytes only mean equal
		// colors when the palettes agree and every index is in range.
		return false
	}
	if source.descriptor.Transparent != nil {
		// ScaledBlit drops source pixels matching the source key regardless of
		// draw state, so a keyed source is never a plain copy.
		return false
	}
	if destination.state.Transparency && destination.descriptor.Transparent != nil {
		return false
	}
	if destination.state.Raster != RasterCopy ||
		destination.state.GlobalAlpha != 0xff || destination.state.GlobalTransparency256 != 0 {
		return false
	}
	return regionOpaque(source, sourceRectangle)
}

// regionOpaque reports whether every pixel of the region decodes to A=0xff.
// Formats that carry no alpha answer without reading memory; the two that do
// are scanned, which is one byte compare per pixel against the decode, blend
// and encode the per-pixel path would otherwise run.
func regionOpaque(current *surface, region Rectangle) bool {
	var alphaByte int
	switch current.descriptor.Format {
	case PixelRGBA8888:
		alphaByte = 3
	case PixelARGB8888:
		alphaByte = 0
	default:
		return formatAlwaysOpaque(current.descriptor.Format)
	}
	stride := int(current.descriptor.Stride)
	width := int(region.Width)
	for y := int(region.Y); y < int(region.Y)+int(region.Height); y++ {
		offset := y*stride + int(region.X)*4 + alphaByte
		for x := 0; x < width; x++ {
			if current.pixels[offset] != 0xff {
				return false
			}
			offset += 4
		}
	}
	return true
}

// blitFastPath copies an unscaled, unmodified region between two surfaces one
// row at a time. It reports whether it handled the call; when it reports
// false it has written nothing and the caller runs the per-pixel path.
func blitFastPath(
	destination, source *surface,
	destinationRectangle, sourceRectangle Rectangle,
) bool {
	if !blitCopyable(destination, source, destinationRectangle, sourceRectangle) {
		return false
	}
	originLeft := int64(destinationRectangle.X) + int64(destination.state.TranslateX)
	originTop := int64(destinationRectangle.Y) + int64(destination.state.TranslateY)
	clip := destination.state.Clip
	// Clipping only removes pixels the per-pixel path would have skipped, so
	// the intersection is taken here once instead of per pixel. Coordinates
	// stay in int64 because a translate can push the rectangle past int32,
	// which is exactly the range the surface bounds then exclude anyway.
	left := max(originLeft, max(0, int64(clip.X)))
	top := max(originTop, max(0, int64(clip.Y)))
	right := min(
		originLeft+int64(destinationRectangle.Width),
		min(int64(destination.descriptor.Width), clip.Right()),
	)
	bottom := min(
		originTop+int64(destinationRectangle.Height),
		min(int64(destination.descriptor.Height), clip.Bottom()),
	)
	if right <= left || bottom <= top {
		// Every pixel was clipped away. The per-pixel path would have written
		// nothing and left the dirty rectangle alone, so neither does this.
		return true
	}
	bytesPerPixel := source.descriptor.Format.BytesPerPixel()
	rowBytes := int(right-left) * bytesPerPixel
	sourceStride := int(source.descriptor.Stride)
	destinationStride := int(destination.descriptor.Stride)
	sourceLeft := int64(sourceRectangle.X) + left - originLeft
	sourceTop := int64(sourceRectangle.Y) + top - originTop
	for y := int64(0); y < bottom-top; y++ {
		sourceOffset := int(sourceTop+y)*sourceStride + int(sourceLeft)*bytesPerPixel
		destinationOffset := int(top+y)*destinationStride + int(left)*bytesPerPixel
		copy(
			destination.pixels[destinationOffset:destinationOffset+rowBytes],
			source.pixels[sourceOffset:sourceOffset+rowBytes],
		)
	}
	// Bits a format does not use are not part of the color. encodeSurfaceColor
	// wrote zeroes there; a raw row copy would carry the source's bits over,
	// and stored bytes are observable through Pixels and the save state.
	switch destination.descriptor.Format {
	case PixelXRGB8888:
		maskPixelByte(destination, left, top, right, bottom, 0, 0x00)
	case PixelBGRX8888:
		maskPixelByte(destination, left, top, right, bottom, 3, 0x00)
	case PixelRGB555:
		maskPixelByte(destination, left, top, right, bottom, 1, 0x7f)
	}
	destination.dirty = destination.dirty.Union(Rectangle{
		X:      int32(left),
		Y:      int32(top),
		Width:  int32(right - left),
		Height: int32(bottom - top),
	})
	return true
}

// maskPixelByte applies mask to one byte of every pixel in the region.
func maskPixelByte(
	current *surface,
	left, top, right, bottom int64,
	index int,
	mask byte,
) {
	bytesPerPixel := current.descriptor.Format.BytesPerPixel()
	stride := int(current.descriptor.Stride)
	for y := top; y < bottom; y++ {
		offset := int(y)*stride + int(left)*bytesPerPixel + index
		for x := left; x < right; x++ {
			current.pixels[offset] &= mask
			offset += bytesPerPixel
		}
	}
}
