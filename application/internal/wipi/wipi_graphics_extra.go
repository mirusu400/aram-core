package wipi

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"sort"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	wipiImageMagic          = uint32(0x474d4957) // "WIMG" in guest memory.
	wipiImageDescriptorSize = uint32(40)
)

type wipiImageDescriptor struct {
	framebuffer uint32
	bufferID    uint32
	offset      uint32
	length      uint32
	frameIndex  uint32
	frameCount  uint32
	animated    bool
	delayMS     int32
	loopCount   int32
}

func (r *Runtime) readImage(handle uint32) (wipiImageDescriptor, bool, error) {
	if handle == 0 || r.Heap.Allocations[handle] < wipiImageDescriptorSize {
		return wipiImageDescriptor{}, false, nil
	}
	var encoded [wipiImageDescriptorSize]byte
	if err := r.CPU.ReadMemory(handle, encoded[:]); err != nil {
		return wipiImageDescriptor{}, false, err
	}
	if binary.LittleEndian.Uint32(encoded[0:4]) != wipiImageMagic {
		return wipiImageDescriptor{}, false, nil
	}
	descriptor := wipiImageDescriptor{
		framebuffer: binary.LittleEndian.Uint32(encoded[4:8]),
		bufferID:    binary.LittleEndian.Uint32(encoded[8:12]),
		offset:      binary.LittleEndian.Uint32(encoded[12:16]),
		length:      binary.LittleEndian.Uint32(encoded[16:20]),
		frameIndex:  binary.LittleEndian.Uint32(encoded[20:24]),
		frameCount:  binary.LittleEndian.Uint32(encoded[24:28]),
		animated:    binary.LittleEndian.Uint32(encoded[28:32]) != 0,
		delayMS:     int32(binary.LittleEndian.Uint32(encoded[32:36])),
		loopCount:   int32(binary.LittleEndian.Uint32(encoded[36:40])),
	}
	if _, ok := r.Framebuffers[descriptor.framebuffer]; !ok {
		return wipiImageDescriptor{}, false, nil
	}
	if descriptor.frameCount == 0 || descriptor.frameIndex >= descriptor.frameCount {
		return wipiImageDescriptor{}, false, nil
	}
	if descriptor.bufferID != 0 {
		size, ok := r.Heap.Allocations[descriptor.bufferID]
		end := uint64(descriptor.offset) + uint64(descriptor.length)
		if !ok || descriptor.length == 0 || end > uint64(size) {
			return wipiImageDescriptor{}, false, nil
		}
	}
	return descriptor, true, nil
}

func (r *Runtime) writeImage(handle uint32, descriptor wipiImageDescriptor) error {
	var encoded [wipiImageDescriptorSize]byte
	values := [...]uint32{
		wipiImageMagic,
		descriptor.framebuffer,
		descriptor.bufferID,
		descriptor.offset,
		descriptor.length,
		descriptor.frameIndex,
		descriptor.frameCount,
		0,
		uint32(descriptor.delayMS),
		uint32(descriptor.loopCount),
	}
	if descriptor.animated {
		values[7] = 1
	}
	for index, value := range values {
		binary.LittleEndian.PutUint32(encoded[index*4:], value)
	}
	return r.CPU.WriteMemory(handle, encoded[:])
}

func (r *Runtime) imageProperty(handle uint32, property int32) (int32, error) {
	descriptor, ok, err := r.readImage(handle)
	if err != nil || !ok {
		return 0, err
	}
	framebuffer := r.Framebuffers[descriptor.framebuffer]
	switch property {
	case 1:
		if descriptor.animated {
			return 1, nil
		}
	case 2:
		return descriptor.delayMS, nil
	case 3:
		return descriptor.loopCount, nil
	case 4:
		return int32(framebuffer.Width), nil
	case 5:
		return int32(framebuffer.Height), nil
	case 6:
		return int32(framebuffer.BitsPerPixel), nil
	}
	return 0, nil
}

