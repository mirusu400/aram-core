package wipi

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) dispatchGraphics(name string) (guest.WIPIReturn, bool, error) {
	count, modeled := graphicsArgumentCount(name)
	if !modeled {
		return guest.WIPIReturn{}, false, nil
	}
	values, err := r.args(count)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	args := make([]uint32, 9)
	copy(args, values)
	switch name {
	case "MC_grpGetImageProperty":
		value, err := r.imageProperty(args[0], int32(args[1]))
		return guest.WIPIReturn{Low: uint32(value)}, true, err
	case "MC_grpGetImageFrameBuffer":
		image, ok, err := r.readImage(args[0])
		if err != nil || !ok {
			return guest.WIPIReturn{}, true, err
		}
		return guest.WIPIReturn{Low: image.framebuffer}, true, nil
	case "MC_grpGetScreenFrameBuffer":
		if int32(args[0]) < 0 || args[0] > 1 {
			return guest.WIPIReturn{}, true, nil
		}
		handle, err := r.EnsureScreenFramebuffer()
		return guest.WIPIReturn{Low: handle}, true, err
	case "MC_grpCreateOffScreenFrameBuffer":
		handle, err := r.newFramebuffer(int(int32(args[0])), int(int32(args[1])), true)
		return guest.WIPIReturn{Low: handle}, true, err
	case "MC_grpDestroyOffScreenFrameBuffer":
		fb, ok := r.Framebuffers[args[0]]
		if ok && fb.Handle != r.ScreenHandle {
			if serviceID := r.surfaceServices[args[0]]; serviceID != 0 {
				if err := r.Services.Graphics.DestroySurface(
					r.ServiceOwner,
					serviceID,
				); err != nil {
					return guest.WIPIReturn{}, true, err
				}
				delete(r.surfaceServices, args[0])
			}
			r.Heap.Release(fb.Pixels)
			r.Heap.Release(fb.Handle)
			delete(r.Framebuffers, args[0])
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_grpInitContext":
		return guest.WIPIReturn{}, true, r.initializeGraphicsContext(args[0])
	case "MC_grpSetContext", "MC_grpGetContext":
		return guest.WIPIReturn{}, true, r.transferGraphicsContext(name, args[0], int32(args[1]), args[2])
	case "MC_grpPutPixel":
		return guest.WIPIReturn{}, true, r.putPixel(args[0], int(int32(args[1])), int(int32(args[2])), args[3], nil)
	case "MC_grpDrawLine":
		return guest.WIPIReturn{}, true, r.drawLine(
			args[0],
			int(int32(args[1])),
			int(int32(args[2])),
			int(int32(args[3])),
			int(int32(args[4])),
			args[5],
		)
	case "MC_grpDrawImage":
		return guest.WIPIReturn{}, true, r.drawImage(args)
	case "MC_grpDrawRect", "MC_grpFillRect":
		return guest.WIPIReturn{}, true, r.drawRect(name == "MC_grpFillRect", args)
	case "MC_grpDrawArc", "MC_grpFillArc":
		return guest.WIPIReturn{}, true, r.drawArc(name == "MC_grpFillArc", args)
	case "MC_grpDrawString", "MC_grpDrawUnicodeString":
		return guest.WIPIReturn{}, true, r.drawText(name == "MC_grpDrawUnicodeString", args)
	case "MC_grpGetPixelFromRGB":
		return guest.WIPIReturn{
			Low: r.devicePixelFromRGB(args[0], args[1], args[2]),
		}, true, nil
	case "MC_grpGetRGBFromPixel":
		red, green, blue := r.rgbFromDevicePixel(args[0])
		for index, component := range []uint32{red, green, blue} {
			if args[index+1] != 0 {
				if err := r.WriteU32(args[index+1], component); err != nil {
					return guest.WIPIReturn{}, true, err
				}
			}
		}
		return guest.WIPIReturn{Low: args[0]}, true, nil
	case "MC_grpGetDisplayInfo":
		return r.getDisplayInfo(args[0], args[1])
	case "MC_grpGetFont":
		return guest.WIPIReturn{Low: args[0]&0xe0 | args[2]<<8 | args[1]&0x1f}, true, nil
	case "MC_grpGetFontHeight", "MC_grpGetFontAscent", "MC_grpGetFontDescent":
		height := guest.FontHeight(args[0])
		switch name {
		case "MC_grpGetFontAscent":
			height -= height / 4
		case "MC_grpGetFontDescent":
			height /= 4
		}
		return guest.WIPIReturn{Low: uint32(height)}, true, nil
	case "MC_grpGetStringWidth":
		return r.stringWidth(args[0], args[1], int32(args[2]), false)
	case "MC_grpGetUnicodeStringWidth":
		return r.stringWidth(args[0], args[1], int32(args[2]), true)
	case "MC_grpGetRGBPixels":
		return guest.WIPIReturn{}, true, r.getRGBPixels(args)
	case "MC_grpSetRGBPixels":
		return guest.WIPIReturn{}, true, r.setRGBPixels(args)
	case "MC_grpCopyFrameBuffer":
		return guest.WIPIReturn{}, true, r.copyFramebuffer(args)
	case "MC_grpCopyArea":
		return guest.WIPIReturn{}, true, r.copyArea(args)
	case "MC_grpFlushLcd":
		return guest.WIPIReturn{}, true, r.present(args[1])
	case "MC_grpRepaint":
		return guest.WIPIReturn{}, true, r.present(r.ScreenHandle)
	case "MC_grpCreateImage":
		result, err := r.createImage(args[0], args[1], int32(args[2]), int32(args[3]))
		return guest.WIPIReturn{Low: uint32(result)}, true, err
	case "MC_grpDestroyImage":
		return guest.WIPIReturn{}, true, r.destroyImage(args[0])
	case "MC_grpDecodeNextImage":
		result, err := r.decodeNextImage(args[0])
		return guest.WIPIReturn{Low: uint32(result)}, true, err
	case "MC_grpEncodeImage":
		result, err := r.encodeImage(args)
		return guest.WIPIReturn{Low: result}, true, err
	case "MC_grpPostEvent":
		if len(r.GraphicsEvents) >= wipiMaxGraphicsEvents {
			return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPINoMemory)}, true, nil
		}
		r.GraphicsEvents = append(r.GraphicsEvents, GraphicsEvent{
			ID:     int32(args[0]),
			Kind:   int32(args[1]),
			Param1: int32(args[2]),
			Param2: int32(args[3]),
		})
		return guest.WIPIReturn{}, true, nil
	case "MC_grpDrawPolygon", "MC_grpDrawFillPolygon":
		return guest.WIPIReturn{}, true, r.drawPolygon(name == "MC_grpDrawFillPolygon", args)
	default:
		return guest.WIPIReturn{}, false, nil
	}
}

