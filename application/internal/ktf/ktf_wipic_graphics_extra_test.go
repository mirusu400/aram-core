package ktf

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"image"
	"image/color"
	"image/gif"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestKTFWIPICPublicGraphicsSlotsAreMapped(t *testing.T) {
	expected := map[int]ktfHostHandler{
		14: ktfWIPICGraphicsCopyArea,
		15: ktfWIPICGraphicsDrawArc,
		16: ktfWIPICGraphicsFillArc,
		19: ktfWIPICGraphicsGetRGBPixels,
		20: ktfWIPICGraphicsSetRGBPixels,
		34: ktfWIPICGraphicsDecodeNextImage,
		36: ktfWIPICGraphicsPostEvent,
		37: ktfWIPICGraphicsDrawPolygon,
		38: ktfWIPICGraphicsDrawFillPolygon,
	}
	for slot, want := range expected {
		got := ktfWIPICHandler(ktfWIPICMasterGraphics, slot)
		if reflect.ValueOf(got).Pointer() != reflect.ValueOf(want).Pointer() {
			t.Fatalf("graphics slot %d is not mapped to its public handler", slot)
		}
	}
}

func TestKTFWIPICCopyAreaHandlesOverlapOffsetAndClip(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	framebufferHandle, err := runtime.createWIPICFramebuffer(4, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	framebuffer := runtime.wipicFramebuffers[framebufferHandle]
	pixels := make([]byte, framebuffer.stride*framebuffer.height)
	for y := 0; y < framebuffer.height; y++ {
		for x := 0; x < framebuffer.width; x++ {
			binary.LittleEndian.PutUint16(
				pixels[y*framebuffer.stride+x*2:],
				uint16(y*16+x+1),
			)
		}
	}
	if err := runtime.CPU.WriteMemory(framebuffer.pixels, pixels); err != nil {
		t.Fatal(err)
	}
	graphicsContext, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	contextWords := make([]uint32, 15)
	copy(contextWords, []uint32{1, 1, 3, 4, 1})
	contextWords[14] = 1
	if err := runtime.writeWords(graphicsContext, contextWords); err != nil {
		t.Fatal(err)
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{
		framebufferHandle, 0, 0, 4, 3, 0, 0, graphicsContext,
	})
	if _, err := ktfWIPICGraphicsCopyArea(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		x, y int
		want uint16
	}{
		{x: 1, y: 1, want: 2},
		{x: 2, y: 1, want: 3},
		{x: 1, y: 2, want: 18},
		{x: 2, y: 2, want: 19},
		{x: 1, y: 3, want: 34},
		{x: 2, y: 3, want: 35},
		{x: 0, y: 1, want: 17},
		{x: 3, y: 3, want: 52},
	} {
		if got := readKTFWIPICPixel(t, runtime, framebufferHandle, check.x, check.y); got != check.want {
			t.Fatalf("copied pixel (%d,%d) = %04x, want %04x", check.x, check.y, got, check.want)
		}
	}
}

func TestKTFWIPICArcPolygonAndExtremeLineRasterize(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	framebufferHandle, err := runtime.createWIPICFramebuffer(12, 12, false)
	if err != nil {
		t.Fatal(err)
	}
	graphicsContext, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	contextWords := make([]uint32, 15)
	contextWords[5] = 0xf800
	if err := runtime.writeWords(graphicsContext, contextWords); err != nil {
		t.Fatal(err)
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{
		framebufferHandle, 1, 1, 8, 8, 0, 360, graphicsContext,
	})
	if _, err := ktfWIPICGraphicsFillArc(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if got := readKTFWIPICPixel(t, runtime, framebufferHandle, 5, 5); got != 0xf800 {
		t.Fatalf("filled arc center = %04x", got)
	}
	if got := readKTFWIPICPixel(t, runtime, framebufferHandle, 1, 1); got != 0 {
		t.Fatalf("filled arc corner = %04x", got)
	}

	framebuffer := runtime.wipicFramebuffers[framebufferHandle]
	if err := runtime.CPU.WriteMemory(
		framebuffer.pixels,
		make([]byte, framebuffer.stride*framebuffer.height),
	); err != nil {
		t.Fatal(err)
	}
	xCoordinates, err := runtime.Heap.Allocate(12, true)
	if err != nil {
		t.Fatal(err)
	}
	yCoordinates, err := runtime.Heap.Allocate(12, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(xCoordinates, []uint32{1, 9, 1}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(yCoordinates, []uint32{1, 1, 9}); err != nil {
		t.Fatal(err)
	}
	contextWords[13], contextWords[14] = 1, 1
	if err := runtime.writeWords(graphicsContext, contextWords); err != nil {
		t.Fatal(err)
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{
		framebufferHandle, xCoordinates, yCoordinates, 3, graphicsContext,
	})
	if _, err := ktfWIPICGraphicsDrawFillPolygon(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if got := readKTFWIPICPixel(t, runtime, framebufferHandle, 4, 4); got != 0xf800 {
		t.Fatalf("filled polygon interior = %04x", got)
	}
	if got := readKTFWIPICPixel(t, runtime, framebufferHandle, 11, 11); got != 0 {
		t.Fatalf("pixel outside polygon = %04x", got)
	}

	state, err := runtime.wipicGraphicsContext(graphicsContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.drawWIPICLine(
		framebufferHandle,
		-1<<30,
		10,
		1<<30,
		10,
		state,
	); err != nil {
		t.Fatal(err)
	}
	if got := readKTFWIPICPixel(t, runtime, framebufferHandle, 0, 10); got != 0xf800 {
		t.Fatalf("clipped extreme line start = %04x", got)
	}
	if got := readKTFWIPICPixel(t, runtime, framebufferHandle, 11, 10); got != 0xf800 {
		t.Fatalf("clipped extreme line end = %04x", got)
	}
}

func TestKTFWIPICRGBPixelTransfersHonorPitchAndContext(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	framebufferHandle, err := runtime.createWIPICFramebuffer(4, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	graphicsContext, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	contextWords := make([]uint32, 15)
	copy(contextWords, []uint32{0, 0, 3, 3, 1})
	contextWords[14] = 1
	if err := runtime.writeWords(graphicsContext, contextWords); err != nil {
		t.Fatal(err)
	}
	source, err := runtime.Heap.Allocate(6*4, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(source, []uint32{
		0xffff0000, 0xff00ff00, 0xdeadbeef,
		0xff0000ff, 0xffffffff, 0xdeadbeef,
	}); err != nil {
		t.Fatal(err)
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{
		framebufferHandle, 1, 0, 2, 2, source, 3, graphicsContext,
	})
	if _, err := ktfWIPICGraphicsSetRGBPixels(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		x, y int
		want uint16
	}{
		{x: 1, y: 1, want: 0xf800},
		{x: 2, y: 1, want: 0x07e0},
		{x: 1, y: 2, want: 0x001f},
		{x: 2, y: 2, want: 0xffff},
		{x: 1, y: 0, want: 0},
	} {
		if got := readKTFWIPICPixel(t, runtime, framebufferHandle, check.x, check.y); got != check.want {
			t.Fatalf("set RGB pixel (%d,%d) = %04x, want %04x", check.x, check.y, got, check.want)
		}
	}
	output, err := runtime.Heap.Allocate(6*4, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(output, []uint32{
		0, 0, 0xdeadbeef, 0, 0, 0xdeadbeef,
	}); err != nil {
		t.Fatal(err)
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{
		framebufferHandle, 1, 1, 2, 2, output, 3,
	})
	if _, err := ktfWIPICGraphicsGetRGBPixels(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.ReadWords(output, 6)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{0x00ff0000, 0x0000ff00, 0xdeadbeef, 0x000000ff, 0x00ffffff, 0xdeadbeef}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RGB output = %08x, want %08x", got, want)
	}
}

func TestKTFWIPICDecodeNextImageAdvancesAnimatedGIF(t *testing.T) {
	palette := color.Palette{
		color.RGBA{},
		color.RGBA{R: 0xff, A: 0xff},
		color.RGBA{G: 0xff, A: 0xff},
	}
	first := image.NewPaletted(image.Rect(0, 0, 2, 1), palette)
	second := image.NewPaletted(image.Rect(0, 0, 2, 1), palette)
	first.Pix = []byte{1, 1}
	second.Pix = []byte{2, 2}
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, &gif.GIF{
		Image: []*image.Paletted{first, second},
		Delay: []int{1, 1},
		Config: image.Config{
			ColorModel: palette,
			Width:      2,
			Height:     1,
		},
	}); err != nil {
		t.Fatal(err)
	}
	runtime := newScratchKTFRuntime(t)
	memoryID, err := runtime.allocateWIPICMemory(uint32(encoded.Len()), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(
		runtime.wipicMemory[memoryID].data,
		encoded.Bytes(),
	); err != nil {
		t.Fatal(err)
	}
	output, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{
		output, memoryID, 0, uint32(encoded.Len()),
	})
	if result, err := ktfWIPICGraphicsCreateImage(context.Background(), runtime); err != nil || result != 1 {
		t.Fatalf("create animated image result=%08x err=%v", result, err)
	}
	object, err := runtime.ReadU32(output)
	if err != nil {
		t.Fatal(err)
	}
	framebufferHandle := runtime.wipicImages[object].framebuffer
	if got := readKTFWIPICPixel(t, runtime, framebufferHandle, 0, 0); got != 0xf800 {
		t.Fatalf("initial GIF frame pixel = %04x", got)
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{object})
	result, err := ktfWIPICGraphicsDecodeNextImage(context.Background(), runtime)
	if err != nil || int32(result) != guest.WIPIImageDone {
		t.Fatalf("decode next result=%d err=%v", int32(result), err)
	}
	if runtime.wipicImages[object].frameIndex != 1 {
		t.Fatalf("animated image frame index = %d", runtime.wipicImages[object].frameIndex)
	}
	if got := readKTFWIPICPixel(t, runtime, framebufferHandle, 0, 0); got != 0x07e0 {
		t.Fatalf("second GIF frame pixel = %04x", got)
	}
}

func TestKTFWIPICPostEventUsesDeterministicServiceQueue(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	values := []uint32{7, 0xfffffffe, 11, 13}
	setKTFWIPICCallArguments(t, runtime, values)
	result, err := ktfWIPICGraphicsPostEvent(context.Background(), runtime)
	if err != nil || result != 0 {
		t.Fatalf("post event result=%08x err=%v", result, err)
	}
	events := runtime.Services.Events.Snapshot().Events
	var event shared.Event
	found := false
	for _, candidate := range events {
		if candidate.Name == "wipic.graphics" {
			event = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatal("posted WIPI-C event is missing")
	}
	if event.Kind != shared.EventApplication ||
		event.Owner != runtime.ServiceOwner ||
		event.Name != "wipic.graphics" ||
		event.Value != 7 ||
		len(event.Data) != 16 {
		t.Fatalf("posted WIPI-C event = %+v", event)
	}
	for index, want := range values {
		if got := binary.LittleEndian.Uint32(event.Data[index*4:]); got != want {
			t.Fatalf("posted event word %d = %08x, want %08x", index, got, want)
		}
	}
}

func setKTFWIPICCallArguments(t *testing.T, runtime *Runtime, values []uint32) {
	t.Helper()
	for index := 0; index < 4; index++ {
		var value uint32
		if index < len(values) {
			value = values[index]
		}
		if err := runtime.CPU.WriteRegister(cpu.RegisterR0+uint32(index), value); err != nil {
			t.Fatal(err)
		}
	}
	if len(values) <= 4 {
		return
	}
	stack, err := runtime.Heap.Allocate(uint32((len(values)-4)*4), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, values[4:]); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
}

func readKTFWIPICPixel(
	t *testing.T,
	runtime *Runtime,
	framebufferHandle uint32,
	x, y int,
) uint16 {
	t.Helper()
	framebuffer := runtime.wipicFramebuffers[framebufferHandle]
	if framebuffer == nil {
		t.Fatalf("framebuffer 0x%08x is unavailable", framebufferHandle)
	}
	var encoded [2]byte
	if err := runtime.CPU.ReadMemory(
		framebuffer.pixels+uint32(y*framebuffer.stride+x*2),
		encoded[:],
	); err != nil {
		t.Fatal(err)
	}
	return binary.LittleEndian.Uint16(encoded[:])
}

// TestKTFWIPICSpriteFramebuffersDoNotConsumeSharedSurfaces pins issue #68.
// 메이플스토리 도적편 keeps hundreds of MC_grpImage sprites alive, and mirroring
// each one into the graphics service exhausted the surface count mid-play even
// though nothing ever read those mirrors.
func TestKTFWIPICSpriteFramebuffersDoNotConsumeSharedSurfaces(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	limit := int(shared.DefaultGraphicsLimits().MaxSurfaces)
	for index := 0; index < limit+16; index++ {
		handle, err := runtime.createWIPICFramebuffer(8, 8, false)
		if err != nil {
			t.Fatalf("sprite framebuffer %d: %v", index, err)
		}
		if surface := runtime.wipicSurfaceServices[handle]; surface != 0 {
			t.Fatalf(
				"sprite framebuffer %d took shared surface %d before any use",
				index,
				surface,
			)
		}
	}
}

// TestKTFWIPICFramebufferSurfaceMaterializesOnSync covers the other half of the
// lazy mirror: a framebuffer that does leave the guest still gets its shared
// surface, carrying the guest pixels with it.
func TestKTFWIPICFramebufferSurfaceMaterializesOnSync(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	handle, err := runtime.createWIPICFramebuffer(2, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	framebuffer := runtime.wipicFramebuffers[handle]
	pixels := make([]byte, framebuffer.stride*framebuffer.height)
	for index := 0; index < len(pixels); index += 2 {
		binary.LittleEndian.PutUint16(pixels[index:], 0xf800)
	}
	if err := runtime.CPU.WriteMemory(framebuffer.pixels, pixels); err != nil {
		t.Fatal(err)
	}
	if err := runtime.syncKTFWIPICFramebuffer(handle); err != nil {
		t.Fatal(err)
	}
	surface := runtime.wipicSurfaceServices[handle]
	if surface == 0 {
		t.Fatal("syncing a framebuffer did not materialize its shared surface")
	}
	if err := runtime.syncKTFWIPICFramebuffer(handle); err != nil {
		t.Fatal(err)
	}
	if again := runtime.wipicSurfaceServices[handle]; again != surface {
		t.Fatalf("second sync replaced surface %d with %d", surface, again)
	}
	rgba, err := runtime.Services.Graphics.RGBA(runtime.ServiceOwner, surface)
	if err != nil {
		t.Fatal(err)
	}
	if len(rgba) != 2*2*4 {
		t.Fatalf("surface RGBA payload has %d bytes, want %d", len(rgba), 16)
	}
	for index := 0; index < len(rgba); index += 4 {
		if rgba[index] != 0xff || rgba[index+1] != 0 || rgba[index+2] != 0 {
			t.Fatalf(
				"pixel %d is %02x%02x%02x, want the guest red",
				index/4,
				rgba[index],
				rgba[index+1],
				rgba[index+2],
			)
		}
	}
}
