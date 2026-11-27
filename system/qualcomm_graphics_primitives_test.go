package system

import "testing"

func grpTestGeometry() *grpGeometryState {
	geometry := &grpGeometryState{
		modelView: grpIdentity(), mvp: grpIdentity(),
		normalMatrix: [9]float64{1, 0, 0, 0, 1, 0, 0, 0, 1},
		windowScale:  [3]float64{4, -4, -32767},
		windowOffset: [3]float64{4, 4, 32767},
	}
	for unit := range geometry.textureMatrix {
		geometry.textureMatrix[unit] = grpIdentity()
	}
	return geometry
}

func TestGRPGeometryDrawsPointsAndLines(t *testing.T) {
	surface := newGRPTestSurface(8, 8)
	fragment := grpTestFragmentState(surface)
	geometry := grpTestGeometry()
	white := grpColor{1, 1, 1, 1}
	at := func(x, y float64) grpClipVertex { return grpClipVertex{clip: grpVec4{x, y, 0, 1}, color: white} }
	count := func() (int, map[int]bool) {
		pixels := map[int]bool{}
		for index, pixel := range surface.color {
			if pixel != 0 {
				pixels[index] = true
			}
		}
		return len(pixels), pixels
	}

	// ndc (-0.875, 0.875) is window (0.5, 0.5): exactly pixel (0, 0). A point
	// outside the view volume draws nothing.
	geometry.drawPrimitives(surface, fragment, grpPrimitivePoints, []grpClipVertex{at(-0.875, 0.875), at(2, 0)})
	if n, pixels := count(); n != 1 || !pixels[0] {
		t.Fatalf("point pixels = %v", pixels)
	}

	// A horizontal line along row 3 from column 1 to column 6 covers exactly
	// those six pixels.
	clear(surface.color)
	geometry.drawPrimitives(surface, fragment, grpPrimitiveLines,
		[]grpClipVertex{at(-0.625, 0.125), at(0.625, 0.125)})
	n, pixels := count()
	for x := 1; x <= 6; x++ {
		if !pixels[3*8+x] {
			t.Fatalf("line misses pixel (%d,3): %v", x, pixels)
		}
	}
	if n != 6 {
		t.Fatalf("line covered %d pixels, want 6: %v", n, pixels)
	}

	// A line loop closes itself; a strip does not. A segment that leaves the
	// view volume is clipped to it, not dropped.
	loop := []grpClipVertex{at(-0.625, 0.625), at(0.625, 0.625), at(0.625, -0.625)}
	clear(surface.color)
	geometry.drawPrimitives(surface, fragment, grpPrimitiveLineStrip, loop)
	strip, _ := count()
	clear(surface.color)
	geometry.drawPrimitives(surface, fragment, grpPrimitiveLineLoop, loop)
	closed, pixels := count()
	if closed <= strip || !pixels[4*8+4] {
		t.Fatalf("loop %d pixels vs strip %d; diagonal pixel present %t", closed, strip, pixels[4*8+4])
	}
	clear(surface.color)
	geometry.drawPrimitives(surface, fragment, grpPrimitiveLines, []grpClipVertex{at(-3, 0.125), at(3, 0.125)})
	if n, _ := count(); n != 8 {
		t.Fatalf("clipped line covered %d pixels, want the full row", n)
	}
}

func TestGRPTriangleLogicOpAndStencil(t *testing.T) {
	surface := newGRPTestSurface(4, 4)
	surface.stencil = make([]uint8, 16)
	state := grpTestFragmentState(surface)
	full := func(color grpColor) {
		surface.triangle(state, grpTestVertex(0, 0, 0, color), grpTestVertex(8, 0, 0, color), grpTestVertex(0, 8, 0, color))
	}
	for index := range surface.color {
		surface.color[index] = 0x0ff0
	}
	// XOR with white inverts every bit; COPY then stores the source, and the
	// colour mask still protects the red field.
	state.logicOp, state.logicCode = true, 6
	full(grpColor{1, 1, 1, 1})
	if surface.color[0] != 0xf00f {
		t.Fatalf("XOR result = %#04x", surface.color[0])
	}
	state.logicCode = 12
	state.colorMask[0] = false
	full(grpColor{0, 1, 0, 1})
	if surface.color[0] != 0xf7e0 {
		t.Fatalf("masked COPY result = %#04x", surface.color[0])
	}
	if grpLogic(0, 0xffff, 0xffff) != 0 || grpLogic(15, 0, 0) != 0xffff || grpLogic(5, 0x1234, 0x00ff) != 0xff00 {
		t.Fatal("CLEAR / SET / INVERT truth tables are wrong")
	}
	state.logicOp, state.colorMask[0] = false, true

	// Pass 1 marks the left half in the stencil without drawing colour.
	state.stencil = true
	state.stencilFunc, state.stencilRef = grpCompareAlways, 1
	state.stencilMask, state.stencilWriteMask = 1, 1
	state.stencilFail, state.stencilDepthFail, state.stencilPass = grpStencilKeep, grpStencilKeep, grpStencilReplace
	state.colorMask = [4]bool{}
	state.scissor = [4]int{0, 0, 2, 4}
	full(grpColor{1, 1, 1, 1})
	// Pass 2 draws only where the stencil equals 1, and inverts it there.
	state.colorMask = [4]bool{true, true, true, true}
	state.scissor = [4]int{0, 0, 4, 4}
	state.stencilFunc = grpCompareEqual
	state.stencilPass = grpStencilInvert
	for index := range surface.color {
		surface.color[index] = 0
	}
	full(grpColor{0, 0, 1, 1})
	if surface.color[1] != 0x001f || surface.color[2] != 0 {
		t.Fatalf("stencilled pixels = %#04x inside, %#04x outside", surface.color[1], surface.color[2])
	}
	if surface.stencil[1] != 0 || surface.stencil[2] != 0 {
		t.Fatalf("stencil after invert = %d inside, %d outside", surface.stencil[1], surface.stencil[2])
	}
	// A depth failure takes the zfail operation and writes no colour.
	state.depthTest, state.depthFunc = true, grpCompareNever
	state.stencilFunc, state.stencilDepthFail = grpCompareAlways, grpStencilIncrement
	full(grpColor{1, 0, 0, 1})
	if surface.stencil[2] != 1 || surface.color[2] != 0 {
		t.Fatalf("depth-fail stencil = %d colour %#04x", surface.stencil[2], surface.color[2])
	}
}