func graphicsArgumentCount(name string) (int, bool) {
	switch name {
	case "MC_grpGetScreenFrameBuffer", "MC_grpDestroyOffScreenFrameBuffer",
		"MC_grpInitContext", "MC_grpGetFontHeight", "MC_grpGetFontAscent",
		"MC_grpGetFontDescent", "MC_grpGetImageFrameBuffer",
		"MC_grpDestroyImage", "MC_grpDecodeNextImage":
		return 1, true
	case "MC_grpCreateOffScreenFrameBuffer", "MC_grpGetDisplayInfo",
		"MC_grpGetImageProperty":
		return 2, true
	case "MC_grpSetContext", "MC_grpGetContext", "MC_grpGetPixelFromRGB",
		"MC_grpGetFont", "MC_grpGetStringWidth", "MC_grpGetUnicodeStringWidth":
		return 3, true
	case "MC_grpPutPixel", "MC_grpGetRGBFromPixel", "MC_grpPostEvent",
		"MC_grpCreateImage":
		return 4, true
	case "MC_grpRepaint", "MC_grpDrawPolygon", "MC_grpDrawFillPolygon":
		return 5, true
	case "MC_grpDrawLine", "MC_grpDrawRect", "MC_grpFillRect", "MC_grpFlushLcd",
		"MC_grpDrawString", "MC_grpDrawUnicodeString", "MC_grpEncodeImage":
		return 6, true
	case "MC_grpGetRGBPixels":
		return 7, true
	case "MC_grpSetRGBPixels", "MC_grpCopyArea", "MC_grpDrawArc", "MC_grpFillArc":
		return 8, true
	case "MC_grpCopyFrameBuffer", "MC_grpDrawImage":
		return 9, true
	default:
		return 0, false
	}
}

func (r *Runtime) EnsureScreenFramebuffer() (uint32, error) {
	if r.ScreenHandle != 0 {
		return r.ScreenHandle, nil
	}
	handle, err := r.newFramebuffer(r.Frame.Bounds().Dx(), r.Frame.Bounds().Dy(), false)
	if err != nil {
		return 0, err
	}
	r.ScreenHandle = handle
	r.screenPixels = r.Framebuffers[handle].Pixels
	return handle, nil
}

func (r *Runtime) newFramebuffer(width, height int, owns bool) (uint32, error) {
	if width <= 0 || height <= 0 || width > 4096 || height > 4096 {
		return 0, nil
	}
	bytesPerPixel := r.framebufferBits / 8
	if bytesPerPixel != 2 && bytesPerPixel != 4 {
		bytesPerPixel = 4
	}
	pixelBytes := uint64(width) * uint64(height) * uint64(bytesPerPixel)
	if pixelBytes > uint64(guest.HeapSize) {
		return 0, nil
	}
	pixels, err := r.Heap.Allocate(uint32(pixelBytes), true)
	if err != nil || pixels == 0 {
		return 0, err
	}
	handle, err := r.Heap.Allocate(24, true)
	if err != nil || handle == 0 {
		r.Heap.Release(pixels)
		return 0, err
	}
	values := [...]uint32{
		pixels,
		uint32(width),
		uint32(height),
		uint32(width * bytesPerPixel),
		uint32(bytesPerPixel * 8),
		0,
	}
	if owns {
		values[5] = 1
	}
	var descriptor [24]byte
	for index, value := range values {
		binary.LittleEndian.PutUint32(descriptor[index*4:], value)
	}
	if err := r.CPU.WriteMemory(handle, descriptor[:]); err != nil {
		return 0, err
	}
	r.Framebuffers[handle] = Framebuffer{
		Handle:       handle,
		Pixels:       pixels,
		Width:        width,
		Height:       height,
		BitsPerPixel: bytesPerPixel * 8,
		owns:         owns,
	}
	if !owns {
		// Only the screen needs its mirror up front, because SetScreen takes
		// it. Everything else materializes one on first use.
		serviceID, err := r.ensureSurface(handle)
		if err == nil {
			err = r.Services.Graphics.SetScreen(r.ServiceOwner, serviceID)
		}
		if err != nil {
			if serviceID != 0 {
				_ = r.Services.Graphics.DestroySurface(r.ServiceOwner, serviceID)
			}
			delete(r.surfaceServices, handle)
			delete(r.Framebuffers, handle)
			r.Heap.Release(handle)
			r.Heap.Release(pixels)
			return 0, err
		}
	}
	return handle, nil
}

// ensureSurface returns the shared-service mirror of a guest framebuffer,
// creating it on first use.
//
// WIPI-C drawing runs entirely on the guest pixel memory the framebuffer
// points at; the mirror is only read where a framebuffer leaves the guest —
// when it is presented or encoded. Mirroring every framebuffer eagerly spent
// one service surface per MC_grpImage, so 무한신맞고2009, which decodes an
// image per sprite as it plays, crossed the 1024-surface service limit about
// eighty seconds in and faulted with "MC_grpCreateImage: surface count
// reached 1024" (issue #78).
func (r *Runtime) ensureSurface(handle uint32) (shared.ServiceID, error) {
	if serviceID := r.surfaceServices[handle]; serviceID != 0 {
		return serviceID, nil
	}
	framebuffer, ok := r.Framebuffers[handle]
	if !ok {
		return 0, nil
	}
	format := shared.PixelBGRX8888
	if framebuffer.BitsPerPixel == 16 {
		format = shared.PixelRGB565
	}
	serviceID, err := r.Services.Graphics.CreateSurface(
		r.ServiceOwner,
		shared.SurfaceDescriptor{
			Width:  int32(framebuffer.Width),
			Height: int32(framebuffer.Height),
			Stride: int32(uint32(framebuffer.Width) * framebuffer.bytesPerPixel()),
			Format: format,
		},
	)
	if err != nil {
		return 0, err
	}
	r.surfaceServices[handle] = serviceID
	return serviceID, nil
}

