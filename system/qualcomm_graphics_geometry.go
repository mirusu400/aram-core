package system

import "math"

// This file is the geometry half of the graphics module model: OpenGL ES 1.0
// transformation, lighting, fog, clipping and primitive assembly. It consumes
// object-space vertices and feeds window-space triangles to the rasteriser.

type grpVec3 [3]float64
type grpVec4 [4]float64

// grpMat4 is a column-major 4x4 matrix, as OpenGL stores it.
type grpMat4 [16]float64

func grpIdentity() grpMat4 {
	return grpMat4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
}

func grpDot3(a, b grpVec3) float64 {
	return grpMul(a[0], b[0]) + grpMul(a[1], b[1]) + grpMul(a[2], b[2])
}

func grpNormalize(v grpVec3) grpVec3 {
	length := math.Sqrt(grpDot3(v, v))
	if length == 0 {
		return v
	}
	return grpVec3{v[0] / length, v[1] / length, v[2] / length}
}

func (m *grpMat4) transform(v grpVec4) grpVec4 {
	var out grpVec4
	for row := 0; row < 4; row++ {
		out[row] = grpMul(m[row], v[0]) + grpMul(m[4+row], v[1]) +
			grpMul(m[8+row], v[2]) + grpMul(m[12+row], v[3])
	}
	return out
}

// grpPow raises base in [0, 1] to a non-negative exponent using only
// multiplications and one square-root chain, so the result does not depend on
// the host C library.
func grpPow(base, exponent float64) float64 {
	if base <= 0 {
		return 0
	}
	if exponent <= 0 {
		return 1
	}
	whole := int(exponent)
	fraction := exponent - float64(whole)
	result := 1.0
	power := base
	for whole > 0 {
		if whole&1 != 0 {
			result = grpMul(result, power)
		}
		power = grpMul(power, power)
		whole >>= 1
	}
	// Eight binary digits of the fractional exponent.
	root := base
	for bit := 0; bit < 8 && fraction > 0; bit++ {
		root = math.Sqrt(root)
		fraction = grpMul(fraction, 2)
		if fraction >= 1 {
			result = grpMul(result, root)
			fraction -= 1
		}
	}
	return result
}

type grpLight struct {
	enabled              bool
	ambient              grpColor
	diffuse              grpColor
	specular             grpColor
	position             grpVec4 // eye space; w == 0 is directional
	spotDirection        grpVec3 // eye space, normalised
	spotExponent         float64
	spotCosCutoff        float64 // -1 disables the spot cone
	constantAttenuation  float64
	linearAttenuation    float64
	quadraticAttenuation float64
}

type grpMaterial struct {
	ambient   grpColor
	diffuse   grpColor
	specular  grpColor
	emission  grpColor
	shininess float64
}

const grpMaximumLights = 8

type grpGeometryState struct {
	// modelView takes object space to eye space (lighting and fog); mvp takes
	// it straight to clip space. The ARM sends both.
	modelView grpMat4
	mvp       grpMat4
	// normalMatrix is the upper 3x3 of the inverse-transpose model-view
	// matrix, row-major.
	normalMatrix  [9]float64
	textureMatrix [grpTextureUnits]grpMat4
	lighting      bool
	normalize     bool
	colorMaterial bool
	twoSide       bool
	sceneAmbient  grpColor
	material      grpMaterial
	lights        [grpMaximumLights]grpLight
	fog           bool
	fogMode       uint8 // 0 linear, 1 exp, 2 exp2
	fogDensity    float64
	fogStart      float64
	fogEnd        float64
	cullFace      bool
	cullFront     bool
	cullBack      bool
	frontFaceCW   bool
	// Window transform: window = offset + scale * ndc, with rows growing
	// downward and depth already in the module's stored (inverted) form.
	windowScale  [3]float64
	windowOffset [3]float64
}

const (
	grpFogLinear = iota
	grpFogExp
	grpFogExp2
)

