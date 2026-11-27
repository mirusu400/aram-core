package system

import "encoding/binary"

// This file decodes Q3Dimension command arrays. An array is a 64-byte header
// followed by state records and at most one marker; a header with a vertex
// count also describes one primitive batch whose attribute data lives in ARM
// memory. The ARM-side library keeps every value the module needs in these
// records, so the module's whole register file is "the last copy of each
// record". That register file and the depth buffer live in DSP-private memory
// and therefore travel with a machine snapshot.

const (
	qualcommGraphicsRegisterSlots    = 32
	qualcommGraphicsRegisterSlotSize = 80
	qualcommGraphicsLightSlots       = 8
	qualcommGraphicsRegisterBase     = 0x10
	qualcommGraphicsLightBase        = qualcommGraphicsRegisterBase +
		qualcommGraphicsRegisterSlots*qualcommGraphicsRegisterSlotSize
	qualcommGraphicsRegisterFileSize = 0x1000
	qualcommGraphicsDepthBase        = qualcommGraphicsRegisterFileSize
	qualcommGraphicsMaximumSide      = 512
	qualcommGraphicsDSPPrivateSize   = qualcommGraphicsDepthBase +
		qualcommGraphicsMaximumSide*qualcommGraphicsMaximumSide*2
)

// State record opcodes.
const (
	grpOpEnables       = 0
	grpOpCurrent       = 1
	grpOpCull          = 2
	grpOpLightModel    = 3
	grpOpFog           = 4
	grpOpTexture       = 5
	grpOpMaterial      = 6
	grpOpFragment      = 7
	grpOpModelView     = 8
	grpOpRasterMode    = 9
	grpOpMVP           = 10
	grpOpNormalMatrix  = 11
	grpOpViewport      = 12
	grpOpClearValues   = 13
	grpOpSurfaceWords  = 14
	grpOpMasks         = 15
	grpOpStencil       = 16
	grpOpSurface       = 17
	grpOpLight         = 19
	grpOpTextureMatrix = 20
)

// Markers are four-byte records that end an array.
const (
	grpMarkerClearDepthScissored   = 34
	grpMarkerSync0                 = 37
	grpMarkerSyncFrame             = 38
	grpMarkerSync1                 = 39
	grpMarkerClearDepth            = 45
	grpMarkerClearStencilScissored = 46
	grpMarkerClearStencil          = 47
)

// Event codes the ARM-side service counts its sync markers down with.
const (
	qualcommGraphicsDSPEventSync0    = 1
	qualcommGraphicsDSPEventSync1    = 2
	qualcommGraphicsDSPEventFinished = 3
	qualcommGraphicsDSPEventError    = 4
)

var qualcommGraphicsRecordSize = [qualcommGraphicsRegisterSlots]uint8{
	grpOpEnables: 32, grpOpCurrent: 28, grpOpCull: 8, grpOpLightModel: 16,
	grpOpFog: 24, grpOpTexture: 32, grpOpMaterial: 44, grpOpFragment: 28,
	grpOpModelView: 68, grpOpRasterMode: 12, grpOpMVP: 68,
	grpOpNormalMatrix: 40, grpOpViewport: 16, grpOpClearValues: 12,
	grpOpSurfaceWords: 8, grpOpMasks: 20, grpOpStencil: 16, grpOpSurface: 24,
	grpOpLight: 76, grpOpTextureMatrix: 68,
}

type qualcommGraphicsState struct {
	eventIndex uint16
	registers  [qualcommGraphicsRegisterSlots][qualcommGraphicsRegisterSlotSize]byte
	lights     [qualcommGraphicsLightSlots][qualcommGraphicsRegisterSlotSize]byte
}

