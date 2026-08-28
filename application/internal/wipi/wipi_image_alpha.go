package wipi

// wipiImageAlpha is the transparency of one decoded MC_GrpImage frame, one bit
// per pixel, set where the source pixel is fully transparent.
//
// MC_grpDrawImage composites an image's transparency; MC_grpCopyFrameBuffer,
// which is what the draw is implemented on top of, does not. The image's own
// framebuffer is 16bpp with no alpha channel, so painting a frame into it drops
// the transparency entirely and every sprite used to blit as a solid
// rectangle — 무한신맞고2009's sheets reserve magenta for their background and
// the whole dialog came out magenta (issue #76).
//
// frame records which frame of an animation the mask was built from, so a
// title stepping through frames rebuilds it exactly once per frame rather than
// once per draw.
type wipiImageAlpha struct {
	mask   []uint64
	width  int
	height int
	frame  uint32
}

// transparentAt reports whether the source pixel at x, y must be left out of a
// blit. A frame with no transparent pixel carries no mask and answers false
// everywhere.
func (alpha *wipiImageAlpha) transparentAt(x, y int) bool {
	if alpha == nil || len(alpha.mask) == 0 {
		return false
	}
	if x < 0 || y < 0 || x >= alpha.width || y >= alpha.height {
		return false
	}
	index := y*alpha.width + x
	return alpha.mask[index>>6]&(1<<(index&63)) != 0
}

// imageAlphaFor returns the transparency of the image's current frame,
// building it from the decoded asset the first time it is needed. Building it
// lazily rather than at paint time means a restored state recovers it too: the
// asset is restored alongside the image, and the mask is derived, not saved.
func (r *Runtime) imageAlphaFor(
	handle uint32,
	descriptor wipiImageDescriptor,
) *wipiImageAlpha {
	if cached, ok := r.imageAlpha[handle]; ok &&
		cached.frame == descriptor.frameIndex {
		return cached
	}
	alpha := r.buildImageAlpha(handle, descriptor)
	if r.imageAlpha == nil {
		r.imageAlpha = make(map[uint32]*wipiImageAlpha)
	}
	r.imageAlpha[handle] = alpha
	return alpha
}

// buildImageAlpha scans the decoded frame the image was painted from. An image
// whose asset, surface or alpha is unavailable is reported fully opaque, which
// is the behaviour the blit had before masks existed.
func (r *Runtime) buildImageAlpha(
	handle uint32,
	descriptor wipiImageDescriptor,
) *wipiImageAlpha {
	opaque := &wipiImageAlpha{frame: descriptor.frameIndex}
	assetID := r.assetServices[handle]
	if assetID == 0 || r.Services == nil {
		return opaque
	}
	info, err := r.Services.Assets.Info(r.ServiceOwner, assetID)
	if err != nil || descriptor.frameIndex >= uint32(len(info.Frames)) {
		return opaque
	}
	surface := info.Frames[descriptor.frameIndex].Surface
	surfaceInfo, err := r.Services.Graphics.Descriptor(r.ServiceOwner, surface)
	if err != nil {
		return opaque
	}
	pixels, err := r.Services.Graphics.RGBA(r.ServiceOwner, surface)
	if err != nil {
		return opaque
	}
	framebuffer, ok := r.Framebuffers[descriptor.framebuffer]
	if !ok {
		return opaque
	}
	return buildWIPIImageAlpha(
		pixels,
		int(surfaceInfo.Width),
		int(surfaceInfo.Height),
		framebuffer.Width,
		framebuffer.Height,
		descriptor.frameIndex,
	)
}

// buildWIPIImageAlpha turns a decoded surface's alpha into a mask over the
// image framebuffer. The framebuffer is the coordinate space the blit reads,
// so the mask is sized to it and every pixel the paint loop could not reach is
// transparent: those bytes were zeroed, and drawing that black over the
// destination would be worse than leaving the destination alone.
func buildWIPIImageAlpha(
	pixels []byte,
	sourceWidth, sourceHeight int,
	maskWidth, maskHeight int,
	frame uint32,
) *wipiImageAlpha {
	alpha := &wipiImageAlpha{frame: frame}
	if maskWidth <= 0 || maskHeight <= 0 ||
		sourceWidth <= 0 || sourceHeight <= 0 ||
		uint64(sourceWidth)*uint64(sourceHeight)*4 > uint64(len(pixels)) {
		return alpha
	}
	width := min(sourceWidth, maskWidth)
	height := min(sourceHeight, maskHeight)
	transparent := false
	for y := 0; y < height && !transparent; y++ {
		for x := 0; x < width; x++ {
			if pixels[(y*sourceWidth+x)*4+3] == 0 {
				transparent = true
				break
			}
		}
	}
	if !transparent && width == maskWidth && height == maskHeight {
		return alpha
	}
	mask := make([]uint64, (maskWidth*maskHeight+63)/64)
	for index := range mask {
		mask[index] = ^uint64(0)
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if pixels[(y*sourceWidth+x)*4+3] == 0 {
				continue
			}
			index := y*maskWidth + x
			mask[index>>6] &^= 1 << (index & 63)
		}
	}
	alpha.mask = mask
	alpha.width = maskWidth
	alpha.height = maskHeight
	return alpha
}

// releaseImageAlpha drops a destroyed image's cached transparency.
func (r *Runtime) releaseImageAlpha(handle uint32) {
	delete(r.imageAlpha, handle)
}
