package system

import "testing"

func newGRPTestSurface(width, height int) *grpSurface {
	surface := &grpSurface{
		width: width, height: height,
		color: make([]uint16, width*height),
		depth: make([]uint16, width*height),
	}
	for index := range surface.depth {
		surface.depth[index] = 0xffff
	}
	return surface
}

func grpTestFragmentState(surface *grpSurface) *grpFragmentState {
	return &grpFragmentState{
		scissor:   [4]int{0, 0, surface.width, surface.height},
		colorMask: [4]bool{true, true, true, true},
	}
}

func grpTestVertex(x, y, z float64, color grpColor) *grpVertex {
	return &grpVertex{x: x, y: y, z: z, invW: 1, color: color}
}

func TestGRPTriangleFillRuleCoversSharedEdgeOnce(t *testing.T) {
	surface := newGRPTestSurface(8, 8)
	state := grpTestFragmentState(surface)
	state.blend, state.blendSrc, state.blendDst = true, grpBlendOne, grpBlendOne
	gray := grpColor{8.0 / 31, 16.0 / 63, 8.0 / 31, 1}
	// Two triangles forming the square [1,7)x[1,7); additive blending would
	// expose any pixel drawn twice along the diagonal.
	a := grpTestVertex(1, 1, 0, gray)
	b := grpTestVertex(7, 1, 0, gray)
	c := grpTestVertex(7, 7, 0, gray)
	d := grpTestVertex(1, 7, 0, gray)
	surface.triangle(state, a, b, c)
	surface.triangle(state, a, c, d)
	want := grpPack565(gray)
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			pixel := surface.color[y*8+x]
			inside := x >= 1 && x < 7 && y >= 1 && y < 7
			if inside && pixel != want || !inside && pixel != 0 {
				t.Fatalf("pixel (%d,%d) = %#04x, inside=%t want %#04x", x, y, pixel, inside, want)
			}
		}
	}
}

func TestGRPTriangleDepthAlphaScissorAndMask(t *testing.T) {
	surface := newGRPTestSurface(8, 8)
	state := grpTestFragmentState(surface)
	state.depthTest, state.depthFunc, state.depthWrite = true, grpCompareLess, true
	red := grpColor{1, 0, 0, 1}
	blue := grpColor{0, 0, 1, 0.25}
	full := func(z float64, color grpColor) {
		surface.triangle(state, grpTestVertex(0, 0, z, color), grpTestVertex(16, 0, z, color), grpTestVertex(0, 16, z, color))
	}
	full(1000, red)
	if surface.color[0] != 0xf800 || surface.depth[0] != 1000 {
		t.Fatalf("near triangle pixel %#04x depth %d", surface.color[0], surface.depth[0])
	}
	full(2000, blue)
	if surface.color[0] != 0xf800 {
		t.Fatalf("farther triangle passed GL_LESS: %#04x", surface.color[0])
	}
	state.depthWrite = false
	state.alphaTest, state.alphaFunc, state.alphaRef = true, grpCompareGreater, 0.5
	full(500, blue)
	if surface.color[0] != 0xf800 {
		t.Fatalf("fragment failing the alpha test was written: %#04x", surface.color[0])
	}
	state.alphaTest = false
	state.scissor = [4]int{2, 2, 4, 4}
	state.colorMask = [4]bool{false, true, true, true}
	full(500, blue)
	if surface.color[0] != 0xf800 || surface.color[2*8+2] != 0xf81f || surface.color[4*8+4] != 0xf800 {
		t.Fatalf("scissor/mask result corner %#04x inside %#04x outside %#04x",
			surface.color[0], surface.color[2*8+2], surface.color[4*8+4])
	}
	if surface.depth[2*8+2] != 1000 {
		t.Fatalf("depth written with the depth mask clear: %d", surface.depth[2*8+2])
	}
}

func TestGRPTriangleTextureEnvironmentAndBlend(t *testing.T) {
	surface := newGRPTestSurface(4, 4)
	state := grpTestFragmentState(surface)
	texture := &grpTexture{
		width: 2, height: 2, baseFormat: grpTexelRGBA,
		texels: []grpColor{{1, 0, 0, 1}, {0, 1, 0, 1}, {0, 0, 1, 1}, {1, 1, 1, 0.5}},
	}
	state.textures[0] = texture
	state.textureEnv[0] = grpEnvModulate
	white := grpColor{1, 1, 1, 1}
	corner := func(x, y, s, tc float64) *grpVertex {
		v := grpTestVertex(x, y, 0, white)
		v.tex[0] = [2]float64{s, tc}
		return v
	}
	a, b := corner(0, 0, 0, 0), corner(4, 0, 1, 0)
	c, d := corner(4, 4, 1, 1), corner(0, 4, 0, 1)
	surface.triangle(state, a, b, c)
	surface.triangle(state, a, c, d)
	if got := [4]uint16{surface.color[0], surface.color[3], surface.color[12], surface.color[15]}; got !=
		[4]uint16{0xf800, 0x07e0, 0x001f, 0xffff} {
		t.Fatalf("nearest texel quadrants = %#04x", got)
	}

	// Source-alpha blending of the half-transparent white texel over black.
	for index := range surface.color {
		surface.color[index] = 0
	}
	state.blend, state.blendSrc, state.blendDst = true, grpBlendSrcAlpha, grpBlendOneMinusSrcAlpha
	surface.triangle(state, a, b, c)
	surface.triangle(state, a, c, d)
	if pixel := surface.color[15]; pixel != grpPack565(grpColor{0.5, 0.5, 0.5, 1}) {
		t.Fatalf("blended texel = %#04x", pixel)
	}

	// GL_REPLACE with an alpha-only texture keeps the fragment colour.
	alpha := grpApplyTextureEnv(grpEnvReplace, grpTexelAlpha, grpColor{0.2, 0.4, 0.6, 1}, grpColor{0, 0, 0, 0.5}, grpColor{})
	if alpha != (grpColor{0.2, 0.4, 0.6, 0.5}) {
		t.Fatalf("alpha texture replace = %v", alpha)
	}
}