func (r *Runtime) createImage(
	output uint32,
	bufferID uint32,
	offset int32,
	length int32,
) (int32, error) {
	if output == 0 {
		return guest.WIPIInvalid, nil
	}
	if err := r.WriteU32(output, 0); err != nil {
		return guest.WIPIInvalid, err
	}
	size, allocated := r.Heap.Allocations[bufferID]
	if !allocated || offset < 0 || length <= 0 ||
		uint64(uint32(offset))+uint64(uint32(length)) > uint64(size) {
		return guest.WIPIBadFormat, nil
	}
	data := make([]byte, int(length))
	if err := r.CPU.ReadMemory(bufferID+uint32(offset), data); err != nil {
		return guest.WIPIBadFormat, err
	}
	assetID, err := r.Services.Assets.Decode(
		r.ServiceOwner,
		data,
		shared.DecodeOptions{},
	)
	if err != nil {
		return guest.WIPIBadFormat, nil
	}
	releaseAsset := true
	defer func() {
		if releaseAsset {
			_ = r.Services.Assets.Release(r.ServiceOwner, assetID)
		}
	}()
	info, err := r.Services.Assets.Info(r.ServiceOwner, assetID)
	if err != nil || len(info.Frames) == 0 {
		return guest.WIPIBadFormat, err
	}
	framebuffer, err := r.newFramebuffer(int(info.Width), int(info.Height), true)
	if err != nil {
		return guest.WIPINoMemory, err
	}
	if framebuffer == 0 {
		return guest.WIPINoMemory, nil
	}
	cleanupFramebuffer := true
	defer func() {
		if cleanupFramebuffer {
			r.destroyOwnedFramebuffer(framebuffer)
		}
	}()
	if err := r.paintServiceImageFrame(framebuffer, info.Frames[0].Surface, true); err != nil {
		return guest.WIPIBadFormat, err
	}
	handle, err := r.Heap.Allocate(wipiImageDescriptorSize, true)
	if err != nil {
		return guest.WIPINoMemory, err
	}
	if handle == 0 {
		return guest.WIPINoMemory, nil
	}
	descriptor := wipiImageDescriptor{
		framebuffer: framebuffer,
		frameIndex:  0,
		frameCount:  uint32(len(info.Frames)),
		animated:    len(info.Frames) > 1,
		delayMS:     durationMillis(info.Frames[0].Delay),
		loopCount:   info.LoopCount,
	}
	if descriptor.animated {
		descriptor.bufferID = bufferID
		descriptor.offset = uint32(offset)
		descriptor.length = uint32(length)
	}
	if err := r.writeImage(handle, descriptor); err != nil {
		r.Heap.Release(handle)
		return guest.WIPINoMemory, err
	}
	if err := r.WriteU32(output, handle); err != nil {
		r.Heap.Release(handle)
		return guest.WIPIInvalid, err
	}
	if !descriptor.animated {
		r.Heap.Release(bufferID)
	}
	r.assetServices[handle] = assetID
	releaseAsset = false
	cleanupFramebuffer = false
	return guest.WIPIImageDone, nil
}

func (r *Runtime) destroyImage(handle uint32) error {
	descriptor, ok, err := r.readImage(handle)
	if err != nil || !ok {
		return err
	}
	if assetID := r.assetServices[handle]; assetID != 0 {
		if err := r.Services.Assets.Release(r.ServiceOwner, assetID); err != nil {
			return err
		}
		delete(r.assetServices, handle)
	}
	r.releaseImageAlpha(handle)
	r.destroyOwnedFramebuffer(descriptor.framebuffer)
	if descriptor.bufferID != 0 {
		r.Heap.Release(descriptor.bufferID)
	}
	r.Heap.Release(handle)
	return nil
}

func (r *Runtime) destroyOwnedFramebuffer(handle uint32) {
	framebuffer, ok := r.Framebuffers[handle]
	if !ok || handle == r.ScreenHandle || !framebuffer.owns {
		return
	}
	if serviceID := r.surfaceServices[handle]; serviceID != 0 {
		if err := r.Services.Graphics.DestroySurface(r.ServiceOwner, serviceID); err != nil {
			return
		}
		delete(r.surfaceServices, handle)
	}
	r.Heap.Release(framebuffer.Pixels)
	r.Heap.Release(framebuffer.Handle)
	delete(r.Framebuffers, handle)
}

