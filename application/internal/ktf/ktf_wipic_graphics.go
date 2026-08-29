package ktf

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

func ktfWIPICNoop(table, slot int) ktfHostHandler {
	return func(_ context.Context, runtime *Runtime) (uint32, error) {
		parameters := make([]uint32, 6)
		for index := range parameters {
			parameters[index], _ = runtime.parameter(uint32(index))
		}
		link, _ := runtime.CPU.ReadRegister(cpu.RegisterLR)
		runtime.tracef(
			"wipic_call:%d.%d:args=%08x:lr=0x%08x",
			table,
			slot,
			parameters,
			link,
		)
		return 0, nil
	}
}

func ktfWIPICGraphicsGetImageProperty(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	object, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	index, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	imageState := runtime.wipicImages[object]
	if imageState == nil {
		return 0, nil
	}
	framebuffer := runtime.wipicFramebuffers[imageState.framebuffer]
	if framebuffer == nil {
		return 0, nil
	}
	switch index {
	case 1:
		return 0, nil
	case 2:
		return 0, nil
	case 3:
		return 1, nil
	case 4:
		return uint32(framebuffer.width), nil
	case 5:
		return uint32(framebuffer.height), nil
	case 6:
		return uint32(framebuffer.bits), nil
	default:
		return 0, nil
	}
}

func ktfWIPICGraphicsGetImageFramebuffer(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	object, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	imageState := runtime.wipicImages[object]
	if imageState == nil {
		return 0, nil
	}
	return imageState.framebuffer, nil
}

func ktfWIPICGraphicsCreateImage(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	output, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	memoryID, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	offset, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	length, err := runtime.parameter(3)
	if err != nil {
		return 0, err
	}
	if output == 0 {
		return ktfWIPICErrorInvalid, nil
	}
	if err := runtime.WriteU32(output, 0); err != nil {
		return 0, err
	}
	allocation, ok := runtime.wipicMemory[memoryID]
	if !ok || length == 0 ||
		uint64(offset)+uint64(length) > uint64(allocation.size) ||
		length > 16<<20 {
		return ^uint32(15), nil
	}
	encoded := make([]byte, length)
	if err := runtime.CPU.ReadMemory(allocation.data+offset, encoded); err != nil {
		return 0, err
	}
	assetID, err := runtime.Services.Assets.Decode(
		runtime.ServiceOwner,
		encoded,
		shared.DecodeOptions{},
	)
	if err != nil {
		return ^uint32(15), nil
	}
	asset, err := runtime.Services.Assets.Info(runtime.ServiceOwner, assetID)
	if err != nil {
		_ = runtime.Services.Assets.Release(runtime.ServiceOwner, assetID)
		return 0, err
	}
	width, height := int(asset.Width), int(asset.Height)
	if width <= 0 || height <= 0 || width > 4096 || height > 4096 {
		_ = runtime.Services.Assets.Release(runtime.ServiceOwner, assetID)
		return ^uint32(15), nil
	}
	framebufferObject, err := runtime.createWIPICFramebuffer(
		width,
		height,
		false,
	)
	if err != nil {
		_ = runtime.Services.Assets.Release(runtime.ServiceOwner, assetID)
		return 0, err
	}
	alpha, err := runtime.paintWIPICImageFrame(
		framebufferObject,
		asset.Frames[0].Surface,
	)
	if err != nil {
		_ = runtime.Services.Assets.Release(runtime.ServiceOwner, assetID)
		return 0, err
	}
	// KTF's provider-private MC_GrpImage is a pointer wrapper around an image
	// body. Native Clets dereference the wrapper, then read the framebuffer at
	// body+0x08 and the optional mask framebuffer at body+0x0c.
	body, err := runtime.AllocateWords(4)
	if err != nil {
		return 0, err
	}
	if err := runtime.writeWords(body, []uint32{
		0,
		0,
		framebufferObject,
		0,
	}); err != nil {
		return 0, err
	}
	object, err := runtime.AllocateWords(1)
	if err != nil {
		return 0, err
	}
	if err := runtime.WriteU32(object, body); err != nil {
		return 0, err
	}
	runtime.wipicImages[object] = &ktfWIPICImage{
		object:      object,
		body:        body,
		framebuffer: framebufferObject,
		source:      memoryID,
		alpha:       alpha,
	}
	runtime.wipicAssetServices[object] = assetID
	if err := runtime.WriteU32(output, object); err != nil {
		return 0, err
	}
	runtime.tracef(
		"wipic_graphics_image:object=0x%08x:framebuffer=0x%08x:%dx%d",
		object,
		framebufferObject,
		width,
		height,
	)
	return 1, nil
}