func (s *qualcommGraphicsState) load(dsp qualcommGraphicsDSPMemory, offset uint32) {
	raw := make([]byte, qualcommGraphicsRegisterFileSize)
	dsp.memory.ReadMemory(dsp.base+offset, raw)
	s.eventIndex = binary.LittleEndian.Uint16(raw[0:2])
	for slot := range s.registers {
		copy(s.registers[slot][:], raw[qualcommGraphicsRegisterBase+slot*qualcommGraphicsRegisterSlotSize:])
	}
	for slot := range s.lights {
		copy(s.lights[slot][:], raw[qualcommGraphicsLightBase+slot*qualcommGraphicsRegisterSlotSize:])
	}
}

func (s *qualcommGraphicsState) store(dsp qualcommGraphicsDSPMemory, offset uint32) {
	raw := make([]byte, qualcommGraphicsRegisterFileSize)
	binary.LittleEndian.PutUint16(raw[0:2], s.eventIndex)
	for slot := range s.registers {
		copy(raw[qualcommGraphicsRegisterBase+slot*qualcommGraphicsRegisterSlotSize:], s.registers[slot][:])
	}
	for slot := range s.lights {
		copy(raw[qualcommGraphicsLightBase+slot*qualcommGraphicsRegisterSlotSize:], s.lights[slot][:])
	}
	dsp.memory.WriteMemory(dsp.base+offset, raw)
}

func (s *qualcommGraphicsState) u16(opcode, offset int) uint16 {
	return binary.LittleEndian.Uint16(s.registers[opcode][offset:])
}

func (s *qualcommGraphicsState) s16(opcode, offset int) int16 {
	return int16(s.u16(opcode, offset))
}

func (s *qualcommGraphicsState) u32(opcode, offset int) uint32 {
	return binary.LittleEndian.Uint32(s.registers[opcode][offset:])
}

func (s *qualcommGraphicsState) fixed(opcode, offset int) float64 {
	return float64(int32(s.u32(opcode, offset))) / 65536
}

func grpUnit16(value uint16) float64 { return float64(value) / 65535 }

func grpColor16(raw []byte) grpColor {
	return grpColor{
		grpUnit16(binary.LittleEndian.Uint16(raw[0:])), grpUnit16(binary.LittleEndian.Uint16(raw[2:])),
		grpUnit16(binary.LittleEndian.Uint16(raw[4:])), grpUnit16(binary.LittleEndian.Uint16(raw[6:])),
	}
}

// grpColor88 reads four 8.8 fixed-point components (1.0 is 0x100).
func grpColor88(raw []byte) grpColor {
	var out grpColor
	for i := range out {
		out[i] = float64(binary.LittleEndian.Uint16(raw[i*2:])) / 256
	}
	return out
}

func grpColorFixed(raw []byte) grpColor {
	var out grpColor
	for i := range out {
		out[i] = float64(int32(binary.LittleEndian.Uint32(raw[i*4:]))) / 65536
	}
	return out
}

// matrix reads a row-major 16.16 matrix record into column-major form.
func (s *qualcommGraphicsState) matrix(opcode int) grpMat4 {
	var out grpMat4
	for row := 0; row < 4; row++ {
		for column := 0; column < 4; column++ {
			out[column*4+row] = s.fixed(opcode, 4+(row*4+column)*4)
		}
	}
	return out
}

// grpCompareCode maps the module's comparison codes. The depth function uses
// the same codes on the inverted stored depth, which is why the ARM sends it
// mirrored.
func grpCompareCode(code uint16) uint8 {
	switch code {
	case 0:
		return grpCompareNever
	case 2:
		return grpCompareLess
	case 3:
		return grpCompareLessEqual
	case 4:
		return grpCompareEqual
	case 5:
		return grpCompareGreater
	case 6:
		return grpCompareGreaterEqual
	case 7:
		return grpCompareNotEqual
	default:
		return grpCompareAlways
	}
}