func (r *Runtime) decodeNextImage(handle uint32) (int32, error) {
	descriptor, ok, err := r.readImage(handle)
	if err != nil {
		return guest.WIPIBadFormat, err
	}
	if !ok {
		return guest.WIPIInvalid, nil
	}
	if !descriptor.animated || descriptor.frameCount <= 1 ||
		descriptor.frameIndex+1 >= descriptor.frameCount {
		return guest.WIPIImageDone, nil
	}
	nextIndex := descriptor.frameIndex + 1
	assetID := r.assetServices[handle]
	if assetID == 0 {
		return guest.WIPIBadFormat, nil
	}
	info, err := r.Services.Assets.Info(r.ServiceOwner, assetID)
	if err != nil || nextIndex >= uint32(len(info.Frames)) {
		return guest.WIPIBadFormat, err
	}
	frame := info.Frames[nextIndex]
	if err := r.paintServiceImageFrame(descriptor.framebuffer, frame.Surface, true); err != nil {
		return guest.WIPIBadFormat, err
	}
	descriptor.frameIndex = nextIndex
	descriptor.delayMS = durationMillis(frame.Delay)
	if err := r.writeImage(handle, descriptor); err != nil {
		return guest.WIPIBadFormat, err
	}
	if nextIndex+1 >= descriptor.frameCount {
		return guest.WIPIImageDone, nil
	}
	return guest.WIPIImageFrameDone, nil
}

func durationMillis(value time.Duration) int32 {
	milliseconds := value / time.Millisecond
	if milliseconds > time.Duration(^uint32(0)>>1) {
		return int32(^uint32(0) >> 1)
	}
	return int32(milliseconds)
}

func (r *Runtime) paintServiceImageFrame(
	handle uint32,
	surface shared.ServiceID,
	clear bool,
) error {
	framebuffer, ok := r.Framebuffers[handle]
	if !ok {
		return nil
	}
	descriptor, err := r.Services.Graphics.Descriptor(r.ServiceOwner, surface)
	if err != nil {
		return err
	}
	pixels, err := r.Services.Graphics.RGBA(r.ServiceOwner, surface)
	if err != nil {
		return err
	}
	if clear {
		size := uint32(framebuffer.Width*framebuffer.Height) *
			framebuffer.bytesPerPixel()
		if err := guest.ZeroMemory(r.CPU, framebuffer.Pixels, size); err != nil {
			return err
		}
		if serviceID := r.surfaceServices[handle]; serviceID != 0 {
			if err := r.Services.Graphics.ReplacePixels(
				r.ServiceOwner,
				serviceID,
				make([]byte, size),
			); err != nil {
				return err
			}
		}
	}
	width := min(framebuffer.Width, int(descriptor.Width))
	height := min(framebuffer.Height, int(descriptor.Height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := (y*int(descriptor.Width) + x) * 4
			pixel := r.devicePixelFromRGB(
				uint32(pixels[offset]),
				uint32(pixels[offset+1]),
				uint32(pixels[offset+2]),
			)
			if err := r.writeFramebufferPixel(framebuffer, x, y, pixel); err != nil {
				return err
			}
		}
	}
	return nil
}

func validWIPIImageSize(width, height int) bool {
	if width <= 0 || height <= 0 || width > 4096 || height > 4096 {
		return false
	}
	return uint64(width)*uint64(height)*4 <= uint64(guest.HeapSize)
}