func ktfWIPICGraphicsDestroyImage(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	object, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	imageState := runtime.wipicImages[object]
	if imageState == nil {
		return 0, nil
	}
	if framebuffer := runtime.wipicFramebuffers[imageState.framebuffer]; framebuffer != nil {
		if serviceID := runtime.wipicSurfaceServices[framebuffer.object]; serviceID != 0 {
			_ = runtime.Services.Graphics.DestroySurface(
				runtime.ServiceOwner,
				serviceID,
			)
			delete(runtime.wipicSurfaceServices, framebuffer.object)
		}
		runtime.Heap.Release(framebuffer.pixelHeader)
		runtime.Heap.Release(framebuffer.pixelObject)
		runtime.Heap.Release(framebuffer.body)
		runtime.Heap.Release(framebuffer.object)
		delete(runtime.wipicFramebuffers, framebuffer.object)
	}
	if assetID := runtime.wipicAssetServices[object]; assetID != 0 {
		_ = runtime.Services.Assets.Release(runtime.ServiceOwner, assetID)
		delete(runtime.wipicAssetServices, object)
	}
	if allocation, ok := runtime.wipicMemory[imageState.source]; ok {
		runtime.Heap.Release(allocation.base)
		runtime.Heap.Release(imageState.source)
		delete(runtime.wipicMemory, imageState.source)
	}
	runtime.Heap.Release(imageState.body)
	runtime.Heap.Release(imageState.object)
	delete(runtime.wipicImages, object)
	return 0, nil
}

// ktfWIPICGraphicsEncodeImage returns a KTF indirect-memory ID containing a
// BMP representation of the requested framebuffer rectangle. Although public
// WIPI references describe a small status return, KTF Clet support code treats
// this provider slot as an allocating call: it dereferences the returned ID and
// consumes the payload at the resulting buffer head+8.
func ktfWIPICGraphicsEncodeImage(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	values := make([]uint32, 6)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, err
		}
		values[index] = value
	}
	lengthAddress := values[5]
	if lengthAddress != 0 {
		if err := runtime.WriteU32(lengthAddress, 0); err != nil {
			return 0, err
		}
	}
	framebuffer := runtime.wipicFramebuffers[values[0]]
	x, y := int64(int32(values[1])), int64(int32(values[2]))
	width, height := int64(int32(values[3])), int64(int32(values[4]))
	if framebuffer == nil || x < 0 || y < 0 || width <= 0 || height <= 0 ||
		x+width > int64(framebuffer.width) ||
		y+height > int64(framebuffer.height) {
		return 0, nil
	}
	if err := runtime.syncKTFWIPICFramebuffer(values[0]); err != nil {
		return 0, err
	}
	surface := runtime.wipicSurfaceServices[values[0]]
	if surface == 0 {
		return 0, nil
	}
	encoded, err := runtime.Services.Assets.EncodeSurface(
		runtime.ServiceOwner,
		surface,
		"image/bmp",
		shared.Rectangle{
			X:      int32(x),
			Y:      int32(y),
			Width:  int32(width),
			Height: int32(height),
		},
	)
	if err != nil || len(encoded) == 0 || uint64(len(encoded)) > 32<<20 {
		return 0, nil
	}
	memoryID, err := runtime.allocateWIPICMemory(uint32(len(encoded)), false)
	if err != nil || memoryID == 0 {
		return 0, err
	}
	allocation := runtime.wipicMemory[memoryID]
	release := func() {
		runtime.Heap.Release(allocation.base)
		runtime.Heap.Release(memoryID)
		delete(runtime.wipicMemory, memoryID)
	}
	if err := runtime.CPU.WriteMemory(allocation.data, encoded); err != nil {
		release()
		return 0, err
	}
	if lengthAddress != 0 {
		if err := runtime.WriteU32(lengthAddress, uint32(len(encoded))); err != nil {
			release()
			return 0, err
		}
	}
	runtime.tracef(
		"wipic_graphics_encode:framebuffer=0x%08x:memory=0x%08x:"+
			"rect=%d,%d,%d,%d:size=%d",
		values[0],
		memoryID,
		x,
		y,
		width,
		height,
		len(encoded),
	)
	return memoryID, nil
}

func ktfWIPICGraphicsGetScreenFramebuffer(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	display, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	if display > 1 {
		return 0, nil
	}
	return runtime.EnsureWIPICScreenFramebuffer()
}

func (r *Runtime) EnsureWIPICScreenFramebuffer() (uint32, error) {
	if r.WipicScreenFramebuffer != 0 {
		return r.WipicScreenFramebuffer, nil
	}
	if r.frame == nil {
		r.frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	}
	width, height := r.frame.Bounds().Dx(), r.frame.Bounds().Dy()
	object, err := r.createWIPICFramebuffer(width, height, true)
	if err != nil {
		return 0, err
	}
	r.WipicScreenFramebuffer = object
	return object, nil
}

func ktfWIPICGraphicsCreateOffscreenFramebuffer(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	width, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	height, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if width == 0 || height == 0 || width > 4096 || height > 4096 {
		return 0, nil
	}
	return runtime.createWIPICFramebuffer(int(width), int(height), false)
}