// grpBlendCode maps a blend factor code. Code 2 is "the other colour": the
// destination colour in a source factor and the source colour in a
// destination factor.
func grpBlendCode(code uint16, source bool) uint8 {
	switch code {
	case 0:
		return grpBlendZero
	case 1:
		return grpBlendSrcAlpha
	case 5:
		return grpBlendOneMinusSrcAlpha
	case 3:
		return grpBlendDstAlpha
	case 7:
		return grpBlendOneMinusDstAlpha
	case 2:
		if source {
			return grpBlendDstColor
		}
		return grpBlendSrcColor
	case 6:
		if source {
			return grpBlendOneMinusDstColor
		}
		return grpBlendOneMinusSrcColor
	case 15:
		return grpBlendSrcAlphaSaturate
	default:
		return grpBlendOne
	}
}

// grpTarget is the render target named by the surface record, restricted to
// the rows the current scissor can touch.
type grpTarget struct {
	surface grpSurface
	address uint32
	stride  int
	row0    int
	row1    int
	loaded  bool
	dirty   bool
}

func (s *qualcommGraphicsState) target() (grpTarget, bool) {
	size := s.u32(grpOpSurface, 20)
	width, height := int(size&0x1ff), int(size>>9&0x1ff)
	stride := int(s.u16(grpOpSurface, 4))
	address := s.u32(grpOpSurface, 8)
	// Only 16-bit RGB565 targets are modelled; that is the only colour format
	// the handset's window surfaces use.
	if width == 0 || height == 0 || width > qualcommGraphicsMaximumSide ||
		height > qualcommGraphicsMaximumSide || stride < width*2 || address == 0 ||
		s.u16(grpOpSurface, 2) != 0 {
		return grpTarget{}, false
	}
	return grpTarget{
		surface: grpSurface{width: width, height: height},
		address: address, stride: stride,
	}, true
}

// scissor returns the scissor rectangle as x0, y0, x1, y1 (exclusive) clipped
// to the surface.
func (s *qualcommGraphicsState) scissor(width, height int) [4]int {
	x0, y0 := int(s.s16(grpOpFragment, 18)), int(s.s16(grpOpFragment, 20))
	y1 := y0 + int(s.s16(grpOpFragment, 22)) + 1
	x1 := x0 + int(s.s16(grpOpFragment, 24)) + 1
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > width {
		x1 = width
	}
	if y1 > height {
		y1 = height
	}
	if x1 < x0 {
		x1 = x0
	}
	if y1 < y0 {
		y1 = y0
	}
	return [4]int{x0, y0, x1, y1}
}

// open loads the colour and depth rows [row0, row1) of the target.
func (t *grpTarget) open(dsp qualcommGraphicsDSPMemory, private uint32, row0, row1 int) {
	if t.loaded || row0 >= row1 {
		return
	}
	surface := &t.surface
	surface.color = make([]uint16, surface.width*surface.height)
	surface.depth = make([]uint16, surface.width*surface.height)
	t.row0, t.row1 = row0, row1
	raw := make([]byte, surface.width*2)
	for y := row0; y < row1; y++ {
		line := surface.color[y*surface.width : (y+1)*surface.width]
		if dsp.memory.ReadMemory(t.address+uint32(y*t.stride), raw) {
			for x := range line {
				line[x] = binary.LittleEndian.Uint16(raw[x*2:])
			}
		}
		depth := surface.depth[y*surface.width : (y+1)*surface.width]
		if dsp.memory.ReadMemory(dsp.base+private+qualcommGraphicsDepthBase+uint32(y*surface.width*2), raw) {
			for x := range depth {
				depth[x] = binary.LittleEndian.Uint16(raw[x*2:])
			}
		}
	}
	t.loaded = true
}

// close writes the loaded rows back.
func (t *grpTarget) close(dsp qualcommGraphicsDSPMemory, private uint32) {
	if !t.loaded || !t.dirty {
		return
	}
	surface := &t.surface
	raw := make([]byte, surface.width*2)
	for y := t.row0; y < t.row1; y++ {
		for x, pixel := range surface.color[y*surface.width : (y+1)*surface.width] {
			binary.LittleEndian.PutUint16(raw[x*2:], pixel)
		}
		dsp.memory.WriteMemory(t.address+uint32(y*t.stride), raw)
		for x, depth := range surface.depth[y*surface.width : (y+1)*surface.width] {
			binary.LittleEndian.PutUint16(raw[x*2:], depth)
		}
		dsp.memory.WriteMemory(dsp.base+private+qualcommGraphicsDepthBase+uint32(y*surface.width*2), raw)
	}
}