// grpInputVertex is one vertex as fetched from the client arrays.
type grpInputVertex struct {
	position grpVec4
	normal   grpVec3
	color    grpColor
	tex      [grpTextureUnits]grpVec4
}

// grpClipVertex is a vertex in clip space with its shaded attributes.
type grpClipVertex struct {
	clip  grpVec4
	color grpColor
	tex   [grpTextureUnits][2]float64
	fog   float64
}

func (g *grpGeometryState) light(eye grpVec4, normal grpVec3, vertexColor grpColor) grpColor {
	material := g.material
	if g.colorMaterial {
		material.ambient, material.diffuse = vertexColor, vertexColor
	}
	var out grpColor
	for i := 0; i < 3; i++ {
		out[i] = material.emission[i] + grpMul(material.ambient[i], g.sceneAmbient[i])
	}
	out[3] = material.diffuse[3]
	position := grpVec3{eye[0], eye[1], eye[2]}
	for index := range g.lights {
		light := &g.lights[index]
		if !light.enabled {
			continue
		}
		var direction grpVec3
		attenuation := 1.0
		if light.position[3] == 0 {
			direction = grpNormalize(grpVec3{light.position[0], light.position[1], light.position[2]})
		} else {
			direction = grpVec3{
				light.position[0] - position[0], light.position[1] - position[1], light.position[2] - position[2],
			}
			distance := math.Sqrt(grpDot3(direction, direction))
			direction = grpNormalize(direction)
			denominator := light.constantAttenuation + grpMul(light.linearAttenuation, distance) +
				grpMul(light.quadraticAttenuation, grpMul(distance, distance))
			if denominator > 0 {
				attenuation = 1 / denominator
			}
			if light.spotCosCutoff > -1 {
				along := -grpDot3(direction, light.spotDirection)
				if along < light.spotCosCutoff {
					attenuation = 0
				} else {
					attenuation = grpMul(attenuation, grpPow(along, light.spotExponent))
				}
			}
		}
		if attenuation == 0 {
			continue
		}
		diffuse := grpDot3(normal, direction)
		if diffuse < 0 {
			diffuse = 0
		}
		specular := 0.0
		if diffuse > 0 && material.shininess >= 0 {
			// OpenGL ES 1.0 uses an infinite viewer: the half vector is the
			// light direction plus +z.
			half := grpNormalize(grpVec3{direction[0], direction[1], direction[2] + 1})
			specular = grpPow(grpDot3(normal, half), material.shininess)
			if grpDot3(normal, half) <= 0 {
				specular = 0
			}
		}
		for i := 0; i < 3; i++ {
			term := grpMul(material.ambient[i], light.ambient[i]) +
				grpMul(grpMul(material.diffuse[i], light.diffuse[i]), diffuse) +
				grpMul(grpMul(material.specular[i], light.specular[i]), specular)
			out[i] += grpMul(attenuation, term)
		}
	}
	for i := range out {
		out[i] = grpClamp01(out[i])
	}
	return out
}

func (g *grpGeometryState) fogFactor(eyeZ float64) float64 {
	distance := math.Abs(eyeZ)
	switch g.fogMode {
	case grpFogExp:
		return grpClamp01(grpExpNegative(grpMul(g.fogDensity, distance)))
	case grpFogExp2:
		scaled := grpMul(g.fogDensity, distance)
		return grpClamp01(grpExpNegative(grpMul(scaled, scaled)))
	default:
		span := g.fogEnd - g.fogStart
		if span == 0 {
			return 1
		}
		return grpClamp01((g.fogEnd - distance) / span)
	}
}

