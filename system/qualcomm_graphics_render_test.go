package system

import (
	"encoding/binary"
	"testing"
)

// grpArrayBuilder assembles a command array the way the ARM-side packer does.
type grpArrayBuilder struct {
	data []byte
}

func newGRPArray(id uint32) *grpArrayBuilder {
	builder := &grpArrayBuilder{data: make([]byte, qualcommGraphicsDSPArrayHeader)}
	binary.LittleEndian.PutUint32(builder.data[0:], id)
	return builder
}

// record appends one zeroed state record and returns its offset.
func (b *grpArrayBuilder) record(opcode int) int {
	start := len(b.data)
	b.data = append(b.data, make([]byte, qualcommGraphicsRecordSize[opcode])...)
	binary.LittleEndian.PutUint16(b.data[start:], uint16(opcode))
	return start
}

func (b *grpArrayBuilder) put16(record, offset int, value uint16) {
	binary.LittleEndian.PutUint16(b.data[record+offset:], value)
}

func (b *grpArrayBuilder) put32(record, offset int, value uint32) {
	binary.LittleEndian.PutUint32(b.data[record+offset:], value)
}

func (b *grpArrayBuilder) identity(opcode int) {
	record := b.record(opcode)
	for index := 0; index < 4; index++ {
		b.put32(record, 4+index*20, 0x10000)
	}
}

// current sets the current colour (0.16 per channel) and a +z normal.
func (b *grpArrayBuilder) current(red, green, blue uint16) {
	record := b.record(grpOpCurrent)
	b.put16(record, 2, red)
	b.put16(record, 4, green)
	b.put16(record, 6, blue)
	b.put16(record, 8, 0xffff)
	b.put16(record, 14, 0x4000)
}

func (b *grpArrayBuilder) marker(opcode uint16) {
	b.data = append(b.data, byte(opcode), byte(opcode>>8), 0, 0)
}

func (b *grpArrayBuilder) bytes() []byte {
	binary.LittleEndian.PutUint16(b.data[0x0c:], uint16(len(b.data)))
	return b.data
}

const (
	grpTestColorAddress    = 0x00030000
	grpTestPositionAddress = 0x00040000
	grpTestCoordAddress    = 0x00041000
	grpTestIndexAddress    = 0x00042000
	grpTestTextureAddress  = 0x00050000
	grpTestSide            = 16
)

// grpTestScene is the state block a context sends before its first draw: a
// 16x16 RGB565 surface, a viewport covering it, identity matrices, depth
// testing with GL_LESS, and an opaque white current colour. It returns the
// builder and the offset of the enables record.
func grpTestScene(id uint32) (*grpArrayBuilder, int) {
	builder := newGRPArray(id)
	builder.identity(grpOpModelView)
	builder.identity(grpOpMVP)
	enables := builder.record(grpOpEnables)
	builder.put16(enables, 12, 1)
	builder.put16(enables, 22, 1) // depth test
	builder.current(0xffff, 0xffff, 0xffff)
	cull := builder.record(grpOpCull)
	builder.put16(cull, 4, 1)
	builder.put16(cull, 6, 1)
	fragment := builder.record(grpOpFragment)
	builder.put16(fragment, 2, 1)  // alpha ALWAYS
	builder.put16(fragment, 6, 4)  // blend ONE
	builder.put16(fragment, 14, 5) // depth LESS, mirrored
	builder.put16(fragment, 22, grpTestSide-1)
	builder.put16(fragment, 24, grpTestSide-1)
	viewport := builder.record(grpOpViewport)
	builder.put16(viewport, 2, grpTestSide)
	builder.put16(viewport, 4, uint16(0x10000-grpTestSide))
	builder.put16(viewport, 6, 0x8001)
	builder.put16(viewport, 8, grpTestSide*32)
	builder.put16(viewport, 10, grpTestSide*32)
	builder.put16(viewport, 12, 0x7fff)
	surface := builder.record(grpOpSurface)
	builder.put16(surface, 4, grpTestSide*2)
	builder.put32(surface, 8, grpTestColorAddress)
	builder.put32(surface, 16, 0x78000000)
	builder.put32(surface, 20, grpTestSide|grpTestSide<<9)
	masks := builder.record(grpOpMasks)
	builder.put16(masks, 2, 0x0f)
	builder.put16(masks, 4, 1)
	builder.put16(masks, 8, 0xffff)
	builder.record(grpOpClearValues)
	return builder, enables
}