func ktfWIPICGraphicsDestroyOffscreenFramebuffer(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	object, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	framebuffer := runtime.wipicFramebuffers[object]
	if framebuffer == nil || framebuffer.screen {
		return 0, nil
	}
	if serviceID := runtime.wipicSurfaceServices[object]; serviceID != 0 {
		if err := runtime.Services.Graphics.DestroySurface(
			runtime.ServiceOwner,
			serviceID,
		); err != nil {
			return 0, err
		}
		delete(runtime.wipicSurfaceServices, object)
	}
	runtime.Heap.Release(framebuffer.pixelHeader)
	runtime.Heap.Release(framebuffer.pixelObject)
	runtime.Heap.Release(framebuffer.body)
	runtime.Heap.Release(framebuffer.object)
	delete(runtime.wipicFramebuffers, object)
	return 0, nil
}

func (r *Runtime) createWIPICFramebuffer(
	width int,
	height int,
	screen bool,
) (uint32, error) {
	const bits = 16
	stride := width * (bits / 8)
	pixelBytes := uint64(stride) * uint64(height)
	if pixelBytes+8 > uint64(^uint32(0)) {
		return 0, errors.New("KTF WIPI-C framebuffer size overflows")
	}
	pixelHeader, err := r.Heap.Allocate(uint32(pixelBytes)+8, true)
	if err != nil || pixelHeader == 0 {
		return 0, err
	}
	pixelObject, err := r.AllocateWords(1)
	if err != nil {
		return 0, err
	}
	if err := r.WriteU32(pixelObject, pixelHeader); err != nil {
		return 0, err
	}
	body, err := r.AllocateWords(8)
	if err != nil {
		return 0, err
	}
	// The first nested surface is sufficient for the metrics read directly by
	// Clets. Point it back to this body so +0x08..+0x18 have one canonical
	// representation. body+0x18 is the handset's nested pixel-array object.
	if err := r.writeWords(body, []uint32{
		body,
		pixelObject,
		uint32(width),
		uint32(height),
		uint32(stride),
		bits,
		pixelObject,
		1,
	}); err != nil {
		return 0, err
	}
	object, err := r.AllocateWords(5)
	if err != nil {
		return 0, err
	}
	if err := r.WriteU32(object, body); err != nil {
		return 0, err
	}
	framebuffer := &ktfWIPICFramebuffer{
		object:      object,
		body:        body,
		pixelObject: pixelObject,
		pixelHeader: pixelHeader,
		pixels:      pixelHeader + 8,
		width:       width,
		height:      height,
		stride:      stride,
		bits:        bits,
		screen:      screen,
	}
	r.wipicFramebuffers[object] = framebuffer
	if screen {
		surface, err := r.createWIPICSurface(framebuffer)
		if err != nil {
			delete(r.wipicFramebuffers, object)
			return 0, err
		}
		if err := r.Services.Graphics.SetScreen(r.ServiceOwner, surface); err != nil {
			_ = r.Services.Graphics.DestroySurface(r.ServiceOwner, surface)
			delete(r.wipicSurfaceServices, object)
			delete(r.wipicFramebuffers, object)
			return 0, err
		}
	}
	r.tracef(
		"wipic_graphics_framebuffer:object=0x%08x:body=0x%08x:pixels=0x%08x:%dx%dx%d:screen=%t",
		object,
		body,
		framebuffer.pixels,
		width,
		height,
		bits,
		screen,
	)
	return object, nil
}

// ensureWIPICSurface returns the shared-service mirror of a guest RGB565
// framebuffer, creating it on first use.
//
// WIPI-C drawing, sprite blits included, runs entirely on the guest pixel
// memory the framebuffer body points at; the shared surface is only read where
// the framebuffer leaves the guest, that is when it is presented, merged into
// the Java frame, or encoded. Mirroring every framebuffer eagerly therefore
// spent one service surface per MC_grpImage, and a title that legitimately
// keeps hundreds of sprites alive (메이플스토리 도적편, issue #68) exhausted the
// surface-count limit mid-play on mirrors nothing had ever read.
func (r *Runtime) ensureWIPICSurface(handle uint32) (shared.ServiceID, error) {
	if surface := r.wipicSurfaceServices[handle]; surface != 0 {
		return surface, nil
	}
	framebuffer := r.wipicFramebuffers[handle]
	if framebuffer == nil {
		return 0, nil
	}
	return r.createWIPICSurface(framebuffer)
}

func (r *Runtime) createWIPICSurface(
	framebuffer *ktfWIPICFramebuffer,
) (shared.ServiceID, error) {
	surface, err := r.Services.Graphics.CreateSurface(
		r.ServiceOwner,
		shared.SurfaceDescriptor{
			Width:  int32(framebuffer.width),
			Height: int32(framebuffer.height),
			Stride: int32(framebuffer.stride),
			Format: shared.PixelRGB565,
		},
	)
	if err != nil {
		return 0, err
	}
	r.wipicSurfaceServices[framebuffer.object] = surface
	return surface, nil
}

