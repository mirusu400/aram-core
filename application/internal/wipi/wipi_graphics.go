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
	format := shared.PixelBGRX8888
	if bytesPerPixel == 2 {
		format = shared.PixelRGB565
	}
	serviceID, err := r.Services.Graphics.CreateSurface(
		r.ServiceOwner,
		shared.SurfaceDescriptor{
			Width:  int32(width),
			Height: int32(height),
			Stride: int32(width * bytesPerPixel),
			Format: format,
		},
	)
	if err != nil {
		delete(r.Framebuffers, handle)
		r.Heap.Release(handle)
		r.Heap.Release(pixels)
		return 0, err
	}
	r.surfaceServices[handle] = serviceID
	if !owns {
		if err := r.Services.Graphics.SetScreen(r.ServiceOwner, serviceID); err != nil {
			_ = r.Services.Graphics.DestroySurface(r.ServiceOwner, serviceID)
			delete(r.surfaceServices, handle)
			delete(r.Framebuffers, handle)
			r.Heap.Release(handle)
			r.Heap.Release(pixels)
			return 0, err
		}
	}
	return handle, nil
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
	if fb.BitsPerPixel == 16 {
		var encoded [2]byte
		if err := r.CPU.ReadMemory(address, encoded[:]); err != nil {
			return 0, err
		}
		return uint32(binary.LittleEndian.Uint16(encoded[:])), nil
	}
	return r.ReadU32(address)
}

func (r *Runtime) writeFramebufferPixel(
	fb Framebuffer,
	x, y int,
	pixel uint32,
) error {
	address := fb.Pixels +
		uint32(y*fb.Width+x)*fb.bytesPerPixel()
	if fb.BitsPerPixel == 16 {
		var encoded [2]byte
		binary.LittleEndian.PutUint16(encoded[:], uint16(pixel))
		if err := r.CPU.WriteMemory(address, encoded[:]); err != nil {
			return err
		}
		if serviceID := r.surfaceServices[fb.Handle]; serviceID != 0 {
			offset := uint64((y*fb.Width + x) * 2)
			return r.Services.Graphics.WritePixelBytes(
				r.ServiceOwner,
				serviceID,
				offset,
				encoded[:],
			)
		}
		return nil
	}
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], pixel&0xffffff)
	if err := r.CPU.WriteMemory(address, encoded[:]); err != nil {
		return err
	}
	if serviceID := r.surfaceServices[fb.Handle]; serviceID != 0 {
		offset := uint64((y*fb.Width + x) * 4)
		return r.Services.Graphics.WritePixelBytes(
			r.ServiceOwner,
			serviceID,
			offset,
			encoded[:],
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
	switch {
	case context.pixelOperation != 0:
		foreground, err = r.CallGuestFunction(
			context.pixelOperation,
			foreground,
			destination,
			uint32(context.pixelParameter),
		)
		if err != nil {
			return err
		}
		if coverage < 0xff {
			foreground = r.blendDevicePixel(destination, foreground, uint32(coverage))
		}
	case context.xor:
		foreground = destination ^ foreground
		if coverage < 0xff {
			foreground = r.blendDevicePixel(destination, foreground, uint32(coverage))
		}
	default:
		alpha := uint32(0)
		if context.alpha > 0 {
			alpha = uint32(context.alpha)
			if alpha > 0xff {
				alpha = 0xff
			}
			alpha = alpha * uint32(coverage) / 0xff
		}
		foreground = r.blendDevicePixel(destination, foreground, alpha)
	}
	return r.writeFramebufferPixel(fb, x, y, foreground)
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
			if err := r.putPixel(handle, x1, y1, context, nil); err != nil {
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
		for row := 0; row < height; row++ {
			for column := 0; column < width; column++ {
				if err := r.putPixel(handle, x+column, y+row, context, nil); err != nil {
					return err
				}
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
	return guest.WIPIReturn{}, true, r.CPU.WriteMemory(pointer, encoded[:])
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
	pixels := make([]uint32, max(0, width)*max(0, height))
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			sourceX, sourceY := sx+column, sy+row
			if sourceX < 0 || sourceY < 0 || sourceX >= source.Width || sourceY >= source.Height {
				continue
			}
			value, err := r.framebufferPixel(source, sourceX, sourceY)
			if err != nil {
				return err
			}
			pixels[row*width+column] = value
		}
	}
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			if alpha.transparentAt(sx+column, sy+row) {
				continue
			}
			value := pixels[row*width+column]
			if err := r.putPixel(destination.Handle, dx+column, dy+row, context, &value); err != nil {
				return err
			}
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
	serviceID := r.surfaceServices[handle]
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