func decodeWIPIBMP(data []byte) (*image.RGBA, error) {
	if len(data) < 54 {
		return nil, errors.New("truncated BMP")
	}
	offset := binary.LittleEndian.Uint32(data[10:14])
	dibSize := binary.LittleEndian.Uint32(data[14:18])
	width := int32(binary.LittleEndian.Uint32(data[18:22]))
	rawHeight := int32(binary.LittleEndian.Uint32(data[22:26]))
	planes := binary.LittleEndian.Uint16(data[26:28])
	bitsPerPixel := binary.LittleEndian.Uint16(data[28:30])
	compression := binary.LittleEndian.Uint32(data[30:34])
	colorsUsed := binary.LittleEndian.Uint32(data[46:50])
	if dibSize < 40 || width <= 0 || rawHeight == 0 || planes != 1 ||
		(bitsPerPixel != 1 && bitsPerPixel != 4 &&
			bitsPerPixel != 8 && bitsPerPixel != 24 &&
			bitsPerPixel != 32) ||
		compression != 0 {
		return nil, errors.New("unsupported BMP")
	}
	height := int64(rawHeight)
	topDown := height < 0
	if topDown {
		height = -height
	}
	if height > int64(^uint(0)>>1) ||
		!validWIPIImageSize(int(width), int(height)) {
		return nil, errors.New("invalid BMP dimensions")
	}
	palette := []color.RGBA(nil)
	minimumOffset := uint64(14) + uint64(dibSize)
	if bitsPerPixel <= 8 {
		maximumColors := uint32(1) << bitsPerPixel
		if colorsUsed == 0 {
			colorsUsed = maximumColors
		}
		if colorsUsed > maximumColors {
			return nil, errors.New("invalid BMP palette")
		}
		paletteEnd := minimumOffset + uint64(colorsUsed)*4
		if paletteEnd > uint64(offset) || paletteEnd > uint64(len(data)) {
			return nil, errors.New("truncated BMP palette")
		}
		palette = make([]color.RGBA, colorsUsed)
		for index := range palette {
			position := int(minimumOffset) + index*4
			palette[index] = color.RGBA{
				R: data[position+2],
				G: data[position+1],
				B: data[position],
				A: 0xff,
			}
		}
		minimumOffset = paletteEnd
	}
	rowBits := int64(width) * int64(bitsPerPixel)
	rowBytes := ((rowBits + 31) / 32) * 4
	end := uint64(offset) + uint64(rowBytes)*uint64(height)
	if uint64(offset) < minimumOffset || end > uint64(len(data)) {
		return nil, errors.New("truncated BMP pixels")
	}
	output := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		sourceY := y
		if !topDown {
			sourceY = int(height) - 1 - y
		}
		row := int(offset) + sourceY*int(rowBytes)
		for x := 0; x < int(width); x++ {
			var pixel color.RGBA
			switch bitsPerPixel {
			case 1:
				index := data[row+x/8] >> uint(7-x%8) & 1
				if int(index) >= len(palette) {
					return nil, errors.New("invalid BMP palette index")
				}
				pixel = palette[index]
			case 4:
				index := data[row+x/2]
				if x%2 == 0 {
					index >>= 4
				} else {
					index &= 0x0f
				}
				if int(index) >= len(palette) {
					return nil, errors.New("invalid BMP palette index")
				}
				pixel = palette[index]
			case 8:
				index := data[row+x]
				if int(index) >= len(palette) {
					return nil, errors.New("invalid BMP palette index")
				}
				pixel = palette[index]
			case 24:
				position := row + x*3
				pixel = color.RGBA{
					R: data[position+2],
					G: data[position+1],
					B: data[position],
					A: 0xff,
				}
			case 32:
				position := row + x*4
				pixel = color.RGBA{
					R: data[position+2],
					G: data[position+1],
					B: data[position],
					A: 0xff,
				}
			}
			output.SetRGBA(x, y, pixel)
		}
	}
	return output, nil
}

func (r *Runtime) drawImage(args []uint32) error {
	descriptor, ok, err := r.readImage(args[5])
	if err != nil || !ok {
		return err
	}
	alpha := r.imageAlphaFor(args[5], descriptor)
	translated := append([]uint32(nil), args...)
	translated[5] = descriptor.framebuffer
	// MC_grpDrawImage composites the image's transparency; the framebuffer
	// copy it is built on does not, which is why the mask is threaded through.
	return r.copyFramebufferAlpha(translated, alpha)
}

