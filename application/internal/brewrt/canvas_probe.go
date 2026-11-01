package brewrt

// CanvasProbe measures where a title draws its images, so a package with no
// recorded handset can be given the screen it was built for.
//
// Run on the large handset canvas, a title made for that handset puts much of
// its image area - backgrounds, menus, HUDs - right of column 120. A title
// made for the small handset keeps its art within those 120 columns; some
// read the screen height and move a footer or scenery to the bottom, so only
// the width separates the two. Solid fills and text are left out: a
// full-screen clear or one status glyph says nothing about the artwork.
type CanvasProbe struct {
	ImageArea  int64
	ImageRight int64
}

// Right reports the fraction of image area right of the small handset's
// 120 columns.
func (p CanvasProbe) Right() float64 {
	if p.ImageArea == 0 {
		return 0
	}
	return float64(p.ImageRight) / float64(p.ImageArea)
}

// EnableCanvasProbe starts accumulating image placement for CanvasProbeResult.
func (r *Runtime) EnableCanvasProbe() {
	r.canvasProbe = &CanvasProbe{}
}

// CanvasProbeResult returns the accumulated image placement.
func (r *Runtime) CanvasProbeResult() CanvasProbe {
	if r.canvasProbe == nil {
		return CanvasProbe{}
	}
	return *r.canvasProbe
}

func (r *Runtime) probeImageDraw(x, y, width, height int32) {
	if r.canvasProbe == nil || width <= 0 || height <= 0 {
		return
	}
	clamp := func(value, low, high int32) int32 { return max(low, min(value, high)) }
	screenWidth, screenHeight := int32(r.screenWidth), int32(r.screenHeight)
	left, top := clamp(x, 0, screenWidth), clamp(y, 0, screenHeight)
	right, bottom := clamp(x+width, 0, screenWidth), clamp(y+height, 0, screenHeight)
	if right <= left || bottom <= top {
		return
	}
	r.canvasProbe.ImageArea += int64(right-left) * int64(bottom-top)
	if edge := int32(SmallHandsetCanvas.X); right > edge {
		r.canvasProbe.ImageRight += int64(right-max(left, edge)) * int64(bottom-top)
	}
}