// execute runs one command array and returns the events it owes the ARM, in
// order.
func (s *qualcommGraphicsState) execute(dsp qualcommGraphicsDSPMemory, private uint32, array []byte) []uint16 {
	var events []uint16
	marker := 0
	for offset := qualcommGraphicsDSPArrayHeader; offset+4 <= len(array); {
		opcode := int(binary.LittleEndian.Uint16(array[offset:]))
		if opcode >= qualcommGraphicsRegisterSlots || qualcommGraphicsRecordSize[opcode] == 0 {
			// A marker is the last record of its array; anything else here
			// is not a record this module knows.
			marker = opcode
			break
		}
		size := int(qualcommGraphicsRecordSize[opcode])
		if offset+size > len(array) {
			return append(events, qualcommGraphicsDSPEventError)
		}
		record := array[offset : offset+size]
		if opcode == grpOpLight {
			if slot := int(binary.LittleEndian.Uint16(record[2:])); slot < qualcommGraphicsLightSlots {
				copy(s.lights[slot][:], record)
			}
		} else {
			copy(s.registers[opcode][:], record)
		}
		offset += size
	}

	target, valid := s.target()
	if valid {
		if binary.LittleEndian.Uint16(array[6:]) != 0 {
			s.draw(dsp, private, &target, array)
		}
		switch marker {
		case grpMarkerClearDepth:
			s.clearDepth(dsp, private, &target, [4]int{0, 0, target.surface.width, target.surface.height})
		case grpMarkerClearDepthScissored:
			s.clearDepth(dsp, private, &target, s.scissor(target.surface.width, target.surface.height))
		}
		target.close(dsp, private)
	}
	switch marker {
	case 0, grpMarkerClearDepth, grpMarkerClearDepthScissored,
		grpMarkerClearStencil, grpMarkerClearStencilScissored:
	case grpMarkerSync0:
		events = append(events, qualcommGraphicsDSPEventSync0)
	case grpMarkerSyncFrame:
		events = append(events, qualcommGraphicsDSPEventFinished)
	case grpMarkerSync1:
		events = append(events, qualcommGraphicsDSPEventSync1)
	default:
		events = append(events, qualcommGraphicsDSPEventError)
	}
	return events
}

func (s *qualcommGraphicsState) clearDepth(
	dsp qualcommGraphicsDSPMemory,
	private uint32,
	target *grpTarget,
	rectangle [4]int,
) {
	if rectangle[0] >= rectangle[2] || rectangle[1] >= rectangle[3] {
		return
	}
	target.open(dsp, private, rectangle[1], rectangle[3])
	if rectangle[1] < target.row0 || rectangle[3] > target.row1 {
		return
	}
	value := s.u16(grpOpClearValues, 2)
	for y := rectangle[1]; y < rectangle[3]; y++ {
		row := target.surface.depth[y*target.surface.width:]
		for x := rectangle[0]; x < rectangle[2]; x++ {
			row[x] = value
		}
	}
	target.dirty = true
}