// draw turns the array into a draw of 16.16 positions with the given
// primitive code.
func (b *grpArrayBuilder) draw(h *graphicsDSPHarness, primitive uint16, positions [][3]float64) {
	raw := make([]byte, len(positions)*12)
	for index, position := range positions {
		for component, value := range position {
			binary.LittleEndian.PutUint32(raw[index*12+component*4:], uint32(int32(value*65536)))
		}
	}
	h.load(grpTestPositionAddress, raw)
	b.put16(0, 0x04, primitive)
	b.put16(0, 0x06, uint16(len(positions)))
	b.put16(0, 0x0e, uint16(len(raw)))
	b.put16(0, 0x20, 3)
	b.put32(0, 0x2c, grpTestPositionAddress)
}

func (h *graphicsDSPHarness) startGraphics() {
	h.t.Helper()
	h.write(0x70000300, Width32, 0x12000315)
	h.write(0x70001000, Width32, 0)
	h.write(0x70001008, Width32, 0x88000000)
}

func (h *graphicsDSPHarness) pixel(x, y int) uint16 {
	h.t.Helper()
	return uint16(h.read(grpTestColorAddress+uint32(y*grpTestSide*2+x*2), Width16))
}

// events drains the event ring the way the ARM-side pump does.
func (h *graphicsDSPHarness) events() []uint16 {
	h.t.Helper()
	var events []uint16
	for slot := uint32(0); slot < 4; slot++ {
		if event := uint16(h.read(0x70000100+slot*2, Width16)); event != qualcommGraphicsDSPNoEvent {
			events = append(events, event)
			h.write(0x70000100+slot*2, Width16, qualcommGraphicsDSPNoEvent)
		}
	}
	return events
}

func TestQualcommGraphicsDSPDrawsDepthTestedTriangles(t *testing.T) {
	h := newGraphicsDSPHarness(t)
	h.startGraphics()
	// The ARM clears colour itself; depth is cleared by marker 45 to the
	// record's value (0 is the far plane in the stored, inverted form).
	scene, _ := grpTestScene(1)
	scene.marker(grpMarkerClearDepth)
	h.submit(0x00020000, scene.bytes())
	if events := h.events(); len(events) != 0 {
		t.Fatalf("depth clear posted events %v", events)
	}

	// A white quad over the left half at mid depth.
	white := newGRPArray(2)
	white.draw(h, 7, [][3]float64{{-1, -1, 0}, {0, -1, 0}, {-1, 1, 0}, {0, 1, 0}})
	h.submit(0x00020000, white.bytes())
	if h.pixel(0, 0) != 0xffff || h.pixel(7, 15) != 0xffff || h.pixel(8, 0) != 0 {
		t.Fatalf("left quad pixels = %#04x %#04x %#04x", h.pixel(0, 0), h.pixel(7, 15), h.pixel(8, 0))
	}

	// A red full-screen quad behind it only fills the right half. The draw
	// carries its state change in the same array.
	red := newGRPArray(3)
	red.current(0xffff, 0, 0)
	red.draw(h, 7, [][3]float64{{-1, -1, 0.5}, {1, -1, 0.5}, {-1, 1, 0.5}, {1, 1, 0.5}})
	h.submit(0x00020000, red.bytes())
	if h.pixel(3, 3) != 0xffff || h.pixel(12, 3) != 0xf800 {
		t.Fatalf("depth-tested pixels = %#04x %#04x", h.pixel(3, 3), h.pixel(12, 3))
	}

	// The strip's first triangle is counter-clockwise, so the quad is a front
	// face: culling FRONT (-1) removes it, culling BACK (1) keeps it.
	for _, test := range []struct {
		face uint16
		want uint16
	}{{0xffff, 0xf800}, {1, 0x001f}} {
		blue := newGRPArray(4)
		blue.current(0, 0, 0xffff)
		cull := blue.record(grpOpCull)
		blue.put16(cull, 2, 0xffff)
		blue.put16(cull, 4, test.face)
		blue.put16(cull, 6, 1)
		blue.draw(h, 7, [][3]float64{{0, -1, -0.5}, {1, -1, -0.5}, {0, 1, -0.5}, {1, 1, -0.5}})
		h.submit(0x00020000, blue.bytes())
		if got := h.pixel(12, 3); got != test.want {
			t.Fatalf("cull face %#x pixel %#04x, want %#04x", test.face, got, test.want)
		}
	}
	if stamp := h.read(0x7000020c, Width32); stamp != 4 {
		t.Fatalf("completion stamp = %d", stamp)
	}

	// A scissored depth clear (marker 34) reopens only its rectangle: the
	// far red quad then wins inside it and still loses outside it. The row
	// of a draw that does not reach a pixel is neither read nor written.
	clear := newGRPArray(5)
	fragment := clear.record(grpOpFragment)
	clear.put16(fragment, 2, 1)
	clear.put16(fragment, 6, 4)
	clear.put16(fragment, 14, 5)
	clear.put16(fragment, 18, 2) // x0
	clear.put16(fragment, 20, 4) // y0
	clear.put16(fragment, 22, 3) // rows 4..7
	clear.put16(fragment, 24, 3) // columns 2..5
	clear.marker(grpMarkerClearDepthScissored)
	h.submit(0x00020000, clear.bytes())
	far := newGRPArray(6)
	full := far.record(grpOpFragment)
	far.put16(full, 2, 1)
	far.put16(full, 6, 4)
	far.put16(full, 14, 5)
	far.put16(full, 22, grpTestSide-1)
	far.put16(full, 24, grpTestSide-1)
	far.current(0xffff, 0, 0)
	far.draw(h, 7, [][3]float64{{-1, 0, 0.5}, {1, 0, 0.5}, {-1, 1, 0.5}, {1, 1, 0.5}})
	h.submit(0x00020000, far.bytes())
	if h.pixel(3, 5) != 0xf800 || h.pixel(3, 2) != 0xffff || h.pixel(6, 5) != 0xffff {
		t.Fatalf("after scissored depth clear: inside %#04x above %#04x beside %#04x",
			h.pixel(3, 5), h.pixel(3, 2), h.pixel(6, 5))
	}
	if h.pixel(3, 12) != 0xffff {
		t.Fatalf("row below the batch changed to %#04x", h.pixel(3, 12))
	}
}