func ktfWIPICGraphicsInitContext(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil || address == 0 {
		return 0, err
	}
	values := [...]uint32{
		0,                            // clip_enabled
		0, 0, 0x7fffffff, 0x7fffffff, // clip
		0xffff, 0, 0, // fg_pixel, bg_pixel, trans_pixel
		255,  // alpha
		0, 0, // offset
		0, 0, // pixel_op, pixel_param1
		0, 0, // font, style
	}
	return 0, runtime.writeWords(address, values[:])
}

func ktfWIPICGraphicsSetContext(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	index, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	value, err := runtime.parameter(2)
	if err != nil || address == 0 {
		return 0, err
	}
	offsets := ktfWIPICContextScalarOffsets
	if index == 0 {
		if value == 0 {
			return 0, nil
		}
		clip := make([]byte, 16)
		if err := runtime.CPU.ReadMemory(value, clip); err != nil {
			return 0, err
		}
		if err := runtime.CPU.WriteMemory(
			address+ktfWIPICContextClip,
			clip,
		); err != nil {
			return 0, err
		}
		return 0, runtime.WriteU32(address+ktfWIPICContextClipEnabled, 1)
	}
	if index == 10 {
		if value == 0 {
			return 0, nil
		}
		offset := make([]byte, 8)
		if err := runtime.CPU.ReadMemory(value, offset); err != nil {
			return 0, err
		}
		return 0, runtime.CPU.WriteMemory(
			address+ktfWIPICContextOffset,
			offset,
		)
	}
	if offset, ok := offsets[index]; ok {
		return 0, runtime.WriteU32(address+offset, value)
	}
	return 0, nil
}

func ktfWIPICGraphicsGetContext(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	address, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	index, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(2)
	if err != nil || address == 0 || output == 0 {
		return 0, err
	}
	offset, size := uint32(0), uint32(0)
	switch {
	case index == 0:
		offset, size = ktfWIPICContextClip, 16
	case index == 10:
		offset, size = ktfWIPICContextOffset, 8
	default:
		scalar, ok := ktfWIPICContextScalarOffsets[index]
		if !ok {
			return 0, nil
		}
		offset, size = scalar, 4
	}
	data := make([]byte, size)
	if err := runtime.CPU.ReadMemory(address+offset, data); err != nil {
		return 0, err
	}
	return 0, runtime.CPU.WriteMemory(output, data)
}

// The KTF MC_GrpContext members, proven from 컴투스포춘골프3D's own
// MC_grpSetContext front end: it services the members it knows by writing the
// struct directly - transparent pixel to +0x1c, alpha to +0x20 (mirrored into
// the pixel-op parameter at +0x30), the drawing offset pair to +0x24, and the
// pixel-op procedure to +0x2c - and forwards every other index to the real
// entry point. The clip sits behind a leading enable word rather than in front
// of a trailing one, which is why the same title's clip read as an empty
// rectangle before (issue #86) and discarded every host draw it made.
const (
	ktfWIPICContextClipEnabled = 0
	ktfWIPICContextClip        = 4
	ktfWIPICContextForeground  = 20
	ktfWIPICContextBackground  = 24
	ktfWIPICContextTransparent = 28
	ktfWIPICContextAlpha       = 32
	ktfWIPICContextOffset      = 36
	ktfWIPICContextPixelOp     = 44
	ktfWIPICContextPixelParam  = 48
	ktfWIPICContextFont        = 52
	ktfWIPICContextStyle       = 56
	ktfWIPICContextSize        = 60
)

// ktfWIPICContextScalarOffsets maps the single-word MC_grpSetContext indices
// onto the members above. Index 0 (clip) and index 10 (offset) carry a
// pointer to a small array instead and are handled separately.
var ktfWIPICContextScalarOffsets = map[uint32]uint32{
	1: ktfWIPICContextForeground,
	2: ktfWIPICContextBackground,
	3: ktfWIPICContextTransparent,
	4: ktfWIPICContextAlpha,
	5: ktfWIPICContextPixelOp,
	6: ktfWIPICContextPixelParam,
	7: ktfWIPICContextFont,
	8: ktfWIPICContextStyle,
}

type ktfWIPICGraphicsContext struct {
	left, top, right, bottom int
	clipEnabled              bool
	foreground               uint16
	font                     uint32
	offsetX, offsetY         int
	pixelOp                  uint32
	pixelParam               uint32
}