func (s *qualcommGraphicsState) geometry() *grpGeometryState {
	enabled := func(offset int) bool { return s.u16(grpOpEnables, offset) != 0 }
	geometry := &grpGeometryState{
		modelView:     s.matrix(grpOpModelView),
		mvp:           s.matrix(grpOpMVP),
		lighting:      enabled(2),
		normalize:     enabled(4),
		colorMaterial: enabled(16),
		twoSide:       s.u16(grpOpLightModel, 4) != 0,
		sceneAmbient:  grpColor88(s.registers[grpOpLightModel][8:]),
		fog:           enabled(14),
		fogDensity:    s.fixed(grpOpFog, 12),
		fogStart:      s.fixed(grpOpFog, 16),
		fogEnd:        s.fixed(grpOpFog, 20),
		cullFace:      s.u16(grpOpCull, 2) != 0,
		frontFaceCW:   s.s16(grpOpCull, 6) < 0,
	}
	for index := range geometry.normalMatrix {
		geometry.normalMatrix[index] = s.fixed(grpOpNormalMatrix, 4+index*4)
	}
	geometry.textureMatrix[0] = grpIdentity()
	if s.u16(grpOpEnables, 10) != 0 {
		geometry.textureMatrix[0] = s.matrix(grpOpTextureMatrix)
	}
	for unit := 1; unit < grpTextureUnits; unit++ {
		geometry.textureMatrix[unit] = grpIdentity()
	}
	switch face := s.s16(grpOpCull, 4); {
	case face < 0:
		geometry.cullFront = true
	case face > 0:
		geometry.cullBack = true
	default:
		geometry.cullFront, geometry.cullBack = true, true
	}
	switch mode := s.s16(grpOpFog, 2); {
	case mode < 0:
		geometry.fogMode = grpFogLinear
	case mode == 0:
		geometry.fogMode = grpFogExp
	default:
		geometry.fogMode = grpFogExp2
	}

	material := s.registers[grpOpMaterial][:]
	geometry.material = grpMaterial{
		shininess: float64(binary.LittleEndian.Uint16(material[2:])) / 256,
		emission:  grpColor16(material[4:]),
		ambient:   grpColor16(material[12:]),
		diffuse:   grpColor16(material[20:]),
		specular:  grpColorFixed(material[28:]),
	}
	count := int(s.u16(grpOpEnables, 26))
	for slot := 0; slot < count && slot < qualcommGraphicsLightSlots; slot++ {
		record := s.lights[slot][:]
		light := grpLight{
			enabled:       true,
			ambient:       grpColor88(record[10:]),
			diffuse:       grpColor88(record[24:]),
			specular:      grpColorFixed(record[32:]),
			spotCosCutoff: -1,
			spotExponent:  float64(binary.LittleEndian.Uint16(record[8:])) / 256,
		}
		for i := range light.position {
			light.position[i] = float64(int32(binary.LittleEndian.Uint32(record[48+i*4:]))) / 65536
		}
		for i := range light.spotDirection {
			light.spotDirection[i] = float64(int16(binary.LittleEndian.Uint16(record[18+i*2:]))) / 16384
		}
		if binary.LittleEndian.Uint16(record[4:]) != 0 {
			light.spotCosCutoff = float64(binary.LittleEndian.Uint16(record[6:])) / 16384
		}
		light.constantAttenuation = float64(int32(binary.LittleEndian.Uint32(record[64:]))) / 65536
		light.linearAttenuation = float64(int32(binary.LittleEndian.Uint32(record[68:]))) / 65536
		light.quadraticAttenuation = float64(int32(binary.LittleEndian.Uint32(record[72:]))) / 65536
		geometry.lights[slot] = light
	}

	// window = centre + ndc * (w/2, -h/2); the centre is in 1/64 pixel with a
	// top-left origin, and depth arrives as stored = offset + scale * ndc.
	geometry.windowScale = [3]float64{
		float64(s.u16(grpOpViewport, 2)) / 2,
		float64(s.s16(grpOpViewport, 4)) / 2,
		float64(s.s16(grpOpViewport, 6)),
	}
	geometry.windowOffset = [3]float64{
		float64(s.u16(grpOpViewport, 8)) / 64,
		float64(s.u16(grpOpViewport, 10)) / 64,
		float64(s.u16(grpOpViewport, 12)),
	}
	return geometry
}

