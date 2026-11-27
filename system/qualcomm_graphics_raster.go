package system

// This file is the fragment half of the Q3Dimension graphics module model: a
// fixed-function OpenGL ES 1.0 triangle rasteriser working on window-space
// vertices. It knows nothing about the command-array encoding.
//
// Arithmetic is float64 with every product passed through an explicit
// conversion. The Go specification forbids fusing a multiply into a following
// add across such a conversion, so a frame is bit-identical on every host
// architecture.

const (
	grpCompareNever = iota
	grpCompareLess
	grpCompareEqual
	grpCompareLessEqual
	grpCompareGreater
	grpCompareNotEqual
	grpCompareGreaterEqual
	grpCompareAlways
)

const (
	grpBlendZero = iota
	grpBlendOne
	grpBlendSrcColor
	grpBlendOneMinusSrcColor
	grpBlendSrcAlpha
	grpBlendOneMinusSrcAlpha
	grpBlendDstAlpha
	grpBlendOneMinusDstAlpha
	grpBlendDstColor
	grpBlendOneMinusDstColor
	grpBlendSrcAlphaSaturate
)

const (
	grpEnvModulate = iota
	grpEnvReplace
	grpEnvDecal
	grpEnvBlend
	grpEnvAdd
)

const (
	grpTexelRGB = iota
	grpTexelRGBA
	grpTexelLuminance
	grpTexelLuminanceAlpha
	grpTexelAlpha
)

const grpTextureUnits = 2

// grpColor is an RGBA colour with components in [0, 1].
type grpColor [4]float64

func grpMul(a, b float64) float64 { return float64(a * b) }

func grpClamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// grpTexture is one decoded mip level 0 image. Texels are stored as RGBA with
// the base format's missing components already expanded the way the GL texture
// environment tables expect.
type grpTexture struct {
	width, height int
	texels        []grpColor
	baseFormat    uint8
	linear        bool
	clampS        bool
	clampT        bool
}

func grpWrap(coordinate, size int, clamp bool) int {
	if clamp {
		if coordinate < 0 {
			return 0
		}
		if coordinate >= size {
			return size - 1
		}
		return coordinate
	}
	coordinate %= size
	if coordinate < 0 {
		coordinate += size
	}
	return coordinate
}

func grpFloor(value float64) int {
	whole := int(value)
	if float64(whole) > value {
		whole--
	}
	return whole
}

func (t *grpTexture) texel(x, y int) grpColor {
	return t.texels[grpWrap(y, t.height, t.clampT)*t.width+grpWrap(x, t.width, t.clampS)]
}

// sample returns the texture colour at normalised coordinates (s, t).
func (t *grpTexture) sample(s, tc float64) grpColor {
	u := grpMul(s, float64(t.width))
	v := grpMul(tc, float64(t.height))
	if !t.linear {
		return t.texel(grpFloor(u), grpFloor(v))
	}
	u -= 0.5
	v -= 0.5
	x0, y0 := grpFloor(u), grpFloor(v)
	fx, fy := u-float64(x0), v-float64(y0)
	c00, c10 := t.texel(x0, y0), t.texel(x0+1, y0)
	c01, c11 := t.texel(x0, y0+1), t.texel(x0+1, y0+1)
	var out grpColor
	for i := range out {
		top := c00[i] + grpMul(c10[i]-c00[i], fx)
		bottom := c01[i] + grpMul(c11[i]-c01[i], fx)
		out[i] = top + grpMul(bottom-top, fy)
	}
	return out
}

// grpVertex is a vertex after the perspective divide and viewport transform.
// invW is 1/w_clip; colour and texture coordinates are the plain attribute
// values and are interpolated perspective-correctly.
type grpVertex struct {
	x, y, z float64
	invW    float64
	color   grpColor
	tex     [grpTextureUnits][2]float64
	fog     float64
}

type grpFragmentState struct {
	scissor                 [4]int // x0, y0, x1, y1 (exclusive), already clipped to the surface
	depthTest               bool
	depthFunc               uint8
	depthWrite              bool
	alphaTest               bool
	alphaFunc               uint8
	alphaRef                float64
	blend                   bool
	blendSrc, blendDst      uint8
	colorMask               [4]bool
	flat                    bool
	fog                     bool
	fogColor                grpColor
	textures                [grpTextureUnits]*grpTexture
	textureEnv              [grpTextureUnits]uint8
	textureEnvColor         [grpTextureUnits]grpColor
	polygonOffsetUnits      float64
	polygonOffsetFactor     float64
	polygonOffsetFillEnable bool
}

// grpSurface is the render target: RGB565 colour and a 16-bit depth buffer,
// both row-major with the origin at the top-left as stored in guest memory.
type grpSurface struct {
	width, height int
	color         []uint16
	depth         []uint16
}