func (r *Runtime) wipicGraphicsContext(
	address uint32,
) (ktfWIPICGraphicsContext, error) {
	state := ktfWIPICGraphicsContext{
		right:  int(^uint32(0) >> 1),
		bottom: int(^uint32(0) >> 1),
	}
	if address == 0 {
		return state, nil
	}
	var encoded [ktfWIPICContextSize]byte
	if err := r.CPU.ReadMemory(address, encoded[:]); err != nil {
		return state, fmt.Errorf(
			"read KTF WIPI-C graphics context at 0x%08x: %w",
			address,
			err,
		)
	}
	word := func(offset int) uint32 {
		return binary.LittleEndian.Uint32(encoded[offset : offset+4])
	}
	state.left = int(int32(word(ktfWIPICContextClip)))
	state.top = int(int32(word(ktfWIPICContextClip + 4)))
	state.right = int(int32(word(ktfWIPICContextClip + 8)))
	state.bottom = int(int32(word(ktfWIPICContextClip + 12)))
	// MC_grpSetContext always installs a non-empty clip, so an enabled clip
	// with no area is not one the title asked for: it is whatever bytes its
	// own MC_GrpContext happened to hold. Honouring it would silently discard
	// every draw made through that context.
	state.clipEnabled = word(ktfWIPICContextClipEnabled) != 0 &&
		state.left < state.right && state.top < state.bottom
	state.foreground = uint16(word(ktfWIPICContextForeground))
	state.font = word(ktfWIPICContextFont)
	state.offsetX = int(int32(word(ktfWIPICContextOffset)))
	state.offsetY = int(int32(word(ktfWIPICContextOffset + 4)))
	state.pixelOp = word(ktfWIPICContextPixelOp)
	state.pixelParam = word(ktfWIPICContextPixelParam)
	return state, nil
}

func ktfWIPICGraphicsPutPixel(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	framebuffer, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	x, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	y, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	contextAddress, err := runtime.parameter(3)
	if err != nil {
		return 0, err
	}
	state, err := runtime.wipicGraphicsContext(contextAddress)
	if err != nil {
		return 0, err
	}
	if err := runtime.writeWIPICPixel(
		framebuffer,
		int(int32(x))+state.offsetX,
		int(int32(y))+state.offsetY,
		state,
	); err != nil {
		return 0, err
	}
	return 0, runtime.commitKTFWIPICFramebuffer(framebuffer)
}

func ktfWIPICGraphicsDrawLine(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	values := make([]uint32, 6)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, fmt.Errorf(
				"read KTF WIPI-C line parameter %d: %w",
				index,
				err,
			)
		}
		values[index] = value
	}
	state, err := runtime.wipicGraphicsContext(values[5])
	if err != nil {
		return 0, err
	}
	x1 := int(int32(values[1])) + state.offsetX
	y1 := int(int32(values[2])) + state.offsetY
	x2 := int(int32(values[3])) + state.offsetX
	y2 := int(int32(values[4])) + state.offsetY
	if err := runtime.drawWIPICLine(values[0], x1, y1, x2, y2, state); err != nil {
		return 0, err
	}
	return 0, runtime.commitKTFWIPICFramebuffer(values[0])
}

func (r *Runtime) drawWIPICLine(
	handle uint32,
	x1, y1, x2, y2 int,
	state ktfWIPICGraphicsContext,
) error {
	var visible bool
	x1, y1, x2, y2, visible = r.clipWIPICLine(
		handle,
		x1,
		y1,
		x2,
		y2,
		state,
	)
	if !visible {
		return nil
	}
	dx := guest.Abs(x2 - x1)
	dy := -guest.Abs(y2 - y1)
	stepX, stepY := -1, -1
	if x1 < x2 {
		stepX = 1
	}
	if y1 < y2 {
		stepY = 1
	}
	difference := dx + dy
	for {
		if err := r.writeWIPICPixel(handle, x1, y1, state); err != nil {
			return err
		}
		if x1 == x2 && y1 == y2 {
			break
		}
		twice := difference * 2
		if twice >= dy {
			difference += dy
			x1 += stepX
		}
		if twice <= dx {
			difference += dx
			y1 += stepY
		}
	}
	return nil
}

func ktfWIPICGraphicsDrawRect(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	values := make([]uint32, 6)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, err
		}
		values[index] = value
	}
	width, height := int(int32(values[3])), int(int32(values[4]))
	if width <= 0 || height <= 0 {
		return 0, nil
	}
	state, err := runtime.wipicGraphicsContext(values[5])
	if err != nil {
		return 0, err
	}
	x := int(int32(values[1])) + state.offsetX
	y := int(int32(values[2])) + state.offsetY
	lines := [][4]int{
		{x, y, x + width - 1, y},
		{x, y + height - 1, x + width - 1, y + height - 1},
		{x, y, x, y + height - 1},
		{x + width - 1, y, x + width - 1, y + height - 1},
	}
	for _, line := range lines {
		if err := runtime.drawWIPICLine(
			values[0],
			line[0],
			line[1],
			line[2],
			line[3],
			state,
		); err != nil {
			return 0, err
		}
	}
	return 0, runtime.commitKTFWIPICFramebuffer(values[0])
}