func (s *qualcommGraphicsState) fragment(dsp qualcommGraphicsDSPMemory, target *grpTarget) *grpFragmentState {
	enabled := func(offset int) bool { return s.u16(grpOpEnables, offset) != 0 }
	mask := s.u16(grpOpMasks, 2)
	fragment := &grpFragmentState{
		scissor:    s.scissor(target.surface.width, target.surface.height),
		depthTest:  enabled(22),
		depthFunc:  grpCompareCode(s.u16(grpOpFragment, 14)),
		depthWrite: s.u16(grpOpMasks, 4) != 0,
		alphaTest:  enabled(18),
		alphaFunc:  grpCompareCode(s.u16(grpOpFragment, 2)),
		alphaRef:   float64(s.u16(grpOpFragment, 4)) / 255,
		blend:      enabled(20),
		blendSrc:   grpBlendCode(s.u16(grpOpFragment, 6), true),
		blendDst:   grpBlendCode(s.u16(grpOpFragment, 8), false),
		colorMask:  [4]bool{mask&4 != 0, mask&2 != 0, mask&1 != 0, mask&8 != 0},
		flat:       enabled(6),
		fog:        enabled(14),
		fogColor:   grpColor16(s.registers[grpOpFog][4:]),
	}
	if s.u16(grpOpMasks, 6)&1 != 0 {
		fragment.polygonOffsetFillEnable = true
		// A positive offset moves the fragment away from the viewer, which is
		// toward zero in the stored depth.
		fragment.polygonOffsetUnits = -float64(int32(s.u32(grpOpMasks, 16)))
	}
	if enabled(8) {
		if texture := s.texture(dsp); texture != nil {
			fragment.textures[0] = texture
			environment := s.u32(grpOpTexture, 28)
			fragment.textureEnvColor[0] = grpColor{
				float64(environment>>16&0xff) / 255, float64(environment>>8&0xff) / 255,
				float64(environment&0xff) / 255, float64(environment>>24) / 255,
			}
			switch s.u16(grpOpTexture, 8) {
			case 0:
				fragment.textureEnv[0] = grpEnvReplace
			case 2:
				fragment.textureEnv[0] = grpEnvDecal
			case 3:
				fragment.textureEnv[0] = grpEnvBlend
			case 4:
				fragment.textureEnv[0] = grpEnvAdd
			default:
				fragment.textureEnv[0] = grpEnvModulate
			}
		}
	}
	return fragment
}

// texture decodes level 0 of the bound texture. Texels are 16-bit and stored
// in vertical strips four texels wide.
func (s *qualcommGraphicsState) texture(dsp qualcommGraphicsDSPMemory) *grpTexture {
	address := s.u32(grpOpTexture, 24) << 2
	widthLog, heightLog := s.u16(grpOpTexture, 14), s.u16(grpOpTexture, 6)
	if address == 0 || widthLog > 8 || heightLog > 8 {
		return nil
	}
	width, height := 1<<widthLog, 1<<heightLog
	rows := height
	if rows < 4 {
		rows = 4
	}
	strips := (width + 3) / 4
	raw := make([]byte, strips*rows*8)
	if !dsp.memory.ReadMemory(address, raw) {
		return nil
	}
	format := s.u16(grpOpTexture, 4)
	texture := &grpTexture{
		width: width, height: height,
		texels:     make([]grpColor, width*height),
		baseFormat: grpTexelRGBA,
		linear:     s.u16(grpOpTexture, 10) != 0,
		clampS:     s.u16(grpOpTexture, 20) != 0,
		clampT:     s.u16(grpOpTexture, 22) != 0,
	}
	if format == 0 {
		texture.baseFormat = grpTexelRGB
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			texel := binary.LittleEndian.Uint16(raw[((x>>2)*rows+y)*8+(x&3)*2:])
			var color grpColor
			switch format {
			case 1: // RGBA 5551
				color = grpColor{
					float64(texel>>11) / 31, float64(texel>>6&0x1f) / 31,
					float64(texel>>1&0x1f) / 31, float64(texel & 1),
				}
			case 2: // RGBA 4444
				color = grpColor{
					float64(texel>>12) / 15, float64(texel>>8&0xf) / 15,
					float64(texel>>4&0xf) / 15, float64(texel&0xf) / 15,
				}
			default:
				color = grpUnpack565(texel)
			}
			texture.texels[y*width+x] = color
		}
	}
	return texture
}