func (r *Runtime) drawArc(fill bool, args []uint32) error {
	framebuffer, ok := r.Framebuffers[args[0]]
	if !ok {
		return nil
	}
	x, y := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	start, sweep := int(int32(args[5])), int(int32(args[6]))
	context := args[7]
	if width <= 0 || height <= 0 || width > framebuffer.Width*2 ||
		height > framebuffer.Height*2 || sweep == 0 {
		return nil
	}
	radiusX, radiusY := width/2, height/2
	if radiusX <= 0 || radiusY <= 0 {
		rectArgs := []uint32{
			args[0], args[1], args[2], args[3], args[4], context,
		}
		return r.drawRect(fill, rectArgs)
	}
	centerX, centerY := x+radiusX, y+radiusY
	rxSquared := int64(radiusX) * int64(radiusX)
	rySquared := int64(radiusY) * int64(radiusY)
	radiusProduct := rxSquared * rySquared
	threshold := rxSquared*int64(radiusY) + rySquared*int64(radiusX)
	for row := max(0, y); row < min(framebuffer.Height, y+height); row++ {
		for column := max(0, x); column < min(framebuffer.Width, x+width); column++ {
			dx, dy := int64(column-centerX), int64(row-centerY)
			distance := dx*dx*rySquared + dy*dy*rxSquared
			inside := distance <= radiusProduct
			if !fill {
				delta := distance - radiusProduct
				if delta < 0 {
					delta = -delta
				}
				inside = delta <= threshold
			}
			if inside && guest.PointInWIPIArc(column-centerX, row-centerY, start, sweep) {
				if err := r.putPixel(args[0], column, row, context, nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r *Runtime) drawText(unicode bool, args []uint32) error {
	if args[3] == 0 {
		return nil
	}
	length := int32(args[4])
	var characters []uint16
	if unicode {
		if length == -1 {
			// A length of -1 means the UCS-2 string is null-terminated; read
			// until the terminating zero (검은방2/3 draw all their text this way).
			for offset := uint32(0); uint64(len(characters))*2 < uint64(maxWIPIString); offset += 2 {
				var pair [2]byte
				if err := r.CPU.ReadMemory(args[3]+offset, pair[:]); err != nil {
					return err
				}
				value := binary.LittleEndian.Uint16(pair[:])
				if value == 0 {
					break
				}
				characters = append(characters, value)
			}
		} else {
			if length <= 0 || uint64(uint32(length))*2 > uint64(maxWIPIString) {
				return nil
			}
			encoded := make([]byte, int(length)*2)
			if err := r.CPU.ReadMemory(args[3], encoded); err != nil {
				return err
			}
			for index := 0; index < int(length); index++ {
				value := binary.LittleEndian.Uint16(encoded[index*2:])
				if value == 0 {
					break
				}
				characters = append(characters, value)
			}
		}
	} else {
		var encoded []byte
		switch {
		case length == -1:
			value, err := r.ReadCString(args[3])
			if err != nil {
				return err
			}
			encoded = value
		case length > 0 && uint32(length) <= maxWIPIString:
			encoded = make([]byte, int(length))
			if err := r.CPU.ReadMemory(args[3], encoded); err != nil {
				return err
			}
		default:
			return nil
		}
		for _, value := range encoded {
			if value == 0 {
				break
			}
			characters = append(characters, uint16(value))
		}
	}
	font, err := r.graphicsContextFont(args[5])
	if err != nil {
		return err
	}
	fontID, err := r.ensureGraphicsFont(font)
	if err != nil {
		return err
	}
	metrics, err := r.Services.Text.Metrics(r.ServiceOwner, fontID)
	if err != nil {
		return err
	}
	cursor := int(int32(args[1]))
	top := int(int32(args[2])) - int(metrics.Ascent)
	for _, character := range characters {
		glyph, err := r.Services.Text.Glyph(
			r.ServiceOwner,
			fontID,
			rune(character),
		)
		if err != nil {
			return err
		}
		for row := int32(0); row < glyph.Height; row++ {
			for column := int32(0); column < glyph.Width; column++ {
				alpha := glyph.Alpha[row*glyph.Width+column]
				if err := r.putPixelCoverage(
					args[0],
					cursor+int(glyph.BearingX+column),
					top+int(glyph.BearingY+row),
					args[5],
					nil,
					alpha,
				); err != nil {
					return err
				}
			}
		}
		cursor += int(glyph.Advance)
	}
	return nil
}

func (r *Runtime) ensureGraphicsFont(font uint32) (shared.ServiceID, error) {
	var style shared.FontStyle
	if font&0x0100 != 0 {
		style |= shared.FontBold
	}
	if font&0x0200 != 0 {
		style |= shared.FontItalic
	}
	if font&0x0400 != 0 {
		style |= shared.FontUnderlined
	}
	return r.Services.Text.EnsureFont(
		r.ServiceOwner,
		shared.FontDescriptor{
			Family: "aram-fallback",
			Size:   int32(guest.FontHeight(font)),
			Style:  style,
		},
	)
}

func (r *Runtime) graphicsContextFont(context uint32) (uint32, error) {
	graphicsContext, err := r.context(context)
	return graphicsContext.font, err
}

func (r *Runtime) encodeImage(args []uint32) (uint32, error) {
	if args[5] != 0 {
		if err := r.WriteU32(args[5], 0); err != nil {
			return 0, err
		}
	}
	framebuffer, ok := r.Framebuffers[args[0]]
	x, y := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	if !ok || x < 0 || y < 0 || width <= 0 || height <= 0 ||
		x+width > framebuffer.Width || y+height > framebuffer.Height {
		return 0, nil
	}
	if err := r.syncFramebufferToService(framebuffer); err != nil {
		return 0, err
	}
	encoded, err := r.Services.Assets.EncodeSurface(
		r.ServiceOwner,
		r.surfaceServices[framebuffer.Handle],
		"image/bmp",
		shared.Rectangle{
			X:      int32(x),
			Y:      int32(y),
			Width:  int32(width),
			Height: int32(height),
		},
	)
	if err != nil || uint64(len(encoded)) > uint64(guest.HeapSize) {
		return 0, nil
	}
	buffer, err := r.Heap.Allocate(uint32(len(encoded)), false)
	if err != nil || buffer == 0 {
		return 0, err
	}
	if err := r.CPU.WriteMemory(buffer, encoded); err != nil {
		r.Heap.Release(buffer)
		return 0, err
	}
	if args[5] != 0 {
		if err := r.WriteU32(args[5], uint32(len(encoded))); err != nil {
			r.Heap.Release(buffer)
			return 0, err
		}
	}
	return buffer, nil
}

func (r *Runtime) drawPolygon(fill bool, args []uint32) error {
	framebuffer, ok := r.Framebuffers[args[0]]
	count := int(int32(args[3]))
	if !ok || args[1] == 0 || args[2] == 0 || count <= 0 || count > 4096 {
		return nil
	}
	xCoordinates, err := r.readCoordinateArray(args[1], count)
	if err != nil {
		return err
	}
	yCoordinates, err := r.readCoordinateArray(args[2], count)
	if err != nil {
		return err
	}
	if fill && count >= 3 {
		minimumY, maximumY := yCoordinates[0], yCoordinates[0]
		for _, coordinate := range yCoordinates[1:] {
			minimumY = min(minimumY, coordinate)
			maximumY = max(maximumY, coordinate)
		}
		minimumY = max(0, minimumY)
		maximumY = min(framebuffer.Height-1, maximumY)
		nodes := make([]int, 0, count)
		for row := minimumY; row <= maximumY; row++ {
			nodes = nodes[:0]
			previous := count - 1
			for index := 0; index < count; index++ {
				currentY, previousY := yCoordinates[index], yCoordinates[previous]
				if (currentY <= row && previousY > row) ||
					(previousY <= row && currentY > row) {
					numerator := int64(row-currentY) *
						int64(xCoordinates[previous]-xCoordinates[index])
					nodes = append(
						nodes,
						xCoordinates[index]+int(numerator/int64(previousY-currentY)),
					)
				}
				previous = index
			}
			sort.Ints(nodes)
			for index := 0; index+1 < len(nodes); index += 2 {
				start := max(0, nodes[index])
				end := min(framebuffer.Width-1, nodes[index+1])
				for column := start; column <= end; column++ {
					if err := r.putPixel(args[0], column, row, args[4], nil); err != nil {
						return err
					}
				}
			}
		}
	}
	if count <= 1 {
		return nil
	}
	for index := 0; index < count; index++ {
		next := (index + 1) % count
		if err := r.drawLine(
			args[0],
			xCoordinates[index],
			yCoordinates[index],
			xCoordinates[next],
			yCoordinates[next],
			args[4],
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) readCoordinateArray(address uint32, count int) ([]int, error) {
	encoded := make([]byte, count*4)
	if err := r.CPU.ReadMemory(address, encoded); err != nil {
		return nil, err
	}
	result := make([]int, count)
	for index := range result {
		result[index] = int(int32(binary.LittleEndian.Uint32(encoded[index*4:])))
	}
	return result, nil
}
