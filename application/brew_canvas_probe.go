package application

import (
	"context"
	"image"
	"sync"
	"time"

	"github.com/mirusu400/aram-core/application/internal/brewrt"
	machinecore "github.com/mirusu400/aram-core/core"
)

// A BREW package does not record the handset it was built for, and KTF shipped
// most titles for both its 120x160-class and its 176x220-class handsets. Titles
// read the screen size once, at start, so the choice has to be made before the
// real run: a throwaway machine plays the opening on the large canvas, with no
// save data attached and nothing exported, and watches where the title draws.
//
// The large canvas is the default. A title made for it but run on the small one
// loses part of every screen, while a small-handset title run on the large one
// keeps all of its screen and only gains a border, so the probe shrinks the
// canvas only on clear evidence that the title keeps within the small
// handset's 120 columns, and the less artwork a title has drawn, the stricter
// that evidence has to be. Across the 350 unpinned packages of the KTF BREW
// dump this misjudged no large-handset title and gave 57 of the 148
// small-handset builds their own canvas.
const (
	brewCanvasProbeFrames   = 600
	brewCanvasProbeTapEvery = 100
	brewCanvasProbeBudget   = 3 * time.Second
	// With this much image area, a small-handset title keeps all but a tenth
	// of it in the first 120 columns, and the screen right of them stays
	// mostly one colour (the varied fraction of any sampled frame).
	brewCanvasProbeRichArea       = 50_000
	brewCanvasProbeRichImageRight = 0.10
	brewCanvasProbeRichFrameRight = 0.15
	// With less, text and painted pixels weigh more: large-handset titles of
	// this kind still put 8% or more of their images right of column 120.
	brewCanvasProbeSomeArea       = 10_000
	brewCanvasProbeSomeImageRight = 0.05
	brewCanvasProbeSomeFrameRight = 0.05
	// A title that paints its own pixels shows something in the corner and
	// almost nothing right of it.
	brewCanvasProbePaintedRight = 0.025
	brewCanvasProbeCornerShown  = 0.02
)

var brewCanvasDecisions sync.Map // package SHA-256 -> image.Point

func brewProbedCanvas(source machinecore.Source, pkg brewrt.Package, digest, fallbackFont string) image.Point {
	if cached, ok := brewCanvasDecisions.Load(digest); ok {
		return cached.(image.Point)
	}
	decision := probeBREWCanvas(source, pkg, fallbackFont)
	brewCanvasDecisions.Store(digest, decision)
	return decision
}

func probeBREWCanvas(source machinecore.Source, pkg brewrt.Package, fallbackFont string) image.Point {
	pkg.NativeCanvas = brewrt.LargeHandsetCanvas
	machine := newBREWMachine(source, pkg)
	machine.fallbackFont = fallbackFont
	defer machine.Close()
	ctx, cancel := context.WithTimeout(context.Background(), brewCanvasProbeBudget)
	defer cancel()
	if err := machine.Start(ctx); err != nil || machine.runtime == nil {
		return brewrt.LargeHandsetCanvas
	}
	machine.runtime.EnableCanvasProbe()
	deadline := time.Now().Add(brewCanvasProbeBudget)
	frameRight, frameCorner := 0.0, 0.0
	for frame := 1; frame <= brewCanvasProbeFrames && time.Now().Before(deadline); frame++ {
		if frame%brewCanvasProbeTapEvery == 0 {
			_ = machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true})
			_ = machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: false, At: time.Millisecond})
		}
		if err := machine.StepFrame(ctx); err != nil {
			break
		}
		if frame%20 == 0 {
			right, corner := variedFraction(machine.Framebuffer())
			frameRight, frameCorner = max(frameRight, right), max(frameCorner, corner)
		}
		// Plenty of artwork already right of the small screen settles it.
		probe := machine.runtime.CanvasProbeResult()
		if frame >= 120 && probe.ImageArea >= 6*brewCanvasProbeRichArea && probe.Right() >= 0.3 {
			return brewrt.LargeHandsetCanvas
		}
	}
	return brewCanvasDecision(machine.runtime.CanvasProbeResult(), frameRight, frameCorner)
}

func brewCanvasDecision(probe brewrt.CanvasProbe, frameRight, frameCorner float64) image.Point {
	var small bool
	switch {
	case probe.ImageArea >= brewCanvasProbeRichArea:
		small = probe.Right() < brewCanvasProbeRichImageRight && frameRight < brewCanvasProbeRichFrameRight
	case probe.ImageArea >= brewCanvasProbeSomeArea:
		small = probe.Right() < brewCanvasProbeSomeImageRight && frameRight < brewCanvasProbeSomeFrameRight
	default:
		small = frameCorner >= brewCanvasProbeCornerShown && frameRight < brewCanvasProbePaintedRight
	}
	if small {
		return brewrt.SmallHandsetCanvas
	}
	return brewrt.LargeHandsetCanvas
}

// variedFraction returns, for the screen right of the small handset's 120
// columns and for its 120x160 corner, the fraction of pixels that differ from
// the area's most common colour. A four-pixel seam between the two is left
// out, so art that ends exactly on the edge does not count against either.
func variedFraction(frame image.Image) (right, corner float64) {
	bounds := frame.Bounds()
	rightCounts, cornerCounts := map[uint32]int{}, map[uint32]int{}
	edge := brewrt.SmallHandsetCanvas
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := frame.At(x, y).RGBA()
			key := (r>>8)<<16 | (g>>8)<<8 | b>>8
			switch {
			case x >= edge.X+4:
				rightCounts[key]++
			case x < edge.X && y < edge.Y:
				cornerCounts[key]++
			}
		}
	}
	return varied(rightCounts), varied(cornerCounts)
}

func varied(counts map[uint32]int) float64 {
	total, dominant := 0, 0
	for _, count := range counts {
		total += count
		dominant = max(dominant, count)
	}
	if total == 0 {
		return 0
	}
	return 1 - float64(dominant)/float64(total)
}