func TestGRPGeometryTransformsLightsClipsAndCulls(t *testing.T) {
	surface := newGRPTestSurface(8, 8)
	fragment := grpTestFragmentState(surface)
	geometry := &grpGeometryState{
		modelView: grpIdentity(), mvp: grpIdentity(),
		normalMatrix: [9]float64{1, 0, 0, 0, 1, 0, 0, 0, 1},
		// An 8x8 viewport at the surface origin, rows down, default depth range.
		windowScale:  [3]float64{4, -4, -32767},
		windowOffset: [3]float64{4, 4, 32767},
	}
	for unit := range geometry.textureMatrix {
		geometry.textureMatrix[unit] = grpIdentity()
	}
	geometry.lighting = true
	geometry.material = grpMaterial{diffuse: grpColor{1, 1, 1, 1}}
	geometry.lights[0] = grpLight{
		enabled: true, diffuse: grpColor{1, 0.5, 0, 1},
		position: grpVec4{0, 0, 1, 0}, spotCosCutoff: -1, constantAttenuation: 1,
	}
	vertex := func(x, y float64) grpClipVertex {
		return geometry.shade(&grpInputVertex{
			position: grpVec4{x, y, 0, 1}, normal: grpVec3{0, 0, 1}, color: grpColor{0, 0, 1, 1},
		})
	}
	// A counter-clockwise triangle far larger than the viewport: clipping
	// must reduce it to the full window.
	big := []grpClipVertex{vertex(-4, -4), vertex(4, -4), vertex(0, 8)}
	geometry.cullFace, geometry.cullBack = true, true
	geometry.drawPrimitives(surface, fragment, grpPrimitiveTriangles, big)
	lit := grpPack565(grpColor{1, 0.5, 0, 1})
	if surface.color[7*8+0] != lit || surface.color[7*8+7] != lit {
		t.Fatalf("lit, clipped triangle bottom row = %#04x %#04x, want %#04x",
			surface.color[7*8+0], surface.color[7*8+7], lit)
	}
	// The same triangle wound clockwise is a back face.
	for index := range surface.color {
		surface.color[index] = 0
	}
	geometry.drawPrimitives(surface, fragment, grpPrimitiveTriangles, []grpClipVertex{big[1], big[0], big[2]})
	for index, pixel := range surface.color {
		if pixel != 0 {
			t.Fatalf("culled back face drew pixel %d = %#04x", index, pixel)
		}
	}
	// A triangle wholly behind the near plane disappears.
	geometry.cullFace = false
	behind := func(x, y float64) grpClipVertex {
		return grpClipVertex{clip: grpVec4{x, y, -2, 1}, color: grpColor{1, 1, 1, 1}}
	}
	geometry.drawPrimitives(surface, fragment, grpPrimitiveTriangles,
		[]grpClipVertex{behind(-1, -1), behind(1, -1), behind(0, 1)})
	for index, pixel := range surface.color {
		if pixel != 0 {
			t.Fatalf("triangle behind the near plane drew pixel %d = %#04x", index, pixel)
		}
	}
	if fog := (&grpGeometryState{fogMode: grpFogLinear, fogStart: 1, fogEnd: 3}).fogFactor(-2); fog != 0.5 {
		t.Fatalf("linear fog factor at eye z -2 = %v", fog)
	}
	if value := grpExpNegative(1); value < 0.36787 || value > 0.36789 {
		t.Fatalf("exp(-1) = %v", value)
	}
	if value := grpPow(0.5, 2.5); value < 0.1767 || value > 0.1768 {
		t.Fatalf("0.5^2.5 = %v", value)
	}
}

func TestGRPGeometryBoundsTheRowsABatchCanCover(t *testing.T) {
	geometry := &grpGeometryState{
		windowScale:  [3]float64{120, -55, -32767},
		windowOffset: [3]float64{121, 119, 32767},
	}
	vertex := func(y, z, w float64) grpClipVertex { return grpClipVertex{clip: grpVec4{0, y, z, w}} }
	low, high, bounded := geometry.windowRows([]grpClipVertex{vertex(1, 0, 2), vertex(-2, 0, 2), vertex(0, 0, 1)})
	// ndc y in [-1, 0.5] maps to rows 91.5 .. 174.
	if !bounded || low != 91 || high != 175 {
		t.Fatalf("row bound = [%d, %d) bounded %t", low, high, bounded)
	}
	for name, vertices := range map[string][]grpClipVertex{
		"behind the eye":        {vertex(0, 0, 1), vertex(0, 0, -1)},
		"on the eye plane":      {vertex(0, 0, 0)},
		"before the near plane": {vertex(0, -2, 1)},
		"absurdly far away":     {vertex(1e9, 0, 1)},
	} {
		if _, _, bounded := geometry.windowRows(vertices); bounded {
			t.Errorf("%s: batch was given a row bound", name)
		}
	}
	if low, high, bounded := geometry.windowRows(nil); !bounded || low != high {
		t.Fatalf("empty batch bound = [%d, %d) %t", low, high, bounded)
	}
}