// grpExpNegative computes e^-x for x >= 0 by argument halving and a short
// Taylor series, again to stay independent of the host C library.
func grpExpNegative(x float64) float64 {
	if x <= 0 {
		return 1
	}
	if x > 40 {
		return 0
	}
	halvings := 0
	for x > 0.5 {
		x = grpMul(x, 0.5)
		halvings++
	}
	term, sum := 1.0, 1.0
	for n := 1; n <= 10; n++ {
		term = grpMul(term, -x) / float64(n)
		sum += term
	}
	for ; halvings > 0; halvings-- {
		sum = grpMul(sum, sum)
	}
	return sum
}

// shade runs one vertex through the model-view transform, lighting, fog,
// texture matrices and projection.
func (g *grpGeometryState) shade(in *grpInputVertex) grpClipVertex {
	eye := g.modelView.transform(in.position)
	var out grpClipVertex
	out.clip = g.mvp.transform(in.position)
	out.color = in.color
	if g.lighting {
		normal := grpVec3{
			grpMul(g.normalMatrix[0], in.normal[0]) + grpMul(g.normalMatrix[1], in.normal[1]) + grpMul(g.normalMatrix[2], in.normal[2]),
			grpMul(g.normalMatrix[3], in.normal[0]) + grpMul(g.normalMatrix[4], in.normal[1]) + grpMul(g.normalMatrix[5], in.normal[2]),
			grpMul(g.normalMatrix[6], in.normal[0]) + grpMul(g.normalMatrix[7], in.normal[1]) + grpMul(g.normalMatrix[8], in.normal[2]),
		}
		if g.normalize {
			normal = grpNormalize(normal)
		}
		out.color = g.light(eye, normal, in.color)
	}
	for unit := 0; unit < grpTextureUnits; unit++ {
		coordinate := g.textureMatrix[unit].transform(in.tex[unit])
		if coordinate[3] != 0 && coordinate[3] != 1 {
			coordinate[0] /= coordinate[3]
			coordinate[1] /= coordinate[3]
		}
		out.tex[unit] = [2]float64{coordinate[0], coordinate[1]}
	}
	out.fog = 1
	if g.fog {
		out.fog = g.fogFactor(eye[2])
	}
	return out
}

func grpLerpClip(a, b *grpClipVertex, t float64) grpClipVertex {
	var out grpClipVertex
	for i := range out.clip {
		out.clip[i] = a.clip[i] + grpMul(b.clip[i]-a.clip[i], t)
	}
	for i := range out.color {
		out.color[i] = a.color[i] + grpMul(b.color[i]-a.color[i], t)
	}
	for unit := range out.tex {
		for i := range out.tex[unit] {
			out.tex[unit][i] = a.tex[unit][i] + grpMul(b.tex[unit][i]-a.tex[unit][i], t)
		}
	}
	out.fog = a.fog + grpMul(b.fog-a.fog, t)
	return out
}

// grpClipPolygon clips a convex polygon against the six clip-space planes
// (Sutherland-Hodgman).
func grpClipPolygon(polygon []grpClipVertex) []grpClipVertex {
	for plane := 0; plane < 6 && len(polygon) >= 3; plane++ {
		axis, sign := plane/2, 1.0
		if plane&1 != 0 {
			sign = -1
		}
		distance := func(v *grpClipVertex) float64 { return v.clip[3] + grpMul(sign, v.clip[axis]) }
		clipped := make([]grpClipVertex, 0, len(polygon)+1)
		for index := range polygon {
			current := &polygon[index]
			next := &polygon[(index+1)%len(polygon)]
			dc, dn := distance(current), distance(next)
			if dc >= 0 {
				clipped = append(clipped, *current)
			}
			if dc >= 0 != (dn >= 0) {
				clipped = append(clipped, grpLerpClip(current, next, dc/(dc-dn)))
			}
		}
		polygon = clipped
	}
	if len(polygon) < 3 {
		return nil
	}
	return polygon
}

