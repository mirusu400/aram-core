package skvmhost

import (
	"context"
	"image"
	"sync"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
)

// An SKT package does not record the handset it was built for, and a title
// whose art sits in opaque resource blobs gives the asset inference nothing to
// measure, so it falls back to the large 240x320 canvas. Many such titles were
// authored for a smaller SKT handset and, on a larger one, centre their own
// fixed-size layout and leave the rest of the display alone: 나이트메이커 frames a
// 176-column picture (`getWidth() > 176` selects its centred branch, #571) and
// 메이플스토리 전사편 keeps to 128 columns. On the large canvas they show as a
// small picture inside wide empty bands.
//
// A throwaway machine therefore plays the opening on the fallback canvas, with
// no save data attached and nothing exported, and samples what it draws. The
// canvas shrinks only when every sampled screen keeps its content inside one
// centred column and at least one screen fills that column exactly - the title
// drawing a picture precisely as wide as a smaller handset. The first content
// outside the column ends the probe and keeps the large canvas, so titles that
// lay out for 240 columns pay only until their first full-width screen. A
// centred dialog of some other width proves nothing either way.
const (
	skvmCanvasProbeFrames      = 600
	skvmCanvasProbeSampleEvery = 10
	skvmCanvasProbeTapEvery    = 100
	// The probe keeps the large canvas when it cannot finish in this time, so
	// a slow host decides conservatively rather than on partial evidence.
	skvmCanvasProbeBudget = 4 * time.Second
)

// skvmCanvasProbeFallback is the only canvas the probe second-guesses: the one
// inferSKVMFramebufferSize returns when no package asset names a handset.
var skvmCanvasProbeFallback = image.Pt(240, 320)

// skvmCentredDesigns are the smaller SKT handset canvases, narrowest first.
var skvmCentredDesigns = [...]image.Point{
	image.Pt(120, 160),
	image.Pt(128, 160),
	image.Pt(176, 220),
}

var skvmCanvasDecisions sync.Map // package SHA-256 -> image.Point

// skvmCanvasNeedsProbe reports whether the canvas is still the fallback guess:
// no package asset named a handset and no exact-title entry applies. The
// decision is cached by package digest, so a source without one is not
// probed.
func skvmCanvasNeedsProbe(
	source machinecore.Source,
	pkg skloader.Package,
	inferred, canvas image.Point,
) bool {
	if source.SHA256 == "" || inferred != skvmCanvasProbeFallback || canvas != inferred {
		return false
	}
	_, pinned := lookupSKVMTitleCanvas(source, pkg, inferred)
	return !pinned
}

// skvmProbedCanvas answers with the handset canvas a title keeps its layout
// to, or canvas itself when there is no clear evidence of a smaller one.
func skvmProbedCanvas(
	source machinecore.Source,
	app Application,
	pkg skloader.Package,
	canvas image.Point,
) image.Point {
	if cached, ok := skvmCanvasDecisions.Load(source.SHA256); ok {
		return cached.(image.Point)
	}
	decision := probeSKVMCentredCanvas(source, app, pkg, canvas)
	skvmCanvasDecisions.Store(source.SHA256, decision)
	return decision
}

func probeSKVMCentredCanvas(
	source machinecore.Source,
	app Application,
	pkg skloader.Package,
	canvas image.Point,
) image.Point {
	ctx, cancel := context.WithTimeout(context.Background(), skvmCanvasProbeBudget)
	defer cancel()
	machine, err := newJavaMachine(ctx, source, app, &pkg, canvas, 0, 0, 0, false)
	if err != nil {
		return canvas
	}
	defer machine.Close()
	if err := machine.Start(ctx); err != nil {
		return canvas
	}
	var evidence skvmColumnEvidence
	for frame := 1; frame <= skvmCanvasProbeFrames; frame++ {
		if ctx.Err() != nil {
			return canvas
		}
		if frame%skvmCanvasProbeTapEvery == 0 {
			_ = machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true})
			_ = machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: false, At: time.Millisecond})
		}
		if err := machine.StepFrame(ctx); err != nil {
			// A title that faults or exits in its opening has not shown
			// enough of itself to be judged.
			return canvas
		}
		if frame%skvmCanvasProbeSampleEvery != 0 {
			continue
		}
		screen, ok := machine.Framebuffer().(*image.NRGBA)
		if !ok || !evidence.observe(screen) {
			return canvas
		}
	}
	if ctx.Err() != nil {
		return canvas
	}
	return evidence.design(canvas)
}

// skvmColumnEvidence accumulates, over sampled frames, how far the title's
// content stays from the left and right edges of the display.
type skvmColumnEvidence struct {
	samples     int
	left, right int
	// spans records the content widths of screens whose content is centred
	// and bounded by solid bands on both sides.
	spans map[int]bool
}

// observe folds one frame into the evidence. It reports false once the frame
// shows content too close to an edge for any smaller handset's column.
func (e *skvmColumnEvidence) observe(frame *image.NRGBA) bool {
	bounds := frame.Bounds()
	width := bounds.Dx()
	left := solidColumns(frame, bounds.Min.X, 1)
	if left >= width {
		return true // a blank screen proves nothing
	}
	right := solidColumns(frame, bounds.Max.X-1, -1)
	widest := skvmCentredDesigns[len(skvmCentredDesigns)-1].X
	margin := (width - widest) / 2
	if left < margin || right < margin {
		return false
	}
	if e.samples == 0 {
		e.left, e.right = left, right
		e.spans = make(map[int]bool)
	}
	e.samples++
	e.left = min(e.left, left)
	e.right = min(e.right, right)
	if left == right {
		e.spans[width-left-right] = true
	}
	return true
}

// design returns the narrowest smaller handset whose centred column holds
// every sampled screen and is filled exactly by at least one of them.
func (e *skvmColumnEvidence) design(canvas image.Point) image.Point {
	if e.samples == 0 {
		return canvas
	}
	for _, design := range skvmCentredDesigns {
		margin := (canvas.X - design.X) / 2
		if design.X >= canvas.X || design.Y >= canvas.Y ||
			e.left < margin || e.right < margin || !e.spans[design.X] {
			continue
		}
		return design
	}
	return canvas
}

// solidColumns counts the columns, starting at x and stepping by step, that
// are each a single colour from top to bottom.
func solidColumns(frame *image.NRGBA, x, step int) int {
	bounds := frame.Bounds()
	count := 0
	for ; x >= bounds.Min.X && x < bounds.Max.X; x += step {
		offset := frame.PixOffset(x, bounds.Min.Y)
		first := frame.Pix[offset : offset+4 : offset+4]
		for y := bounds.Min.Y + 1; y < bounds.Max.Y; y++ {
			offset += frame.Stride
			pixel := frame.Pix[offset : offset+4 : offset+4]
			if pixel[0] != first[0] || pixel[1] != first[1] ||
				pixel[2] != first[2] || pixel[3] != first[3] {
				return count
			}
		}
		count++
	}
	return count
}