func grpCompare(function uint8, incoming, stored float64) bool {
	switch function {
	case grpCompareNever:
		return false
	case grpCompareLess:
		return incoming < stored
	case grpCompareEqual:
		return incoming == stored
	case grpCompareLessEqual:
		return incoming <= stored
	case grpCompareGreater:
		return incoming > stored
	case grpCompareNotEqual:
		return incoming != stored
	case grpCompareGreaterEqual:
		return incoming >= stored
	default:
		return true
	}
}

func grpUnpack565(pixel uint16) grpColor {
	return grpColor{
		float64(pixel>>11) / 31,
		float64(pixel>>5&0x3f) / 63,
		float64(pixel&0x1f) / 31,
		1,
	}
}

func grpPack565(color grpColor) uint16 {
	r := uint16(grpMul(grpClamp01(color[0]), 31) + 0.5)
	g := uint16(grpMul(grpClamp01(color[1]), 63) + 0.5)
	b := uint16(grpMul(grpClamp01(color[2]), 31) + 0.5)
	return r<<11 | g<<5 | b
}

func grpBlendFactor(factor uint8, source, destination grpColor) grpColor {
	switch factor {
	case grpBlendZero:
		return grpColor{}
	case grpBlendSrcColor:
		return source
	case grpBlendOneMinusSrcColor:
		return grpColor{1 - source[0], 1 - source[1], 1 - source[2], 1 - source[3]}
	case grpBlendSrcAlpha:
		return grpColor{source[3], source[3], source[3], source[3]}
	case grpBlendOneMinusSrcAlpha:
		a := 1 - source[3]
		return grpColor{a, a, a, a}
	case grpBlendDstAlpha:
		return grpColor{destination[3], destination[3], destination[3], destination[3]}
	case grpBlendOneMinusDstAlpha:
		a := 1 - destination[3]
		return grpColor{a, a, a, a}
	case grpBlendDstColor:
		return destination
	case grpBlendOneMinusDstColor:
		return grpColor{1 - destination[0], 1 - destination[1], 1 - destination[2], 1 - destination[3]}
	case grpBlendSrcAlphaSaturate:
		f := source[3]
		if 1-destination[3] < f {
			f = 1 - destination[3]
		}
		return grpColor{f, f, f, 1}
	default:
		return grpColor{1, 1, 1, 1}
	}
}

// grpApplyTextureEnv combines the incoming fragment colour with one texture
// unit per the OpenGL ES 1.0 texture environment tables.
func grpApplyTextureEnv(mode, baseFormat uint8, fragment, texel, constant grpColor) grpColor {
	hasColor := baseFormat != grpTexelAlpha
	hasAlpha := baseFormat != grpTexelRGB && baseFormat != grpTexelLuminance
	out := fragment
	switch mode {
	case grpEnvReplace:
		if hasColor {
			out[0], out[1], out[2] = texel[0], texel[1], texel[2]
		}
		if hasAlpha {
			out[3] = texel[3]
		}
	case grpEnvDecal:
		switch baseFormat {
		case grpTexelRGB:
			out[0], out[1], out[2] = texel[0], texel[1], texel[2]
		case grpTexelRGBA:
			for i := 0; i < 3; i++ {
				out[i] = grpMul(fragment[i], 1-texel[3]) + grpMul(texel[i], texel[3])
			}
		}
	case grpEnvBlend:
		if hasColor {
			for i := 0; i < 3; i++ {
				out[i] = grpMul(fragment[i], 1-texel[i]) + grpMul(constant[i], texel[i])
			}
		}
		if hasAlpha {
			out[3] = grpMul(fragment[3], texel[3])
		}
	case grpEnvAdd:
		if hasColor {
			for i := 0; i < 3; i++ {
				out[i] = grpClamp01(fragment[i] + texel[i])
			}
		}
		if hasAlpha {
			out[3] = grpMul(fragment[3], texel[3])
		}
	default: // modulate
		if hasColor {
			for i := 0; i < 3; i++ {
				out[i] = grpMul(fragment[i], texel[i])
			}
		}
		if hasAlpha {
			out[3] = grpMul(fragment[3], texel[3])
		}
	}
	return out
}

func grpEdge(ax, ay, bx, by, px, py float64) float64 {
	return grpMul(bx-ax, py-ay) - grpMul(by-ay, px-ax)
}