// graphicsContextField locates one MC_GrpContext member.
type graphicsContextField struct {
	offset uint32
	size   uint32
}

// graphicsContextOffsets maps an MC_grpSetContext index to the member it
// addresses. Two spellings of MC_GrpContext exist in the field: the SDK
// struct the Samsung WIPI-C runtime uses carries a clip_enabled word between
// the clip rectangle and the colours, while LGT's Raptor runtime has no such
// member and packs every scalar four bytes earlier. A Clet that reads the
// struct itself only agrees with its own vendor: 블레이드마스터4 fills a text
// strip with the colour it reads back from offset 0x10, so with the
// clip_enabled spelling it filled with the clip flag (black) instead of the
// magenta it keys out, and every string arrived on a black plate.
func (r *Runtime) graphicsContextOffsets() map[int32]graphicsContextField {
	if r.CompactGraphicsContext {
		return map[int32]graphicsContextField{
			0: {0, 16}, 1: {16, 4}, 2: {20, 4}, 4: {24, 4}, 5: {28, 4},
			6: {32, 4}, 7: {36, 4}, 8: {40, 4}, 9: {44, 4}, 10: {48, 8},
		}
	}
	return map[int32]graphicsContextField{
		0: {0, 16}, 1: {20, 4}, 2: {24, 4}, 4: {28, 4}, 5: {32, 4},
		6: {36, 4}, 7: {40, 4}, 8: {44, 4}, 9: {48, 4}, 10: {52, 8},
	}
}

func (r *Runtime) initializeGraphicsContext(address uint32) error {
	if address == 0 {
		return nil
	}
	values := [...]uint32{
		0, 0, 0x7fffffff, 0x7fffffff, 0,
		0, r.deviceWhite(), 255, 0, 0, 0, 0, 0, 0, 0,
	}
	if r.CompactGraphicsContext {
		values = [...]uint32{
			0, 0, 0x7fffffff, 0x7fffffff,
			0, r.deviceWhite(), 255, 0, 0, 0, 0, 0, 0, 0, 0,
		}
	}
	var encoded [15 * 4]byte
	for index, value := range values {
		binary.LittleEndian.PutUint32(encoded[index*4:], value)
	}
	return r.CPU.WriteMemory(address, encoded[:])
}

func (r *Runtime) transferGraphicsContext(name string, context uint32, index int32, pointer uint32) error {
	offsets := r.graphicsContextOffsets()
	field, ok := offsets[index]
	if context == 0 || !ok {
		return nil
	}
	data := make([]byte, field.size)
	if name == "MC_grpSetContext" {
		// Only the clip rectangle and the translation pair need a buffer;
		// every scalar field arrives as the value itself cast to void*.
		// Reading a scalar back as an address would pick up whatever the
		// guest happens to hold there, and a colour on a 16bpp screen is
		// small enough to land inside the loaded image.
		if field.size == 4 {
			binary.LittleEndian.PutUint32(data, pointer)
		} else {
			if pointer == 0 {
				return nil
			}
			if err := r.CPU.ReadMemory(pointer, data); err != nil {
				return err
			}
		}
		if err := r.CPU.WriteMemory(context+field.offset, data); err != nil {
			return err
		}
		if index == 0 && !r.CompactGraphicsContext {
			return r.WriteU32(context+16, 1)
		}
		return nil
	}
	if pointer == 0 {
		return nil
	}
	if err := r.CPU.ReadMemory(context+field.offset, data); err != nil {
		return err
	}
	return r.CPU.WriteMemory(pointer, data)
}

type wipiGraphicsContext struct {
	left, top, right, bottom int
	clipEnabled              bool
	foreground               uint32
	background               uint32
	alpha                    int32
	pixelOperation           uint32
	pixelParameter           int32
	font                     uint32
	style                    int32
	xor                      bool
	offsetX, offsetY         int
}

func (r *Runtime) context(address uint32) (wipiGraphicsContext, error) {
	if address == 0 {
		return wipiGraphicsContext{
			right:      int(^uint32(0) >> 1),
			bottom:     int(^uint32(0) >> 1),
			background: r.deviceWhite(),
			alpha:      255,
		}, nil
	}
	var encoded [60]byte
	if err := r.CPU.ReadMemory(address, encoded[:]); err != nil {
		return wipiGraphicsContext{}, err
	}
	if r.CompactGraphicsContext {
		left := int(int32(binary.LittleEndian.Uint32(encoded[0:4])))
		top := int(int32(binary.LittleEndian.Uint32(encoded[4:8])))
		right := int(int32(binary.LittleEndian.Uint32(encoded[8:12])))
		bottom := int(int32(binary.LittleEndian.Uint32(encoded[12:16])))
		return wipiGraphicsContext{
			left:   left,
			top:    top,
			right:  right,
			bottom: bottom,
			// Without a clip_enabled word the rectangle speaks for itself. A
			// context a Clet never initialised holds zeroes, and clipping
			// every pixel away would blank the screen, so an empty rectangle
			// reads as "no clip" the way an uninitialised flag used to.
			clipEnabled:    right > left && bottom > top,
			foreground:     binary.LittleEndian.Uint32(encoded[16:20]) & 0xffffff,
			background:     binary.LittleEndian.Uint32(encoded[20:24]) & 0xffffff,
			alpha:          int32(binary.LittleEndian.Uint32(encoded[24:28])),
			pixelOperation: binary.LittleEndian.Uint32(encoded[28:32]),
			pixelParameter: int32(binary.LittleEndian.Uint32(encoded[32:36])),
			font:           binary.LittleEndian.Uint32(encoded[36:40]),
			style:          int32(binary.LittleEndian.Uint32(encoded[40:44])),
			xor:            binary.LittleEndian.Uint32(encoded[44:48]) != 0,
			offsetX:        int(int32(binary.LittleEndian.Uint32(encoded[48:52]))),
			offsetY:        int(int32(binary.LittleEndian.Uint32(encoded[52:56]))),
		}, nil
	}
	return wipiGraphicsContext{
		left:           int(int32(binary.LittleEndian.Uint32(encoded[0:4]))),
		top:            int(int32(binary.LittleEndian.Uint32(encoded[4:8]))),
		right:          int(int32(binary.LittleEndian.Uint32(encoded[8:12]))),
		bottom:         int(int32(binary.LittleEndian.Uint32(encoded[12:16]))),
		clipEnabled:    binary.LittleEndian.Uint32(encoded[16:20]) != 0,
		foreground:     binary.LittleEndian.Uint32(encoded[20:24]) & 0xffffff,
		background:     binary.LittleEndian.Uint32(encoded[24:28]) & 0xffffff,
		alpha:          int32(binary.LittleEndian.Uint32(encoded[28:32])),
		pixelOperation: binary.LittleEndian.Uint32(encoded[32:36]),
		pixelParameter: int32(binary.LittleEndian.Uint32(encoded[36:40])),
		font:           binary.LittleEndian.Uint32(encoded[40:44]),
		style:          int32(binary.LittleEndian.Uint32(encoded[44:48])),
		xor:            binary.LittleEndian.Uint32(encoded[48:52]) != 0,
		offsetX:        int(int32(binary.LittleEndian.Uint32(encoded[52:56]))),
		offsetY:        int(int32(binary.LittleEndian.Uint32(encoded[56:60]))),
	}, nil
}