// attributeReader fetches one vertex attribute stream from ARM memory.
type grpAttribute struct {
	data  []byte
	code  uint16
	count uint16
}

func (s *qualcommGraphicsState) attribute(dsp qualcommGraphicsDSPMemory, array []byte, index int) grpAttribute {
	address := binary.LittleEndian.Uint32(array[0x2c+index*4:])
	length := int(binary.LittleEndian.Uint16(array[0x0e+index*2:]))
	if address == 0 || length == 0 {
		return grpAttribute{}
	}
	data := make([]byte, length)
	if !dsp.memory.ReadMemory(address, data) {
		return grpAttribute{}
	}
	return grpAttribute{
		data:  data,
		code:  binary.LittleEndian.Uint16(array[0x18+index*2:]),
		count: binary.LittleEndian.Uint16(array[0x20+index*2:]),
	}
}

func grpFixedAt(data []byte, offset int) float64 {
	return float64(int32(binary.LittleEndian.Uint32(data[offset:]))) / 65536
}

func grpShortAt(data []byte, offset int) float64 {
	return float64(int16(binary.LittleEndian.Uint16(data[offset:])))
}

// grpBlockNormal decodes three mantissas and an exponent: m * 2^(e-14).
func grpBlockNormal(data []byte) grpVec3 {
	scale := 1.0 / 16384
	for exponent := int(int16(binary.LittleEndian.Uint16(data[6:]))); exponent > 0; exponent-- {
		scale = grpMul(scale, 2)
	}
	return grpVec3{
		grpMul(grpShortAt(data, 0), scale), grpMul(grpShortAt(data, 2), scale), grpMul(grpShortAt(data, 4), scale),
	}
}