func ktfWIPICGraphicsFillRect(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	values := make([]uint32, 6)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, fmt.Errorf(
				"read KTF WIPI-C fill-rectangle parameter %d: %w",
				index,
				err,
			)
		}
		values[index] = value
	}
	framebuffer := runtime.wipicFramebuffers[values[0]]
	if framebuffer == nil {
		return 0, nil
	}
	state, err := runtime.wipicGraphicsContext(values[5])
	if err != nil {
		return 0, err
	}
	x := int(int32(values[1])) + state.offsetX
	y := int(int32(values[2])) + state.offsetY
	width := int(int32(values[3]))
	height := int(int32(values[4]))
	left, top := max(0, x), max(0, y)
	right := min(framebuffer.width, x+width)
	bottom := min(framebuffer.height, y+height)
	if state.clipEnabled {
		left = max(left, state.left)
		top = max(top, state.top)
		right = min(right, state.right)
		bottom = min(bottom, state.bottom)
	}
	if left >= right || top >= bottom {
		return 0, nil
	}
	row := make([]byte, (right-left)*2)
	for offset := 0; offset < len(row); offset += 2 {
		binary.LittleEndian.PutUint16(row[offset:], state.foreground)
	}
	for currentY := top; currentY < bottom; currentY++ {
		address := framebuffer.pixels +
			uint32(currentY*framebuffer.stride+left*2)
		if err := runtime.CPU.WriteMemory(address, row); err != nil {
			return 0, fmt.Errorf(
				"fill KTF WIPI-C framebuffer 0x%08x pixels=0x%08x "+
					"row=%d address=0x%08x: %w",
				values[0],
				framebuffer.pixels,
				currentY,
				address,
				err,
			)
		}
	}
	return 0, runtime.commitKTFWIPICFramebuffer(values[0])
}

func ktfWIPICGraphicsCopyFramebuffer(
	ctx context.Context,
	runtime *Runtime,
) (uint32, error) {
	values := make([]uint32, 9)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, err
		}
		values[index] = value
	}
	state, err := runtime.wipicGraphicsContext(values[8])
	if err != nil {
		return 0, err
	}
	if err := runtime.blitWIPICFramebuffer(
		ctx,
		values[0],
		values[5],
		int64(int32(values[1]))+int64(state.offsetX),
		int64(int32(values[2]))+int64(state.offsetY),
		int64(int32(values[3])),
		int64(int32(values[4])),
		int64(int32(values[6])),
		int64(int32(values[7])),
		state,
	); err != nil {
		return 0, err
	}
	return 0, runtime.commitKTFWIPICFramebuffer(values[0])
}

// ktfWIPICGraphicsDrawImage blits an MC_GrpImage into a framebuffer. The image
// wrapper carries its pixels in a framebuffer of its own, so the copy is the
// framebuffer-to-framebuffer one with the source resolved through the image and
// the destination rectangle clipped by the graphics context.
func ktfWIPICGraphicsDrawImage(
	ctx context.Context,
	runtime *Runtime,
) (uint32, error) {
	values := make([]uint32, 9)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return 0, fmt.Errorf(
				"read KTF WIPI-C draw-image parameter %d: %w",
				index,
				err,
			)
		}
		values[index] = value
	}
	image := runtime.wipicImages[values[5]]
	if image == nil {
		return 0, nil
	}
	state, err := runtime.wipicGraphicsContext(values[8])
	if err != nil {
		return 0, err
	}
	if err := runtime.blitWIPICFramebufferKeyed(
		ctx,
		values[0],
		image.framebuffer,
		int64(int32(values[1]))+int64(state.offsetX),
		int64(int32(values[2]))+int64(state.offsetY),
		int64(int32(values[3])),
		int64(int32(values[4])),
		int64(int32(values[6])),
		int64(int32(values[7])),
		state,
		image.alpha,
	); err != nil {
		return 0, err
	}
	return 0, runtime.commitKTFWIPICFramebuffer(values[0])
}

// blitWIPICFramebuffer copies a rectangle between two 16bpp framebuffers,
// clamping it into both and against the context clip.
func (r *Runtime) blitWIPICFramebuffer(
	ctx context.Context,
	destinationHandle, sourceHandle uint32,
	dx, dy, width, height, sx, sy int64,
	state ktfWIPICGraphicsContext,
) error {
	return r.blitWIPICFramebufferKeyed(
		ctx,
		destinationHandle, sourceHandle,
		dx, dy, width, height, sx, sy, state,
		ktfWIPICImageAlpha{key: -1},
	)
}