func (fb Framebuffer) bytesPerPixel() uint32 {
	if fb.BitsPerPixel == 16 {
		return 2
	}
	return 4
}

func (r *Runtime) framebufferPixel(
	fb Framebuffer,
	x, y int,
) (uint32, error) {
	address := fb.Pixels +
		uint32(y*fb.Width+x)*fb.bytesPerPixel()
	// The scratch lives on the runtime so the slice handed to the CPU
	// interface does not escape to the heap on every pixel.
	if fb.BitsPerPixel == 16 {
		encoded := r.pixelScratch[:2]
		if err := r.CPU.ReadMemory(address, encoded); err != nil {
			return 0, err
		}
		return uint32(binary.LittleEndian.Uint16(encoded)), nil
	}
	encoded := r.pixelScratch[:4]
	if err := r.CPU.ReadMemory(address, encoded); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(encoded), nil
}

func (r *Runtime) writeFramebufferPixel(
	fb Framebuffer,
	x, y int,
	pixel uint32,
) error {
	address := fb.Pixels +
		uint32(y*fb.Width+x)*fb.bytesPerPixel()
	if fb.BitsPerPixel == 16 {
		encoded := r.pixelScratch[:2]
		binary.LittleEndian.PutUint16(encoded, uint16(pixel))
		if err := r.CPU.WriteMemory(address, encoded); err != nil {
			return err
		}
		if serviceID := r.surfaceServices[fb.Handle]; serviceID != 0 {
			offset := uint64((y*fb.Width + x) * 2)
			return r.Services.Graphics.WritePixelBytes(
				r.ServiceOwner,
				serviceID,
				offset,
				encoded,
			)
		}
		return nil
	}
	encoded := r.pixelScratch[:4]
	binary.LittleEndian.PutUint32(encoded, pixel&0xffffff)
	if err := r.CPU.WriteMemory(address, encoded); err != nil {
		return err
	}
	if serviceID := r.surfaceServices[fb.Handle]; serviceID != 0 {
		offset := uint64((y*fb.Width + x) * 4)
		return r.Services.Graphics.WritePixelBytes(
			r.ServiceOwner,
			serviceID,
			offset,
			encoded,
		)
	}
	return nil
}

// deviceWhite is the background an untouched context starts on, spelled the
// way this screen stores a pixel.
func (r *Runtime) deviceWhite() uint32 {
	if r.framebufferBits == 16 {
		return 0xffff
	}
	return 0xffffff
}

// A colour crosses the WIPI-C graphics API already spelled the way the screen
// stores a pixel: MC_grpGetPixelFromRGB is the conversion, and everything
// downstream — a graphics context's colour, a raw framebuffer word a Clet
// writes itself — carries that spelling. On a 32bpp screen it is plain 24-bit
// RGB; on a 16bpp one it is RGB565.
func (r *Runtime) devicePixelFromRGB(red, green, blue uint32) uint32 {
	red &= 0xff
	green &= 0xff
	blue &= 0xff
	if r.framebufferBits == 16 {
		return red>>3<<11 | green>>2<<5 | blue>>3
	}
	return red<<16 | green<<8 | blue
}

func (r *Runtime) rgbFromDevicePixel(pixel uint32) (uint32, uint32, uint32) {
	if r.framebufferBits == 16 {
		red := pixel >> 11 & 0x1f
		green := pixel >> 5 & 0x3f
		blue := pixel & 0x1f
		return red<<3 | red>>2, green<<2 | green>>4, blue<<3 | blue>>2
	}
	return pixel >> 16 & 0xff, pixel >> 8 & 0xff, pixel & 0xff
}

func (r *Runtime) putPixel(handle uint32, x, y int, contextAddress uint32, override *uint32) error {
	return r.putPixelCoverage(handle, x, y, contextAddress, override, 0xff)
}

func (r *Runtime) putPixelCoverage(
	handle uint32,
	x, y int,
	contextAddress uint32,
	override *uint32,
	coverage byte,
) error {
	if coverage == 0 {
		return nil
	}
	fb, ok := r.Framebuffers[handle]
	if !ok {
		return nil
	}
	context, err := r.context(contextAddress)
	if err != nil {
		return err
	}
	return r.putPixelDecoded(fb, x, y, &context, override, coverage)
}

// putPixelDecoded is putPixelCoverage for a caller that has already resolved
// the framebuffer and decoded the context once for a whole primitive. The
// per-pixel context decode was a 60-byte guest read per plotted pixel, a fifth
// of 판타지포에버3's frame.
func (r *Runtime) putPixelDecoded(
	fb Framebuffer,
	x, y int,
	context *wipiGraphicsContext,
	override *uint32,
	coverage byte,
) error {
	if coverage == 0 {
		return nil
	}
	x += context.offsetX
	y += context.offsetY
	if x < 0 || y < 0 || x >= fb.Width || y >= fb.Height {
		return nil
	}
	if context.clipEnabled &&
		!(x >= context.left && x < context.right && y >= context.top && y < context.bottom) {
		return nil
	}
	// A context colour is already spelled the way the framebuffer stores it:
	// a Clet converts once with MC_grpGetPixelFromRGB and hands that pixel to
	// MC_grpSetContext, so re-encoding it here would spell it twice. 영웅서기3
	// sets white as 0xffff and 블레이드마스터4 sets its palette entries the same
	// way; writeFramebufferPixel drops the unused half on a 16bpp screen.
	foreground := context.foreground
	if override != nil {
		foreground = *override
	}
	destination, err := r.framebufferPixel(fb, x, y)
	if err != nil {
		return err
	}
	merged, err := r.compositePixel(context, foreground, destination, coverage)
	if err != nil {
		return err
	}
	return r.writeFramebufferPixel(fb, x, y, merged)
}