func TestQualcommGraphicsDSPAnswersEachSyncMarkerOnce(t *testing.T) {
	h := newGraphicsDSPHarness(t)
	h.startGraphics()
	for _, test := range []struct {
		marker uint16
		event  uint16
	}{
		{grpMarkerSync0, qualcommGraphicsDSPEventSync0},
		{grpMarkerSyncFrame, qualcommGraphicsDSPEventFinished},
		{grpMarkerSync1, qualcommGraphicsDSPEventSync1},
		{0x7f, qualcommGraphicsDSPEventError},
	} {
		array := newGRPArray(9)
		array.marker(test.marker)
		h.submit(0x00020000, array.bytes())
		if events := h.events(); len(events) != 1 || events[0] != test.event {
			t.Fatalf("marker %d events = %v, want [%d]", test.marker, events, test.event)
		}
	}
}

func TestQualcommGraphicsDSPSamplesStripOrderedTexturesWithIndices(t *testing.T) {
	h := newGraphicsDSPHarness(t)
	h.startGraphics()
	// An 8x4 RGB565 texture: texel (x, y) lives at ((x>>2)*4 + y)*8 + (x&3)*2.
	// Columns 0-3 are red, columns 4-7 green.
	texels := make([]byte, 2*4*8)
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			color := uint16(0xf800)
			if x >= 4 {
				color = 0x07e0
			}
			binary.LittleEndian.PutUint16(texels[((x>>2)*4+y)*8+(x&3)*2:], color)
		}
	}
	h.load(grpTestTextureAddress, texels)

	scene, enables := grpTestScene(1)
	scene.put16(enables, 8, 1) // GL_TEXTURE_2D
	texture := scene.record(grpOpTexture)
	scene.put16(texture, 6, 2)  // log2 height
	scene.put16(texture, 14, 3) // log2 width; format 0 = RGB565, env 0 = REPLACE
	scene.put32(texture, 24, grpTestTextureAddress>>2)
	scene.marker(grpMarkerClearDepth)
	h.submit(0x00020000, scene.bytes())

	// Four corner vertices drawn through an index list as two triangles.
	draw := newGRPArray(2)
	draw.draw(h, 5, [][3]float64{{-1, 1, 0}, {1, 1, 0}, {-1, -1, 0}, {1, -1, 0}})
	coordinates := make([]byte, 4*8)
	for index, st := range [][2]uint32{{0, 0}, {0x10000, 0}, {0, 0x10000}, {0x10000, 0x10000}} {
		binary.LittleEndian.PutUint32(coordinates[index*8:], st[0])
		binary.LittleEndian.PutUint32(coordinates[index*8+4:], st[1])
	}
	h.load(grpTestCoordAddress, coordinates)
	draw.put16(0, 0x14, uint16(len(coordinates)))
	draw.put16(0, 0x26, 2)
	draw.put32(0, 0x38, grpTestCoordAddress)
	indices := make([]byte, 12)
	for index, value := range []uint16{0, 2, 1, 1, 2, 3} {
		binary.LittleEndian.PutUint16(indices[index*2:], value)
	}
	h.load(grpTestIndexAddress, indices)
	draw.put16(0, 0x08, 6)
	draw.put16(0, 0x16, uint16(len(indices)))
	draw.put32(0, 0x3c, grpTestIndexAddress)
	h.submit(0x00020000, draw.bytes())
	if h.pixel(2, 2) != 0xf800 || h.pixel(13, 13) != 0x07e0 || h.pixel(2, 13) != 0xf800 {
		t.Fatalf("textured pixels = %#04x %#04x %#04x", h.pixel(2, 2), h.pixel(13, 13), h.pixel(2, 13))
	}

	// An index past the vertex list is refused rather than read out of range.
	binary.LittleEndian.PutUint16(indices[0:], 9)
	h.load(grpTestIndexAddress, indices)
	for y := 0; y < grpTestSide; y++ {
		h.load(grpTestColorAddress+uint32(y*grpTestSide*2), make([]byte, grpTestSide*2))
	}
	draw.put32(0, 0, 3)
	h.submit(0x00020000, draw.bytes())
	if h.pixel(2, 2) != 0 {
		t.Fatalf("draw with an out-of-range index wrote %#04x", h.pixel(2, 2))
	}
}