func (g *grpGeometryState) toWindow(v *grpClipVertex) grpVertex {
	inverse := 1 / v.clip[3]
	return grpVertex{
		x:     g.windowOffset[0] + grpMul(grpMul(v.clip[0], inverse), g.windowScale[0]),
		y:     g.windowOffset[1] + grpMul(grpMul(v.clip[1], inverse), g.windowScale[1]),
		z:     g.windowOffset[2] + grpMul(grpMul(v.clip[2], inverse), g.windowScale[2]),
		invW:  inverse,
		color: v.color,
		tex:   v.tex,
		fog:   v.fog,
	}
}

// windowRows bounds the surface rows a batch of shaded vertices can cover as
// [low, high). It declines when a vertex is on or behind the eye plane, where
// the projection gives no bound.
func (g *grpGeometryState) windowRows(vertices []grpClipVertex) (int, int, bool) {
	if len(vertices) == 0 {
		return 0, 0, true
	}
	minimum, maximum := 0.0, 0.0
	for index := range vertices {
		clip := &vertices[index].clip
		if clip[3] <= 0 || clip[2] < -clip[3] {
			return 0, 0, false
		}
		y := g.windowOffset[1] + grpMul(clip[1]/clip[3], g.windowScale[1])
		if index == 0 || y < minimum {
			minimum = y
		}
		if index == 0 || y > maximum {
			maximum = y
		}
	}
	const limit = 1 << 20
	if minimum < -limit || maximum > limit {
		return 0, 0, false
	}
	return grpFloor(minimum), grpFloor(maximum) + 1, true
}

// drawTriangle clips, culls and rasterises one triangle of shaded vertices.
func (g *grpGeometryState) drawTriangle(
	surface *grpSurface,
	fragment *grpFragmentState,
	a, b, c *grpClipVertex,
) {
	polygon := grpClipPolygon([]grpClipVertex{*a, *b, *c})
	if polygon == nil {
		return
	}
	window := make([]grpVertex, len(polygon))
	for index := range polygon {
		window[index] = g.toWindow(&polygon[index])
	}
	// With surface rows growing downward a counter-clockwise GL triangle has
	// a negative edge function here.
	area := grpEdge(window[0].x, window[0].y, window[1].x, window[1].y, window[2].x, window[2].y)
	if area == 0 {
		return
	}
	front := area < 0
	if g.windowScale[1] > 0 {
		front = !front
	}
	if g.frontFaceCW {
		front = !front
	}
	if g.cullFace && (front && g.cullFront || !front && g.cullBack) {
		return
	}
	for index := 1; index+1 < len(window); index++ {
		surface.triangle(fragment, &window[0], &window[index], &window[index+1])
	}
}

const (
	grpPrimitivePoints = iota
	grpPrimitiveLines
	grpPrimitiveLineLoop
	grpPrimitiveLineStrip
	grpPrimitiveTriangles
	grpPrimitiveTriangleStrip
	grpPrimitiveTriangleFan
)

// drawPrimitives assembles triangles from an already shaded vertex list.
// Points and lines are not produced by the titles this module was written
// against and are ignored.
func (g *grpGeometryState) drawPrimitives(
	surface *grpSurface,
	fragment *grpFragmentState,
	mode uint8,
	vertices []grpClipVertex,
) {
	switch mode {
	case grpPrimitiveTriangles:
		for index := 0; index+2 < len(vertices); index += 3 {
			g.drawTriangle(surface, fragment, &vertices[index], &vertices[index+1], &vertices[index+2])
		}
	case grpPrimitiveTriangleStrip:
		for index := 0; index+2 < len(vertices); index++ {
			if index&1 == 0 {
				g.drawTriangle(surface, fragment, &vertices[index], &vertices[index+1], &vertices[index+2])
			} else {
				g.drawTriangle(surface, fragment, &vertices[index+1], &vertices[index], &vertices[index+2])
			}
		}
	case grpPrimitiveTriangleFan:
		for index := 1; index+1 < len(vertices); index++ {
			g.drawTriangle(surface, fragment, &vertices[0], &vertices[index], &vertices[index+1])
		}
	}
}