// compositePixel blends one foreground pixel onto the destination under the
// context: the installed pixel operation when it has one and it is still
// trusted, else XOR, else the context alpha scaled by coverage. It is the one
// blend shared by the scalar path and the row path, so the two cannot drift.
func (r *Runtime) compositePixel(
	context *wipiGraphicsContext,
	foreground, destination uint32,
	coverage byte,
) (uint32, error) {
	if context.pixelOperation != 0 {
		merged, ok, err := r.pixelOperationResult(context, foreground, destination)
		if err != nil {
			return 0, err
		}
		if ok {
			if coverage < 0xff {
				merged = r.blendDevicePixel(destination, merged, uint32(coverage))
			}
			return merged, nil
		}
	}
	if context.xor {
		merged := destination ^ foreground
		if coverage < 0xff {
			merged = r.blendDevicePixel(destination, merged, uint32(coverage))
		}
		return merged, nil
	}
	alpha := uint32(0)
	if context.alpha > 0 {
		alpha = uint32(context.alpha)
		if alpha > 0xff {
			alpha = 0xff
		}
		alpha = alpha * uint32(coverage) / 0xff
	}
	return r.blendDevicePixel(destination, foreground, alpha), nil
}

// compositeSpan composites count pixels of one framebuffer row starting at
// (x, y), coordinates the caller has already offset by the context, reading
// the destination run from guest memory once and writing each touched run
// back once. values, when set, supplies the foreground of each pixel and
// transparent, when set, marks pixels to leave alone; both are indexed from
// the unclipped x. Pixels are blended left to right exactly as the scalar
// path would, so a pixel operation sees the same calls in the same order and
// a retirement mid-row switches the rest of the row the same way. The scalar
// path cost three guest-memory round trips per pixel, each taking the CPU
// mutex and a region lookup; a filled rectangle spent 75% of 판타지포에버3's
// frame in them.
func (r *Runtime) compositeSpan(
	fb Framebuffer,
	x, y, count int,
	context *wipiGraphicsContext,
	values []uint32,
	transparent []bool,
) error {
	if count <= 0 || y < 0 || y >= fb.Height {
		return nil
	}
	first, end := x, x+count
	if context.clipEnabled {
		if y < context.top || y >= context.bottom {
			return nil
		}
		first = max(first, context.left)
		end = min(end, context.right)
	}
	first = max(first, 0)
	end = min(end, fb.Width)
	if first >= end {
		return nil
	}
	bytesPerPixel := int(fb.bytesPerPixel())
	row := y * fb.Width
	address := fb.Pixels + uint32(row+first)*uint32(bytesPerPixel)
	// A pixel operation re-enters the guest, and the guest may draw on its
	// way through; give such a row its own buffer instead of the shared one.
	buffer := r.spanBuffer((end-first)*bytesPerPixel, context.pixelOperation != 0)
	if err := r.CPU.ReadMemory(address, buffer); err != nil {
		return err
	}
	serviceID := r.surfaceServices[fb.Handle]
	runStart := -1
	for column := first; column < end; column++ {
		if transparent != nil && transparent[column-x] {
			if err := r.writeSpanRun(fb, serviceID, row, first, runStart, column, buffer); err != nil {
				return err
			}
			runStart = -1
			continue
		}
		offset := (column - first) * bytesPerPixel
		var destination uint32
		if bytesPerPixel == 2 {
			destination = uint32(binary.LittleEndian.Uint16(buffer[offset:]))
		} else {
			destination = binary.LittleEndian.Uint32(buffer[offset:])
		}
		foreground := context.foreground
		if values != nil {
			foreground = values[column-x]
		}
		merged, err := r.compositePixel(context, foreground, destination, 0xff)
		if err != nil {
			// Keep what was already blended, as the scalar path would have.
			if writeErr := r.writeSpanRun(fb, serviceID, row, first, runStart, column, buffer); writeErr != nil {
				return writeErr
			}
			return err
		}
		if bytesPerPixel == 2 {
			binary.LittleEndian.PutUint16(buffer[offset:], uint16(merged))
		} else {
			binary.LittleEndian.PutUint32(buffer[offset:], merged&0xffffff)
		}
		if runStart < 0 {
			runStart = column
		}
	}
	return r.writeSpanRun(fb, serviceID, row, first, runStart, end, buffer)
}

// writeSpanRun writes the blended pixels [runStart, runEnd) of a compositeSpan
// row, whose buffer starts at column first, to guest memory and to the
// surface mirror when the framebuffer has one. A negative runStart is an empty
// run. Only touched pixels are written, so a pixel the span left alone never
// refreshes the mirror any more than the scalar path did.
func (r *Runtime) writeSpanRun(
	fb Framebuffer,
	serviceID shared.ServiceID,
	row, first, runStart, runEnd int,
	buffer []byte,
) error {
	if runStart < 0 || runStart >= runEnd {
		return nil
	}
	bytesPerPixel := int(fb.bytesPerPixel())
	from, to := (runStart-first)*bytesPerPixel, (runEnd-first)*bytesPerPixel
	address := fb.Pixels + uint32(row+runStart)*uint32(bytesPerPixel)
	if err := r.CPU.WriteMemory(address, buffer[from:to]); err != nil {
		return err
	}
	if serviceID == 0 {
		return nil
	}
	return r.Services.Graphics.WritePixelBytes(
		r.ServiceOwner,
		serviceID,
		uint64((row+runStart)*bytesPerPixel),
		buffer[from:to],
	)
}

// spanBuffer returns a row buffer of size bytes: the shared framebuffer
// scratch normally, a private one when the row will re-enter the guest.
func (r *Runtime) spanBuffer(size int, private bool) []byte {
	if private {
		return make([]byte, size)
	}
	return r.framebufferBuffer(size)
}