// blitWIPICFramebufferKeyed copies a rectangle between two 16bpp framebuffers.
// alpha names the source pixels to skip so the destination shows through:
// its mask when the decoded image carried one, otherwise a key of 0..0xffff
// matched against the RGB565 source value, which is how KTF renders an
// MC_grpImage decoded from a color-keyed sprite sheet. No mask and a negative
// key copies opaquely.
func (r *Runtime) blitWIPICFramebufferKeyed(
	ctx context.Context,
	destinationHandle, sourceHandle uint32,
	dx, dy, width, height, sx, sy int64,
	state ktfWIPICGraphicsContext,
	alpha ktfWIPICImageAlpha,
) error {
	destination := r.wipicFramebuffers[destinationHandle]
	source := r.wipicFramebuffers[sourceHandle]
	if destination == nil || source == nil || width <= 0 || height <= 0 {
		return nil
	}
	if sx < 0 {
		width += sx
		dx -= sx
		sx = 0
	}
	if sy < 0 {
		height += sy
		dy -= sy
		sy = 0
	}
	left, top := dx, dy
	right, bottom := dx+width, dy+height
	if state.clipEnabled {
		left = max(left, int64(state.left))
		top = max(top, int64(state.top))
		right = min(right, int64(state.right))
		bottom = min(bottom, int64(state.bottom))
	}
	left = max(left, 0)
	top = max(top, 0)
	right = min(right, int64(destination.width), dx+int64(source.width)-sx)
	bottom = min(bottom, int64(destination.height), dy+int64(source.height)-sy)
	if left >= right || top >= bottom {
		return nil
	}
	rowBytes := int(right-left) * 2
	rowCount := int(bottom - top)
	byteCount := int64(rowBytes) * int64(rowCount)
	if byteCount > 32<<20 {
		return errors.New("KTF WIPI-C framebuffer copy exceeds 32 MiB")
	}
	// Read the entire source rectangle before writing any destination row.
	// CopyArea commonly moves pixels inside one framebuffer and must have
	// memmove semantics for vertical as well as horizontal overlap.
	data := make([]byte, int(byteCount))
	for y := top; y < bottom; y++ {
		sourceAddress := source.pixels +
			uint32((sy+y-dy)*int64(source.stride)+(sx+left-dx)*2)
		rowOffset := int(y-top) * rowBytes
		if err := r.CPU.ReadMemory(
			sourceAddress,
			data[rowOffset:rowOffset+rowBytes],
		); err != nil {
			return err
		}
	}
	// A mask indexes the source framebuffer directly, so it is only usable
	// when it was built for exactly this surface. The clamping above then
	// keeps every index the row loop computes inside it, which is what lets
	// the inner loop read the bitset without a bounds check per pixel.
	masked := len(alpha.mask) != 0 &&
		alpha.width == source.width && alpha.height == source.height
	// A context may install an MC_GrpPixelOpProc, a guest procedure that
	// combines each source pixel with the one already in the destination.
	// 컴투스포춘골프3D draws its rotating main menu that way, one alpha per
	// ring position, so ignoring the procedure painted every entry fully
	// opaque on top of the others (issue #86).
	blend := r.usableWIPICPixelOp(state, int64(rowBytes/2)*int64(rowCount))
	var destinationRow []byte
	if masked || alpha.key >= 0 || blend {
		destinationRow = make([]byte, rowBytes)
	}
	key := uint16(alpha.key)
	for y := top; y < bottom; y++ {
		destinationAddress := destination.pixels +
			uint32(y*int64(destination.stride)+left*2)
		rowOffset := int(y-top) * rowBytes
		sourceRow := data[rowOffset : rowOffset+rowBytes]
		if !masked && alpha.key < 0 && !blend {
			if err := r.CPU.WriteMemory(destinationAddress, sourceRow); err != nil {
				return err
			}
			continue
		}
		// Merge only the opaque source pixels over the current destination so
		// the transparent background shows whatever was already painted there.
		if err := r.CPU.ReadMemory(destinationAddress, destinationRow); err != nil {
			return err
		}
		base := int(sy+y-dy)*alpha.width + int(sx+left-dx)
		for i := 0; i+1 < rowBytes; i += 2 {
			if masked {
				index := base + i/2
				if alpha.mask[index>>6]&(1<<(index&63)) != 0 {
					continue
				}
			} else if alpha.key >= 0 &&
				binary.LittleEndian.Uint16(sourceRow[i:]) == key {
				continue
			}
			value := binary.LittleEndian.Uint16(sourceRow[i:])
			if blend {
				merged, err := r.applyWIPICPixelOp(
					ctx,
					state,
					value,
					binary.LittleEndian.Uint16(destinationRow[i:]),
				)
				if err != nil {
					return err
				}
				// A procedure that faults is retired for the rest of the
				// session; finish this rectangle as a plain copy.
				if !r.usableWIPICPixelOp(state, 0) {
					blend = false
				} else {
					value = merged
				}
			}
			binary.LittleEndian.PutUint16(destinationRow[i:], value)
		}
		if err := r.CPU.WriteMemory(destinationAddress, destinationRow); err != nil {
			return err
		}
	}
	return nil
}

// ktfWIPICPixelOpBudget bounds one MC_GrpPixelOpProc call. The procedures
// titles install are a few dozen instructions of fixed-point blending.
const ktfWIPICPixelOpBudget = 1 << 16

// ktfWIPICPixelOpMaxPixels keeps a pixel-op blit from turning a full-screen
// copy into a hundred thousand guest calls in one frame. Titles use the
// procedure for sprites and panels, never for the background restore.
const ktfWIPICPixelOpMaxPixels = 1 << 16