// triangle rasterises one window-space triangle. The caller has already
// culled and clipped it against the near plane; winding is not significant
// here.
func (s *grpSurface) triangle(state *grpFragmentState, v0, v1, v2 *grpVertex) {
	provoking := v2
	area := grpEdge(v0.x, v0.y, v1.x, v1.y, v2.x, v2.y)
	if area == 0 {
		return
	}
	if area < 0 {
		v1, v2 = v2, v1
		area = -area
	}
	minX, maxX := v0.x, v0.x
	minY, maxY := v0.y, v0.y
	for _, v := range []*grpVertex{v1, v2} {
		if v.x < minX {
			minX = v.x
		}
		if v.x > maxX {
			maxX = v.x
		}
		if v.y < minY {
			minY = v.y
		}
		if v.y > maxY {
			maxY = v.y
		}
	}
	x0, x1 := grpFloor(minX), grpFloor(maxX)+1
	y0, y1 := grpFloor(minY), grpFloor(maxY)+1
	if x0 < state.scissor[0] {
		x0 = state.scissor[0]
	}
	if y0 < state.scissor[1] {
		y0 = state.scissor[1]
	}
	if x1 > state.scissor[2] {
		x1 = state.scissor[2]
	}
	if y1 > state.scissor[3] {
		y1 = state.scissor[3]
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}
	// Top-left fill rule: an edge function of exactly zero is inside only on
	// top and left edges, so adjoining triangles never double-draw a pixel.
	inside := func(w, dx, dy float64) bool {
		if w != 0 {
			return w > 0
		}
		return dy < 0 || dy == 0 && dx > 0
	}
	zOffset := 0.0
	if state.polygonOffsetFillEnable {
		zOffset = state.polygonOffsetUnits
	}
	for y := y0; y < y1; y++ {
		py := float64(y) + 0.5
		row := y * s.width
		for x := x0; x < x1; x++ {
			px := float64(x) + 0.5
			w0 := grpEdge(v1.x, v1.y, v2.x, v2.y, px, py)
			w1 := grpEdge(v2.x, v2.y, v0.x, v0.y, px, py)
			w2 := grpEdge(v0.x, v0.y, v1.x, v1.y, px, py)
			if !inside(w0, v2.x-v1.x, v2.y-v1.y) || !inside(w1, v0.x-v2.x, v0.y-v2.y) ||
				!inside(w2, v1.x-v0.x, v1.y-v0.y) {
				continue
			}
			b0, b1, b2 := w0/area, w1/area, w2/area
			z := grpMul(b0, v0.z) + grpMul(b1, v1.z) + grpMul(b2, v2.z) + zOffset
			if z < 0 {
				z = 0
			} else if z > 65535 {
				z = 65535
			}
			index := row + x
			if state.depthTest && !grpCompare(state.depthFunc, float64(uint16(z)), float64(s.depth[index])) {
				continue
			}
			// Perspective-correct weights.
			p0, p1, p2 := grpMul(b0, v0.invW), grpMul(b1, v1.invW), grpMul(b2, v2.invW)
			sum := p0 + p1 + p2
			if sum == 0 {
				continue
			}
			p0, p1, p2 = p0/sum, p1/sum, p2/sum

			var fragment grpColor
			if state.flat {
				fragment = provoking.color
			} else {
				for i := range fragment {
					fragment[i] = grpMul(p0, v0.color[i]) + grpMul(p1, v1.color[i]) + grpMul(p2, v2.color[i])
				}
			}
			for unit := 0; unit < grpTextureUnits; unit++ {
				texture := state.textures[unit]
				if texture == nil {
					continue
				}
				sCoord := grpMul(p0, v0.tex[unit][0]) + grpMul(p1, v1.tex[unit][0]) + grpMul(p2, v2.tex[unit][0])
				tCoord := grpMul(p0, v0.tex[unit][1]) + grpMul(p1, v1.tex[unit][1]) + grpMul(p2, v2.tex[unit][1])
				fragment = grpApplyTextureEnv(
					state.textureEnv[unit], texture.baseFormat,
					fragment, texture.sample(sCoord, tCoord), state.textureEnvColor[unit],
				)
			}
			if state.fog {
				f := grpClamp01(grpMul(p0, v0.fog) + grpMul(p1, v1.fog) + grpMul(p2, v2.fog))
				for i := 0; i < 3; i++ {
					fragment[i] = grpMul(fragment[i], f) + grpMul(state.fogColor[i], 1-f)
				}
			}
			for i := range fragment {
				fragment[i] = grpClamp01(fragment[i])
			}
			if state.alphaTest && !grpCompare(state.alphaFunc, fragment[3], state.alphaRef) {
				continue
			}
			if state.depthTest && state.depthWrite {
				s.depth[index] = uint16(z)
			}
			stored := grpUnpack565(s.color[index])
			if state.blend {
				sf := grpBlendFactor(state.blendSrc, fragment, stored)
				df := grpBlendFactor(state.blendDst, fragment, stored)
				for i := 0; i < 3; i++ {
					fragment[i] = grpClamp01(grpMul(fragment[i], sf[i]) + grpMul(stored[i], df[i]))
				}
			}
			for i := 0; i < 3; i++ {
				if !state.colorMask[i] {
					fragment[i] = stored[i]
				}
			}
			s.color[index] = grpPack565(fragment)
		}
	}
}