// wipiPixelOpCacheLimit bounds the pixel-operation memo. A 16bpp title can
// only ever produce 65536 distinct pixels per side, and a 32bpp title that
// somehow exceeds the limit just starts a fresh table.
const wipiPixelOpCacheLimit = 1 << 16

// wipiPixelOpKey identifies one pixel-operation result. It carries the
// procedure and its parameter, so installing a different operation misses the
// memo instead of needing an explicit clear, and the two pixels in the order
// the guest receives them, so the vendor argument swap is part of the key.
type wipiPixelOpKey struct {
	procedure uint32
	parameter uint32
	first     uint32
	second    uint32
}

// pixelOperationResult runs the context's pixel-operation callback for one
// pixel pair. It reports ok=false when the procedure has been retired, in
// which case the caller composites as if no operation were installed.
//
// MC_grpSetContext installs a guest pixel-operation callback. The public
// Samsung model recovered the firmware signature as op(srcPixel, dstPixel,
// param) and the model test pins that order. LGT's Raptor runtime hands the
// two pixels the other way round: 판타지포에버3's op returns its second
// argument unless that argument is the transparent key, and it only
// composites a recognizable in-game scene when the destination is passed
// first (source first left every blit returning the untouched destination, so
// the whole scene stayed black). The vendor split mirrors the divergent
// context struct handled by CompactGraphicsContext, so key the argument order
// off the same flag.
//
// Every call is a synchronous guest re-entry, and a filled rectangle or a
// colour-keyed sprite blit asks for one per pixel, so the result is memoized
// the way the KTF WIPI-C runtime memoizes MC_GrpPixelOpProc: the procedure is
// a pure function of its three arguments. A procedure that faults or overruns
// its budget is retired for the session rather than failing the draw, which
// is also the KTF contract; a cancelled frame is not a broken procedure and
// still surfaces as an error.
func (r *Runtime) pixelOperationResult(
	context *wipiGraphicsContext,
	foreground, destination uint32,
) (uint32, bool, error) {
	procedure := context.pixelOperation
	if r.brokenPixelOps[procedure] {
		return 0, false, nil
	}
	first, second := foreground, destination
	if r.CompactGraphicsContext {
		first, second = destination, foreground
	}
	key := wipiPixelOpKey{
		procedure: procedure,
		parameter: uint32(context.pixelParameter),
		first:     first,
		second:    second,
	}
	if merged, ok := r.pixelOpResults[key]; ok {
		return merged, true, nil
	}
	merged, err := r.CallGuestFunction(procedure, first, second, key.parameter)
	if err != nil {
		// A missing guest runner is a host wiring fault, not a guest one.
		if r.InvokeSync == nil {
			return 0, false, err
		}
		if active := r.activeContext; active != nil && active.Err() != nil {
			return 0, false, err
		}
		r.brokenPixelOps[procedure] = true
		return 0, false, nil
	}
	if len(r.pixelOpResults) >= wipiPixelOpCacheLimit {
		clear(r.pixelOpResults)
	}
	r.pixelOpResults[key] = merged
	return merged, true, nil
}

func (r *Runtime) blendDevicePixel(destination, source, alpha uint32) uint32 {
	if alpha == 0 {
		return destination
	}
	if alpha >= 0xff {
		return source
	}
	inverse := uint32(0xff) - alpha
	sourceRed, sourceGreen, sourceBlue := r.rgbFromDevicePixel(source)
	destRed, destGreen, destBlue := r.rgbFromDevicePixel(destination)
	red := (sourceRed*alpha + destRed*inverse) / 0xff
	green := (sourceGreen*alpha + destGreen*inverse) / 0xff
	blue := (sourceBlue*alpha + destBlue*inverse) / 0xff
	return r.devicePixelFromRGB(red, green, blue)
}

func (r *Runtime) drawLine(handle uint32, x1, y1, x2, y2 int, context uint32) error {
	fb, ok := r.Framebuffers[handle]
	if !ok {
		return nil
	}
	graphicsContext, err := r.context(context)
	if err != nil {
		return err
	}
	dx := guest.Abs(x2 - x1)
	sx := -1
	if x1 < x2 {
		sx = 1
	}
	dy := -guest.Abs(y2 - y1)
	sy := -1
	if y1 < y2 {
		sy = 1
	}
	difference := dx + dy
	count := 0
	for {
		if graphicsContext.style == 0 || count&1 == 0 {
			if err := r.putPixelDecoded(fb, x1, y1, &graphicsContext, nil, 0xff); err != nil {
				return err
			}
		}
		if x1 == x2 && y1 == y2 {
			return nil
		}
		twice := difference * 2
		if twice >= dy {
			difference += dy
			x1 += sx
		}
		if twice <= dx {
			difference += dx
			y1 += sy
		}
		count++
	}
}