// ktfWIPICPixelOpCacheLimit bounds the memo table. The procedure is a pure
// function of its three arguments, so caching it collapses a menu that
// repaints the same sprite over the same background every frame down to one
// guest call per distinct pixel pair.
const ktfWIPICPixelOpCacheLimit = 1 << 16

type ktfWIPICPixelOpKey struct {
	procedure   uint32
	parameter   uint32
	source      uint16
	destination uint16
}

// usableWIPICPixelOp reports whether this context's procedure should run for a
// rectangle of the given pixel count. A zero count only asks whether the
// procedure is still trusted.
func (r *Runtime) usableWIPICPixelOp(
	state ktfWIPICGraphicsContext,
	pixels int64,
) bool {
	if state.pixelOp == 0 || r.brokenWIPICPixelOps[state.pixelOp] {
		return false
	}
	if pixels > ktfWIPICPixelOpMaxPixels {
		return false
	}
	// The procedure lives in the loaded client image like any other guest
	// function; a word that does not is leftover data, not a callback.
	return state.pixelOp >= ImageBase &&
		uint64(state.pixelOp) < uint64(ImageBase)+uint64(r.ImageSz)
}

func (r *Runtime) applyWIPICPixelOp(
	ctx context.Context,
	state ktfWIPICGraphicsContext,
	source, destination uint16,
) (uint16, error) {
	key := ktfWIPICPixelOpKey{
		procedure:   state.pixelOp,
		parameter:   state.pixelParam,
		source:      source,
		destination: destination,
	}
	if merged, ok := r.wipicPixelOpResults[key]; ok {
		return merged, nil
	}
	_, value, err := r.call(
		ctx,
		state.pixelOp,
		[]uint32{uint32(source), uint32(destination), state.pixelParam},
		ktfWIPICPixelOpBudget,
	)
	// A nested call returns through the KTF return sentinel, which reports a
	// breakpoint stop; only the error says whether it completed.
	if err != nil {
		r.brokenWIPICPixelOps[state.pixelOp] = true
		r.tracef(
			"wipic_pixel_op_retired:procedure=0x%08x:error=%v",
			state.pixelOp,
			err,
		)
		return source, nil
	}
	merged := uint16(value)
	if len(r.wipicPixelOpResults) >= ktfWIPICPixelOpCacheLimit {
		clear(r.wipicPixelOpResults)
	}
	r.wipicPixelOpResults[key] = merged
	return merged, nil
}

func (r *Runtime) writeWIPICPixel(
	handle uint32,
	x, y int,
	state ktfWIPICGraphicsContext,
) error {
	return r.writeWIPICPixelValue(handle, x, y, state, state.foreground)
}

func (r *Runtime) writeWIPICPixelValue(
	handle uint32,
	x, y int,
	state ktfWIPICGraphicsContext,
	value uint16,
) error {
	framebuffer := r.wipicFramebuffers[handle]
	if framebuffer == nil ||
		x < 0 || y < 0 ||
		x >= framebuffer.width || y >= framebuffer.height {
		return nil
	}
	if state.clipEnabled &&
		(x < state.left || y < state.top ||
			x >= state.right || y >= state.bottom) {
		return nil
	}
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	return r.CPU.WriteMemory(
		framebuffer.pixels+uint32(y*framebuffer.stride+x*2),
		encoded[:],
	)
}

func (r *Runtime) writeWIPICPixelAlpha(
	handle uint32,
	x, y int,
	state ktfWIPICGraphicsContext,
	alpha byte,
) error {
	if alpha == 0 {
		return nil
	}
	if alpha == 0xff {
		return r.writeWIPICPixel(handle, x, y, state)
	}
	framebuffer := r.wipicFramebuffers[handle]
	if framebuffer == nil ||
		x < 0 || y < 0 ||
		x >= framebuffer.width || y >= framebuffer.height {
		return nil
	}
	if state.clipEnabled &&
		(x < state.left || y < state.top ||
			x >= state.right || y >= state.bottom) {
		return nil
	}
	address := framebuffer.pixels + uint32(y*framebuffer.stride+x*2)
	var encoded [2]byte
	if err := r.CPU.ReadMemory(address, encoded[:]); err != nil {
		return err
	}
	destination := binary.LittleEndian.Uint16(encoded[:])
	binary.LittleEndian.PutUint16(
		encoded[:],
		blendKTFWIPICRGB565(destination, state.foreground, alpha),
	)
	return r.CPU.WriteMemory(address, encoded[:])
}

func blendKTFWIPICRGB565(destination, source uint16, alpha byte) uint16 {
	blend := func(destination, source uint16) uint16 {
		coverage := uint32(alpha)
		return uint16(
			(uint32(source)*coverage +
				uint32(destination)*(0xff-coverage) + 127) / 0xff,
		)
	}
	return blend(destination>>11, source>>11)<<11 |
		blend(destination>>5&0x3f, source>>5&0x3f)<<5 |
		blend(destination&0x1f, source&0x1f)
}
