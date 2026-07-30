package application

import (
	"encoding/binary"
	"image/color"
	"net"
)

func (r *wipiRuntime) dispatchUtility(name string) (wipiReturn, bool, error) {
	args, err := r.args(2)
	if err != nil {
		return wipiReturn{}, true, err
	}
	switch name {
	case "MC_utilHtonl", "MC_utilNtohl":
		return wipiReturn{low: reverse32(args[0])}, true, nil
	case "MC_utilHtons", "MC_utilNtohs":
		return wipiReturn{low: uint32(reverse16(uint16(args[0])))}, true, nil
	case "MC_utilInetAddrInt":
		value, err := r.readCString(args[0])
		if err != nil {
			return wipiReturn{}, true, err
		}
		ip := net.ParseIP(string(value)).To4()
		if ip == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		return wipiReturn{low: binary.BigEndian.Uint32(ip)}, true, nil
	case "MC_utilInetAddrStr":
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], args[0])
		_, err := r.writeCString(args[1], []byte(net.IP(encoded[:]).String()), -1)
		return wipiReturn{}, true, err
	default:
		return wipiReturn{}, false, nil
	}
}

func (r *wipiRuntime) dispatchGraphics(name string) (wipiReturn, bool, error) {
	count, modeled := graphicsArgumentCount(name)
	if !modeled {
		return wipiReturn{}, false, nil
	}
	values, err := r.args(count)
	if err != nil {
		return wipiReturn{}, true, err
	}
	args := make([]uint32, 9)
	copy(args, values)
	switch name {
	case "MC_grpGetImageProperty":
		value, err := r.imageProperty(args[0], int32(args[1]))
		return wipiReturn{low: uint32(value)}, true, err
	case "MC_grpGetImageFrameBuffer":
		image, ok, err := r.readImage(args[0])
		if err != nil || !ok {
			return wipiReturn{}, true, err
		}
		return wipiReturn{low: image.framebuffer}, true, nil
	case "MC_grpGetScreenFrameBuffer":
		if int32(args[0]) < 0 || args[0] > 1 {
			return wipiReturn{}, true, nil
		}
		handle, err := r.ensureScreenFramebuffer()
		return wipiReturn{low: handle}, true, err
	case "MC_grpCreateOffScreenFrameBuffer":
		handle, err := r.newFramebuffer(int(int32(args[0])), int(int32(args[1])), true)
		return wipiReturn{low: handle}, true, err
	case "MC_grpDestroyOffScreenFrameBuffer":
		fb, ok := r.framebuffers[args[0]]
		if ok && fb.handle != r.screenHandle {
			r.heap.release(fb.pixels)
			r.heap.release(fb.handle)
			delete(r.framebuffers, args[0])
		}
		return wipiReturn{}, true, nil
	case "MC_grpInitContext":
		return wipiReturn{}, true, r.initializeGraphicsContext(args[0])
	case "MC_grpSetContext", "MC_grpGetContext":
		return wipiReturn{}, true, r.transferGraphicsContext(name, args[0], int32(args[1]), args[2])
	case "MC_grpPutPixel":
		return wipiReturn{}, true, r.putPixel(args[0], int(int32(args[1])), int(int32(args[2])), args[3], nil)
	case "MC_grpDrawLine":
		return wipiReturn{}, true, r.drawLine(
			args[0],
			int(int32(args[1])),
			int(int32(args[2])),
			int(int32(args[3])),
			int(int32(args[4])),
			args[5],
		)
	case "MC_grpDrawImage":
		return wipiReturn{}, true, r.drawImage(args)
	case "MC_grpDrawRect", "MC_grpFillRect":
		return wipiReturn{}, true, r.drawRect(name == "MC_grpFillRect", args)
	case "MC_grpDrawArc", "MC_grpFillArc":
		return wipiReturn{}, true, r.drawArc(name == "MC_grpFillArc", args)
	case "MC_grpDrawString", "MC_grpDrawUnicodeString":
		return wipiReturn{}, true, r.drawText(name == "MC_grpDrawUnicodeString", args)
	case "MC_grpGetPixelFromRGB":
		return wipiReturn{
			low: r.pixelFromRGB(args[0], args[1], args[2]),
		}, true, nil
	case "MC_grpGetRGBFromPixel":
		red, green, blue := r.rgbFromPixel(args[0])
		for index, component := range []uint32{red, green, blue} {
			if args[index+1] != 0 {
				if err := r.writeU32(args[index+1], component); err != nil {
					return wipiReturn{}, true, err
				}
			}
		}
		return wipiReturn{low: args[0]}, true, nil
	case "MC_grpGetDisplayInfo":
		return r.getDisplayInfo(args[0], args[1])
	case "MC_grpGetFont":
		return wipiReturn{low: args[0]&0xe0 | args[2]<<8 | args[1]&0x1f}, true, nil
	case "MC_grpGetFontHeight", "MC_grpGetFontAscent", "MC_grpGetFontDescent":
		height := fontHeight(args[0])
		switch name {
		case "MC_grpGetFontAscent":
			height -= height / 4
		case "MC_grpGetFontDescent":
			height /= 4
		}
		return wipiReturn{low: uint32(height)}, true, nil
	case "MC_grpGetStringWidth":
		return r.stringWidth(args[0], args[1], int32(args[2]), false)
	case "MC_grpGetUnicodeStringWidth":
		return r.stringWidth(args[0], args[1], int32(args[2]), true)
	case "MC_grpGetRGBPixels":
		return wipiReturn{}, true, r.getRGBPixels(args)
	case "MC_grpSetRGBPixels":
		return wipiReturn{}, true, r.setRGBPixels(args)
	case "MC_grpCopyFrameBuffer":
		return wipiReturn{}, true, r.copyFramebuffer(args)
	case "MC_grpCopyArea":
		return wipiReturn{}, true, r.copyArea(args)
	case "MC_grpFlushLcd":
		return wipiReturn{}, true, r.present(args[1])
	case "MC_grpRepaint":
		return wipiReturn{}, true, r.present(r.screenHandle)
	case "MC_grpCreateImage":
		result, err := r.createImage(args[0], args[1], int32(args[2]), int32(args[3]))
		return wipiReturn{low: uint32(result)}, true, err
	case "MC_grpDestroyImage":
		return wipiReturn{}, true, r.destroyImage(args[0])
	case "MC_grpDecodeNextImage":
		result, err := r.decodeNextImage(args[0])
		return wipiReturn{low: uint32(result)}, true, err
	case "MC_grpEncodeImage":
		result, err := r.encodeImage(args)
		return wipiReturn{low: result}, true, err
	case "MC_grpPostEvent":
		if len(r.graphicsEvents) >= wipiMaxGraphicsEvents {
			return wipiReturn{low: wipiReturnCode(wipiNoMemory)}, true, nil
		}
		r.graphicsEvents = append(r.graphicsEvents, wipiGraphicsEvent{
			id:     int32(args[0]),
			kind:   int32(args[1]),
			param1: int32(args[2]),
			param2: int32(args[3]),
		})
		return wipiReturn{}, true, nil
	case "MC_grpDrawPolygon", "MC_grpDrawFillPolygon":
		return wipiReturn{}, true, r.drawPolygon(name == "MC_grpDrawFillPolygon", args)
	default:
		return wipiReturn{}, false, nil
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

func (r *wipiRuntime) ensureScreenFramebuffer() (uint32, error) {
	if r.screenHandle != 0 {
		return r.screenHandle, nil
	}
	handle, err := r.newFramebuffer(r.frame.Bounds().Dx(), r.frame.Bounds().Dy(), false)
	if err != nil {
		return 0, err
	}
	r.screenHandle = handle
	r.screenPixels = r.framebuffers[handle].pixels
	return handle, nil
}

func (r *wipiRuntime) newFramebuffer(width, height int, owns bool) (uint32, error) {
	if width <= 0 || height <= 0 || width > 4096 || height > 4096 {
		return 0, nil
	}
	bytesPerPixel := r.framebufferBits / 8
	if bytesPerPixel != 2 && bytesPerPixel != 4 {
		bytesPerPixel = 4
	}
	pixelBytes := uint64(width) * uint64(height) * uint64(bytesPerPixel)
	if pixelBytes > uint64(guestHeapSize) {
		return 0, nil
	}
	pixels, err := r.heap.allocate(uint32(pixelBytes), true)
	if err != nil || pixels == 0 {
		return 0, err
	}
	handle, err := r.heap.allocate(24, true)
	if err != nil || handle == 0 {
		r.heap.release(pixels)
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
	if err := r.cpu.WriteMemory(handle, descriptor[:]); err != nil {
		return 0, err
	}
	r.framebuffers[handle] = wipiFramebuffer{
		handle:       handle,
		pixels:       pixels,
		width:        width,
		height:       height,
		bitsPerPixel: bytesPerPixel * 8,
		owns:         owns,
	}
	return handle, nil
}

func (r *wipiRuntime) initializeGraphicsContext(address uint32) error {
	if address == 0 {
		return nil
	}
	values := [...]uint32{
		0, 0, 0x7fffffff, 0x7fffffff, 0,
		0, 0xffffff, 255, 0, 0, 0, 0, 0, 0, 0,
	}
	var encoded [15 * 4]byte
	for index, value := range values {
		binary.LittleEndian.PutUint32(encoded[index*4:], value)
	}
	return r.cpu.WriteMemory(address, encoded[:])
}

func (r *wipiRuntime) transferGraphicsContext(name string, context uint32, index int32, pointer uint32) error {
	offsets := map[int32]struct {
		offset uint32
		size   uint32
	}{
		0: {0, 16}, 1: {20, 4}, 2: {24, 4}, 4: {28, 4}, 5: {32, 4},
		6: {36, 4}, 7: {40, 4}, 8: {44, 4}, 9: {48, 4}, 10: {52, 8},
	}
	field, ok := offsets[index]
	if context == 0 || !ok {
		return nil
	}
	data := make([]byte, field.size)
	if name == "MC_grpSetContext" {
		if field.size == 4 {
			binary.LittleEndian.PutUint32(data, pointer)
			if pointer != 0 {
				indirect := make([]byte, field.size)
				if err := r.cpu.ReadMemory(pointer, indirect); err == nil {
					data = indirect
				}
			}
		} else {
			if pointer == 0 {
				return nil
			}
			if err := r.cpu.ReadMemory(pointer, data); err != nil {
				return err
			}
		}
		if err := r.cpu.WriteMemory(context+field.offset, data); err != nil {
			return err
		}
		if index == 0 {
			return r.writeU32(context+16, 1)
		}
		return nil
	}
	if pointer == 0 {
		return nil
	}
	if err := r.cpu.ReadMemory(context+field.offset, data); err != nil {
		return err
	}
	return r.cpu.WriteMemory(pointer, data)
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

func (r *wipiRuntime) context(address uint32) (wipiGraphicsContext, error) {
	if address == 0 {
		return wipiGraphicsContext{
			right:      int(^uint32(0) >> 1),
			bottom:     int(^uint32(0) >> 1),
			background: 0xffffff,
			alpha:      255,
		}, nil
	}
	var encoded [60]byte
	if err := r.cpu.ReadMemory(address, encoded[:]); err != nil {
		return wipiGraphicsContext{}, err
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

func (fb wipiFramebuffer) bytesPerPixel() uint32 {
	if fb.bitsPerPixel == 16 {
		return 2
	}
	return 4
}

func (r *wipiRuntime) framebufferPixel(
	fb wipiFramebuffer,
	x, y int,
) (uint32, error) {
	address := fb.pixels +
		uint32(y*fb.width+x)*fb.bytesPerPixel()
	if fb.bitsPerPixel == 16 {
		var encoded [2]byte
		if err := r.cpu.ReadMemory(address, encoded[:]); err != nil {
			return 0, err
		}
		return uint32(binary.LittleEndian.Uint16(encoded[:])), nil
	}
	return r.readU32(address)
}

func (r *wipiRuntime) writeFramebufferPixel(
	fb wipiFramebuffer,
	x, y int,
	pixel uint32,
) error {
	address := fb.pixels +
		uint32(y*fb.width+x)*fb.bytesPerPixel()
	if fb.bitsPerPixel == 16 {
		var encoded [2]byte
		binary.LittleEndian.PutUint16(encoded[:], uint16(pixel))
		return r.cpu.WriteMemory(address, encoded[:])
	}
	return r.writeU32(address, pixel&0xffffff)
}

func (r *wipiRuntime) pixelFromRGB(red, green, blue uint32) uint32 {
	red &= 0xff
	green &= 0xff
	blue &= 0xff
	if r.framebufferBits == 16 {
		return red>>3<<11 | green>>2<<5 | blue>>3
	}
	return red<<16 | green<<8 | blue
}

func (r *wipiRuntime) rgbFromPixel(pixel uint32) (uint32, uint32, uint32) {
	if r.framebufferBits == 16 {
		red := pixel >> 11 & 0x1f
		green := pixel >> 5 & 0x3f
		blue := pixel & 0x1f
		return red<<3 | red>>2, green<<2 | green>>4, blue<<3 | blue>>2
	}
	return pixel >> 16 & 0xff, pixel >> 8 & 0xff, pixel & 0xff
}

func (r *wipiRuntime) putPixel(handle uint32, x, y int, contextAddress uint32, override *uint32) error {
	fb, ok := r.framebuffers[handle]
	if !ok {
		return nil
	}
	context, err := r.context(contextAddress)
	if err != nil {
		return err
	}
	x += context.offsetX
	y += context.offsetY
	if x < 0 || y < 0 || x >= fb.width || y >= fb.height {
		return nil
	}
	if context.clipEnabled &&
		!(x >= context.left && x < context.right && y >= context.top && y < context.bottom) {
		return nil
	}
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
		foreground, err = r.callGuestFunction(
			context.pixelOperation,
			foreground,
			destination,
			uint32(context.pixelParameter),
		)
		if err != nil {
			return err
		}
	case context.xor:
		foreground = destination ^ foreground
	case context.alpha <= 0:
		foreground = destination
	case context.alpha < 255:
		alpha := uint32(context.alpha)
		inverse := uint32(255) - alpha
		sourceRed, sourceGreen, sourceBlue := r.rgbFromPixel(foreground)
		destRed, destGreen, destBlue := r.rgbFromPixel(destination)
		red := (sourceRed*alpha + destRed*inverse) / 255
		green := (sourceGreen*alpha + destGreen*inverse) / 255
		blue := (sourceBlue*alpha + destBlue*inverse) / 255
		foreground = r.pixelFromRGB(red, green, blue)
	}
	return r.writeFramebufferPixel(fb, x, y, foreground)
}

func (r *wipiRuntime) drawLine(handle uint32, x1, y1, x2, y2 int, context uint32) error {
	graphicsContext, err := r.context(context)
	if err != nil {
		return err
	}
	dx := abs(x2 - x1)
	sx := -1
	if x1 < x2 {
		sx = 1
	}
	dy := -abs(y2 - y1)
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

func (r *wipiRuntime) drawRect(fill bool, args []uint32) error {
	handle := args[0]
	x, y := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	context := args[5]
	if width <= 0 || height <= 0 {
		return nil
	}
	fb, ok := r.framebuffers[handle]
	if !ok || width > fb.width*2 || height > fb.height*2 {
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

func (r *wipiRuntime) getDisplayInfo(display, pointer uint32) (wipiReturn, bool, error) {
	if pointer == 0 || display > 1 {
		return wipiReturn{low: ^uint32(7)}, true, nil
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
		int32(r.frame.Bounds().Dx()),
		int32(r.frame.Bounds().Dy()),
		int32(r.frame.Bounds().Dx() * (bits / 8)),
		1,
		redMask,
		blueMask,
		greenMask,
	}
	var encoded [9 * 4]byte
	for index, value := range values {
		binary.LittleEndian.PutUint32(encoded[index*4:], uint32(value))
	}
	return wipiReturn{}, true, r.cpu.WriteMemory(pointer, encoded[:])
}

func (r *wipiRuntime) stringWidth(font, address uint32, length int32, unicode bool) (wipiReturn, bool, error) {
	count := int(length)
	if length < 0 {
		if !unicode {
			value, err := r.readCString(address)
			if err != nil {
				return wipiReturn{}, true, err
			}
			count = len(value)
		} else {
			count = 0
			for count < int(maxWIPIString/2) {
				var encoded [2]byte
				if err := r.cpu.ReadMemory(address+uint32(count*2), encoded[:]); err != nil {
					return wipiReturn{}, true, err
				}
				if binary.LittleEndian.Uint16(encoded[:]) == 0 {
					break
				}
				count++
			}
		}
	}
	return wipiReturn{low: uint32(count * max(1, fontHeight(font)/2))}, true, nil
}

func (r *wipiRuntime) getRGBPixels(args []uint32) error {
	fb, ok := r.framebuffers[args[0]]
	if !ok {
		return nil
	}
	x, y := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	output, pitch := args[5], int(int32(args[6]))
	if width <= 0 || height <= 0 || width > fb.width*2 || height > fb.height*2 {
		return nil
	}
	if pitch <= 0 {
		pitch = width
	}
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			sourceX, sourceY := x+column, y+row
			if sourceX < 0 || sourceY < 0 || sourceX >= fb.width || sourceY >= fb.height {
				continue
			}
			value, err := r.framebufferPixel(fb, sourceX, sourceY)
			if err != nil {
				return err
			}
			red, green, blue := r.rgbFromPixel(value)
			rgb := red<<16 | green<<8 | blue
			if err := r.writeU32(
				output+uint32((row*pitch+column)*4),
				rgb,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *wipiRuntime) setRGBPixels(args []uint32) error {
	fb, ok := r.framebuffers[args[0]]
	if !ok {
		return nil
	}
	x, y := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	source, pitch, context := args[5], int(int32(args[6])), args[7]
	if width <= 0 || height <= 0 || width > fb.width*2 || height > fb.height*2 {
		return nil
	}
	if pitch <= 0 {
		pitch = width
	}
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			value, err := r.readU32(source + uint32((row*pitch+column)*4))
			if err != nil {
				return err
			}
			native := r.pixelFromRGB(value>>16, value>>8, value)
			if err := r.putPixel(
				fb.handle,
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

func (r *wipiRuntime) copyFramebuffer(args []uint32) error {
	destination, ok := r.framebuffers[args[0]]
	if !ok {
		return nil
	}
	dx, dy := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	source, ok := r.framebuffers[args[5]]
	if !ok {
		return nil
	}
	sx, sy, context := int(int32(args[6])), int(int32(args[7])), args[8]
	if width <= 0 || height <= 0 ||
		width > max(source.width, destination.width)*2 ||
		height > max(source.height, destination.height)*2 {
		return nil
	}
	pixels := make([]uint32, max(0, width)*max(0, height))
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			sourceX, sourceY := sx+column, sy+row
			if sourceX < 0 || sourceY < 0 || sourceX >= source.width || sourceY >= source.height {
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
			value := pixels[row*width+column]
			if err := r.putPixel(destination.handle, dx+column, dy+row, context, &value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *wipiRuntime) copyArea(args []uint32) error {
	translated := []uint32{
		args[0], args[1], args[2], args[3], args[4],
		args[0], args[5], args[6], args[7],
	}
	return r.copyFramebuffer(translated)
}

func (r *wipiRuntime) present(handle uint32) error {
	if handle == 0 {
		var err error
		handle, err = r.ensureScreenFramebuffer()
		if err != nil {
			return err
		}
	}
	fb, ok := r.framebuffers[handle]
	if !ok {
		return nil
	}
	for y := 0; y < min(fb.height, r.frame.Bounds().Dy()); y++ {
		for x := 0; x < min(fb.width, r.frame.Bounds().Dx()); x++ {
			pixel, err := r.framebufferPixel(fb, x, y)
			if err != nil {
				return err
			}
			red, green, blue := r.rgbFromPixel(pixel)
			r.frame.SetRGBA(x, y, color.RGBA{
				R: uint8(red),
				G: uint8(green),
				B: uint8(blue),
				A: 0xff,
			})
		}
	}
	r.stats.PresentCount++
	return nil
}

func fontHeight(font uint32) int {
	switch font & 0x1f {
	case 8:
		return 10
	case 16:
		return 18
	default:
		return 14
	}
}

func reverse16(value uint16) uint16 {
	return value>>8 | value<<8
}

func reverse32(value uint32) uint32 {
	return uint32(reverse16(uint16(value)))<<16 | uint32(reverse16(uint16(value>>16)))
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
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