func (r *Runtime) drawRect(fill bool, args []uint32) error {
	handle := args[0]
	x, y := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	context := args[5]
	if width <= 0 || height <= 0 {
		return nil
	}
	fb, ok := r.Framebuffers[handle]
	if !ok || width > fb.Width*2 || height > fb.Height*2 {
		return nil
	}
	if fill {
		graphicsContext, err := r.context(context)
		if err != nil {
			return err
		}
		for row := 0; row < height; row++ {
			if err := r.compositeSpan(
				fb,
				x+graphicsContext.offsetX,
				y+row+graphicsContext.offsetY,
				width,
				&graphicsContext,
				nil,
				nil,
			); err != nil {
				return err
			}
		}
		return nil
	}
	for _, line := range [][4]int{
		{x, y, x + width - 1, y},
		{x, y + height - 1, x + width - 1, y + height - 1},
		{x, y, x, y + height - 1},
		{x + width - 1, y, x + width - 1, y + height - 1},
	} {
		if err := r.drawLine(handle, line[0], line[1], line[2], line[3], context); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) getDisplayInfo(display, pointer uint32) (guest.WIPIReturn, bool, error) {
	if pointer == 0 || display > 1 {
		return guest.WIPIReturn{Low: ^uint32(7)}, true, nil
	}
	bits := r.framebufferBits
	if bits != 16 {
		bits = 32
	}
	redMask, blueMask, greenMask := int32(0x00ff0000), int32(0x000000ff), int32(0x0000ff00)
	if bits == 16 {
		redMask, blueMask, greenMask = 0xf800, 0x001f, 0x07e0
	}
	values := [...]int32{
		int32(bits), int32(bits),
		int32(r.Frame.Bounds().Dx()),
		int32(r.Frame.Bounds().Dy()),
		int32(r.Frame.Bounds().Dx() * (bits / 8)),
		1,
		redMask,
		blueMask,
		greenMask,
	}
	var encoded [9 * 4]byte
	for index, value := range values {
		binary.LittleEndian.PutUint32(encoded[index*4:], uint32(value))
	}
	// LGT reports the display count here; the Samsung runtime reports
	// M_E_SUCCESS. See DisplayInfoReturnsCount.
	result := guest.WIPIReturn{}
	if r.DisplayInfoReturnsCount {
		result.Low = 1
	}
	return result, true, r.CPU.WriteMemory(pointer, encoded[:])
}

func (r *Runtime) stringWidth(font, address uint32, length int32, unicode bool) (guest.WIPIReturn, bool, error) {
	count := int(length)
	if length < 0 {
		if !unicode {
			value, err := r.ReadCString(address)
			if err != nil {
				return guest.WIPIReturn{}, true, err
			}
			count = len(value)
		} else {
			count = 0
			for count < int(maxWIPIString/2) {
				var encoded [2]byte
				if err := r.CPU.ReadMemory(address+uint32(count*2), encoded[:]); err != nil {
					return guest.WIPIReturn{}, true, err
				}
				if binary.LittleEndian.Uint16(encoded[:]) == 0 {
					break
				}
				count++
			}
		}
	}
	return guest.WIPIReturn{Low: uint32(count * max(1, guest.FontHeight(font)/2))}, true, nil
}

func (r *Runtime) getRGBPixels(args []uint32) error {
	fb, ok := r.Framebuffers[args[0]]
	if !ok {
		return nil
	}
	x, y := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	output, pitch := args[5], int(int32(args[6]))
	if width <= 0 || height <= 0 || width > fb.Width*2 || height > fb.Height*2 {
		return nil
	}
	if pitch <= 0 {
		pitch = width
	}
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			sourceX, sourceY := x+column, y+row
			if sourceX < 0 || sourceY < 0 || sourceX >= fb.Width || sourceY >= fb.Height {
				continue
			}
			value, err := r.framebufferPixel(fb, sourceX, sourceY)
			if err != nil {
				return err
			}
			red, green, blue := r.rgbFromDevicePixel(value)
			rgb := red<<16 | green<<8 | blue
			if err := r.WriteU32(
				output+uint32((row*pitch+column)*4),
				rgb,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runtime) setRGBPixels(args []uint32) error {
	fb, ok := r.Framebuffers[args[0]]
	if !ok {
		return nil
	}
	x, y := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	source, pitch, context := args[5], int(int32(args[6])), args[7]
	if width <= 0 || height <= 0 || width > fb.Width*2 || height > fb.Height*2 {
		return nil
	}
	if pitch <= 0 {
		pitch = width
	}
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			value, err := r.ReadU32(source + uint32((row*pitch+column)*4))
			if err != nil {
				return err
			}
			native := r.devicePixelFromRGB(value>>16, value>>8, value)
			if err := r.putPixel(
				fb.Handle,
				x+column,
				y+row,
				context,
				&native,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runtime) copyFramebuffer(args []uint32) error {
	return r.copyFramebufferAlpha(args, nil)
}

// copyFramebufferAlpha copies a rectangle between two framebuffers, leaving
// the destination untouched wherever alpha marks the source transparent. A nil
// alpha copies every pixel, which is what MC_grpCopyFrameBuffer and
// MC_grpCopyArea do.
func (r *Runtime) copyFramebufferAlpha(
	args []uint32,
	alpha *wipiImageAlpha,
) error {
	destination, ok := r.Framebuffers[args[0]]
	if !ok {
		return nil
	}
	dx, dy := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	source, ok := r.Framebuffers[args[5]]
	if !ok {
		return nil
	}
	sx, sy, context := int(int32(args[6])), int(int32(args[7])), args[8]
	if width <= 0 || height <= 0 ||
		width > max(source.Width, destination.Width)*2 ||
		height > max(source.Height, destination.Height)*2 {
		return nil
	}
	graphicsContext, err := r.context(context)
	if err != nil {
		return err
	}
	// Read the whole source rectangle before writing any of it: MC_grpCopyArea
	// scrolls a framebuffer over itself, so a row read after an earlier row was
	// written would copy the copy. A source pixel outside its framebuffer reads
	// as zero and is still drawn, as it always was.
	pixels := make([]uint32, max(0, width)*max(0, height))
	sourceBytesPerPixel := int(source.bytesPerPixel())
	firstColumn, endColumn := max(0, sx), min(source.Width, sx+width)
	if firstColumn < endColumn {
		rowBytes := make([]byte, (endColumn-firstColumn)*sourceBytesPerPixel)
		for row := 0; row < height; row++ {
			sourceY := sy + row
			if sourceY < 0 || sourceY >= source.Height {
				continue
			}
			address := source.Pixels +
				uint32(sourceY*source.Width+firstColumn)*uint32(sourceBytesPerPixel)
			if err := r.CPU.ReadMemory(address, rowBytes); err != nil {
				return err
			}
			values := pixels[row*width+(firstColumn-sx) : row*width+(endColumn-sx)]
			for index := range values {
				if sourceBytesPerPixel == 2 {
					values[index] = uint32(binary.LittleEndian.Uint16(rowBytes[index*2:]))
				} else {
					values[index] = binary.LittleEndian.Uint32(rowBytes[index*4:])
				}
			}
		}
	}
	var transparent []bool
	if alpha != nil && len(alpha.mask) != 0 {
		transparent = make([]bool, width)
	}
	for row := 0; row < height; row++ {
		if transparent != nil {
			for column := range transparent {
				transparent[column] = alpha.transparentAt(sx+column, sy+row)
			}
		}
		if err := r.compositeSpan(
			destination,
			dx+graphicsContext.offsetX,
			dy+row+graphicsContext.offsetY,
			width,
			&graphicsContext,
			pixels[row*width:(row+1)*width],
			transparent,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) copyArea(args []uint32) error {
	translated := []uint32{
		args[0], args[1], args[2], args[3], args[4],
		args[0], args[5], args[6], args[7],
	}
	return r.copyFramebuffer(translated)
}

func (r *Runtime) present(handle uint32) error {
	if handle == 0 {
		var err error
		handle, err = r.EnsureScreenFramebuffer()
		if err != nil {
			return err
		}
	}
	fb, ok := r.Framebuffers[handle]
	if !ok {
		return nil
	}
	// Some titles (e.g. 데몬헌터) double-buffer into an offscreen surface and call
	// MC_grpFlushLcd on that buffer directly. The presentation service only lets
	// the screen surface present, so redirect the flush onto the screen: copy the
	// flushed buffer's pixels into the screen framebuffer and present that. Normal
	// titles flush the screen handle itself and skip this branch entirely.
	if r.ScreenHandle != 0 && handle != r.ScreenHandle {
		if screen, ok := r.Framebuffers[r.ScreenHandle]; ok &&
			screen.Width == fb.Width && screen.Height == fb.Height &&
			screen.BitsPerPixel == fb.BitsPerPixel {
			size := uint64(fb.Width) * uint64(fb.Height) * uint64(fb.bytesPerPixel())
			if size <= uint64(^uint(0)>>1) {
				buffer := r.framebufferBuffer(int(size))
				if readErr := r.CPU.ReadMemory(fb.Pixels, buffer); readErr == nil {
					if writeErr := r.CPU.WriteMemory(screen.Pixels, buffer); writeErr == nil {
						handle = r.ScreenHandle
						fb = screen
					}
				}
			}
		}
	}
	serviceID, err := r.ensureSurface(handle)
	if err != nil {
		return err
	}
	if serviceID == 0 {
		return nil
	}
	if err := r.syncFramebufferToService(fb); err != nil {
		return err
	}
	if r.Services.Coordinator.PresentationOwner() != r.ServiceOwner {
		return nil
	}
	presentation, err := r.Services.Graphics.PresentCommit(
		r.ServiceOwner,
		serviceID,
		shared.Rectangle{},
	)
	if err != nil {
		return err
	}
	// A surface larger than the host frame can reach here: presenting a
	// non-screen framebuffer is only refused once a screen surface exists, and
	// a title may flush an offscreen buffer of its own size before creating
	// one. CopyLastFrameRGBA refuses a destination that cannot hold the frame,
	// so that case keeps the copy of the overlap it has always had.
	if int(presentation.Width) <= r.Frame.Bounds().Dx() &&
		int(presentation.Height) <= r.Frame.Bounds().Dy() {
		if err := r.Services.Graphics.CopyLastFrameRGBA(
			r.Frame.Pix,
			r.Frame.Stride,
		); err != nil {
			return err
		}
	} else {
		snapshot := r.Services.Graphics.LastFrame()
		width := min(int(snapshot.Width), r.Frame.Bounds().Dx())
		height := min(int(snapshot.Height), r.Frame.Bounds().Dy())
		for y := 0; y < height; y++ {
			source := y * int(snapshot.Width) * 4
			destination := y * r.Frame.Stride
			copy(
				r.Frame.Pix[destination:destination+width*4],
				snapshot.RGBA[source:source+width*4],
			)
		}
	}
	r.Stats.PresentCount++
	return nil
}

func (r *Runtime) syncFramebufferToService(fb Framebuffer) error {
	serviceID := r.surfaceServices[fb.Handle]
	if serviceID == 0 {
		return nil
	}
	size := uint64(fb.Width) * uint64(fb.Height) * uint64(fb.bytesPerPixel())
	if size > uint64(^uint(0)>>1) {
		return fmt.Errorf("WIPI framebuffer byte size exceeds host limit")
	}
	pixels := r.framebufferBuffer(int(size))
	if err := r.CPU.ReadMemory(fb.Pixels, pixels); err != nil {
		return err
	}
	return r.Services.Graphics.ReplacePixels(r.ServiceOwner, serviceID, pixels)
}

func (r *Runtime) framebufferBuffer(size int) []byte {
	if cap(r.framebufferScratch) < size {
		r.framebufferScratch = make([]byte, size)
	} else {
		r.framebufferScratch = r.framebufferScratch[:size]
	}
	return r.framebufferScratch
}

func (r *Runtime) syncFramebufferFromService(fb Framebuffer) error {
	serviceID := r.surfaceServices[fb.Handle]
	if serviceID == 0 {
		return nil
	}
	pixels, err := r.Services.Graphics.Pixels(r.ServiceOwner, serviceID)
	if err != nil {
		return err
	}
	return r.CPU.WriteMemory(fb.Pixels, pixels)
}

func modeledWIPIAPICount() int {
	return 31 + len(modeledKernelAPIs()) + 6 + len(modeledGraphicsAPIs()) +
		17 + 13 + 41 + 21 + 4 + 1 + 6 + 15 + 15
}

func modeledGraphicsAPIs() []string {
	return []string{
		"MC_grpGetImageProperty",
		"MC_grpGetImageFrameBuffer",
		"MC_grpGetScreenFrameBuffer",
		"MC_grpCreateOffScreenFrameBuffer",
		"MC_grpDestroyOffScreenFrameBuffer",
		"MC_grpInitContext",
		"MC_grpSetContext",
		"MC_grpGetContext",
		"MC_grpPutPixel",
		"MC_grpDrawLine",
		"MC_grpDrawImage",
		"MC_grpDrawRect",
		"MC_grpFillRect",
		"MC_grpDrawArc",
		"MC_grpFillArc",
		"MC_grpDrawString",
		"MC_grpDrawUnicodeString",
		"MC_grpGetPixelFromRGB",
		"MC_grpGetRGBFromPixel",
		"MC_grpGetDisplayInfo",
		"MC_grpGetFont",
		"MC_grpGetFontHeight",
		"MC_grpGetFontAscent",
		"MC_grpGetFontDescent",
		"MC_grpGetStringWidth",
		"MC_grpGetUnicodeStringWidth",
		"MC_grpGetRGBPixels",
		"MC_grpSetRGBPixels",
		"MC_grpCopyFrameBuffer",
		"MC_grpCopyArea",
		"MC_grpFlushLcd",
		"MC_grpRepaint",
		"MC_grpCreateImage",
		"MC_grpDestroyImage",
		"MC_grpDecodeNextImage",
		"MC_grpEncodeImage",
		"MC_grpPostEvent",
		"MC_grpDrawPolygon",
		"MC_grpDrawFillPolygon",
	}
}