func (s *qualcommGraphicsState) draw(
	dsp qualcommGraphicsDSPMemory,
	private uint32,
	target *grpTarget,
	array []byte,
) {
	var mode uint8
	switch binary.LittleEndian.Uint16(array[4:]) {
	case 5:
		mode = grpPrimitiveTriangles
	case 7:
		mode = grpPrimitiveTriangleStrip
	case 6:
		mode = grpPrimitiveTriangleFan
	default:
		return // points and lines
	}
	vertexCount := int(binary.LittleEndian.Uint16(array[6:]))
	positions := s.attribute(dsp, array, 0)
	positionSize := 0
	switch {
	case positions.code == 0 && (positions.count == 3 || positions.count == 4):
		positionSize = int(positions.count) * 4
	case positions.code == 2 && (positions.count == 3 || positions.count == 4):
		positionSize = int(positions.count) * 2
	}
	if positionSize == 0 || len(positions.data) < vertexCount*positionSize {
		return
	}
	colors := s.attribute(dsp, array, 1)
	colorSize := 0
	switch colors.code {
	case 1:
		colorSize = 4
	case 2:
		colorSize = 8
	}
	if len(colors.data) < vertexCount*colorSize {
		colorSize = 0
	}
	normals := s.attribute(dsp, array, 2)
	normalSize := 0
	switch {
	case normals.code == 0:
		normalSize = 12
	case normals.code == 2 && normals.count == 3:
		normalSize = 6
	case normals.code == 2:
		normalSize = 8
	}
	if len(normals.data) < vertexCount*normalSize || len(normals.data) == 0 {
		normalSize = 0
	}
	coordinates := s.attribute(dsp, array, 3)
	coordinateSize := 0
	switch {
	case coordinates.code == 0 && (coordinates.count == 2 || coordinates.count == 4):
		coordinateSize = int(coordinates.count) * 4
	case coordinates.code == 2 && (coordinates.count == 2 || coordinates.count == 4):
		coordinateSize = int(coordinates.count) * 2
	}
	if len(coordinates.data) < vertexCount*coordinateSize || len(coordinates.data) == 0 {
		coordinateSize = 0
	}

	current := s.registers[grpOpCurrent][:]
	currentColor := grpColor16(current[2:])
	currentNormal := grpBlockNormal(current[10:])
	// Only the fractional 16 bits of the current texture coordinate are sent.
	currentCoordinate := grpVec4{
		float64(binary.LittleEndian.Uint16(current[18:])) / 65536,
		float64(binary.LittleEndian.Uint16(current[20:])) / 65536, 0, 1,
	}

	geometry := s.geometry()
	fragment := s.fragment(dsp, target)
	if fragment.scissor[1] >= fragment.scissor[3] || fragment.scissor[0] >= fragment.scissor[2] {
		return
	}
	target.open(dsp, private, fragment.scissor[1], fragment.scissor[3])
	if fragment.scissor[1] < target.row0 {
		fragment.scissor[1] = target.row0
	}
	if fragment.scissor[3] > target.row1 {
		fragment.scissor[3] = target.row1
	}

	shaded := make([]grpClipVertex, vertexCount)
	for index := 0; index < vertexCount; index++ {
		in := grpInputVertex{
			position: grpVec4{0, 0, 0, 1},
			normal:   currentNormal,
			color:    currentColor,
		}
		in.tex[0] = currentCoordinate
		offset := index * positionSize
		for component := 0; component < int(positions.count); component++ {
			if positions.code == 0 {
				in.position[component] = grpFixedAt(positions.data, offset+component*4)
			} else {
				in.position[component] = grpShortAt(positions.data, offset+component*2)
			}
		}
		switch colorSize {
		case 4:
			raw := colors.data[index*4:]
			in.color = grpColor{
				float64(raw[0]) / 255, float64(raw[1]) / 255, float64(raw[2]) / 255, float64(raw[3]) / 255,
			}
		case 8:
			in.color = grpColor16(colors.data[index*8:])
		}
		switch normalSize {
		case 12:
			raw := normals.data[index*12:]
			in.normal = grpVec3{grpFixedAt(raw, 0), grpFixedAt(raw, 4), grpFixedAt(raw, 8)}
		case 6:
			raw := normals.data[index*6:]
			in.normal = grpVec3{grpShortAt(raw, 0) / 32768, grpShortAt(raw, 2) / 32768, grpShortAt(raw, 4) / 32768}
		case 8:
			in.normal = grpBlockNormal(normals.data[index*8:])
		}
		if coordinateSize != 0 {
			raw := coordinates.data[index*coordinateSize:]
			coordinate := grpVec4{0, 0, 0, 1}
			for component := 0; component < int(coordinates.count); component++ {
				if coordinates.code == 0 {
					coordinate[component] = grpFixedAt(raw, component*4)
				} else {
					coordinate[component] = grpShortAt(raw, component*2)
				}
			}
			in.tex[0] = coordinate
		}
		shaded[index] = geometry.shade(&in)
	}

	if indexCount := int(binary.LittleEndian.Uint16(array[8:])); indexCount != 0 {
		raw := make([]byte, indexCount*2)
		address := binary.LittleEndian.Uint32(array[0x3c:])
		if address == 0 || !dsp.memory.ReadMemory(address, raw) {
			return
		}
		indexed := make([]grpClipVertex, 0, indexCount)
		for index := 0; index < indexCount; index++ {
			vertex := int(binary.LittleEndian.Uint16(raw[index*2:]))
			if vertex >= vertexCount {
				return
			}
			indexed = append(indexed, shaded[vertex])
		}
		shaded = indexed
	}
	geometry.drawPrimitives(&target.surface, fragment, mode, shaded)
	target.dirty = true
}