func TestQualcommGraphicsDSPStencilClearMarkersAndLineDraws(t *testing.T) {
	h := newGraphicsDSPHarness(t)
	h.startGraphics()
	// The stencil is cleared to record 13's bit; a draw that requires
	// stencil == 1 then passes or fails as a whole.
	for id, bit := range []uint16{1, 0} {
		scene, enables := grpTestScene(uint32(id*2 + 1))
		scene.put16(enables, 22, 0)      // no depth test
		scene.put16(enables, 28, 0xffff) // stencil test
		stencil := scene.record(grpOpStencil)
		scene.put16(stencil, 2, 4) // EQUAL
		scene.put16(stencil, 4, 1)
		scene.put16(stencil, 6, 1)
		scene.put16(stencil, 8, 1)
		scene.put16(stencil, 10, 1)
		scene.put16(stencil, 12, 1)
		scene.put16(stencil, 14, 1)
		// grpTestScene ends with the clear-values record.
		scene.put16(len(scene.data)-int(qualcommGraphicsRecordSize[grpOpStencil])-int(qualcommGraphicsRecordSize[grpOpClearValues]), 4, bit)
		scene.marker(grpMarkerClearStencil)
		h.submit(0x00020000, scene.bytes())
		for y := 0; y < grpTestSide; y++ {
			h.load(grpTestColorAddress+uint32(y*grpTestSide*2), make([]byte, grpTestSide*2))
		}
		quad := newGRPArray(uint32(id*2 + 2))
		quad.draw(h, 7, [][3]float64{{-1, -1, 0}, {1, -1, 0}, {-1, 1, 0}, {1, 1, 0}})
		h.submit(0x00020000, quad.bytes())
		want := uint16(0)
		if bit == 1 {
			want = 0xffff
		}
		if got := h.pixel(5, 5); got != want {
			t.Fatalf("stencil cleared to %d: pixel %#04x, want %#04x", bit, got, want)
		}
	}

	// A line primitive (code 4) through the middle row reaches guest memory.
	scene, enables := grpTestScene(9)
	scene.put16(enables, 22, 0)
	scene.draw(h, 4, [][3]float64{{-1, 0.0625, 0}, {1, 0.0625, 0}})
	for y := 0; y < grpTestSide; y++ {
		h.load(grpTestColorAddress+uint32(y*grpTestSide*2), make([]byte, grpTestSide*2))
	}
	h.submit(0x00020000, scene.bytes())
	if h.pixel(0, 7) != 0xffff || h.pixel(15, 7) != 0xffff || h.pixel(8, 6) != 0 || h.pixel(8, 8) != 0 {
		t.Fatalf("line row = %#04x %#04x, neighbours %#04x %#04x",
			h.pixel(0, 7), h.pixel(15, 7), h.pixel(8, 6), h.pixel(8, 8))
	}
}
