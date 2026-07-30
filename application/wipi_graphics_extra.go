package application

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	stdDraw "image/draw"
	"image/gif"
	"image/png"
	"sort"
)

const (
	wipiImageMagic          = uint32(0x474d4957) // "WIMG" in guest memory.
	wipiImageDescriptorSize = uint32(40)

	wipiShortBuffer    = int32(-2)
	wipiNoEntry        = int32(-4)
	wipiExists         = int32(-6)
	wipiInvalid        = int32(-8)
	wipiNoMemory       = int32(-12)
	wipiBadFormat      = int32(-20)
	wipiImageDone      = int32(1)
	wipiImageFrameDone = int32(0)
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

type decodedWIPIImage struct {
	frame      image.Image
	width      int
	height     int
	animated   bool
	delayMS    int32
	loopCount  int32
	frameCount uint32
}

func wipiReturnCode(code int32) uint32 {
	return uint32(code)
}

func (r *wipiRuntime) readImage(handle uint32) (wipiImageDescriptor, bool, error) {
	if handle == 0 || r.heap.allocations[handle] < wipiImageDescriptorSize {
		return wipiImageDescriptor{}, false, nil
	}
	var encoded [wipiImageDescriptorSize]byte
	if err := r.cpu.ReadMemory(handle, encoded[:]); err != nil {
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
	if _, ok := r.framebuffers[descriptor.framebuffer]; !ok {
		return wipiImageDescriptor{}, false, nil
	}
	if descriptor.frameCount == 0 || descriptor.frameIndex >= descriptor.frameCount {
		return wipiImageDescriptor{}, false, nil
	}
	if descriptor.bufferID != 0 {
		size, ok := r.heap.allocations[descriptor.bufferID]
		end := uint64(descriptor.offset) + uint64(descriptor.length)
		if !ok || descriptor.length == 0 || end > uint64(size) {
			return wipiImageDescriptor{}, false, nil
		}
	}
	return descriptor, true, nil
}

func (r *wipiRuntime) writeImage(handle uint32, descriptor wipiImageDescriptor) error {
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
	return r.cpu.WriteMemory(handle, encoded[:])
}

func (r *wipiRuntime) imageProperty(handle uint32, property int32) (int32, error) {
	descriptor, ok, err := r.readImage(handle)
	if err != nil || !ok {
		return 0, err
	}
	framebuffer := r.framebuffers[descriptor.framebuffer]
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
		return int32(framebuffer.width), nil
	case 5:
		return int32(framebuffer.height), nil
	case 6:
		return int32(framebuffer.bitsPerPixel), nil
	}
	return 0, nil
}

func (r *wipiRuntime) createImage(
	output uint32,
	bufferID uint32,
	offset int32,
	length int32,
) (int32, error) {
	if output == 0 {
		return wipiInvalid, nil
	}
	if err := r.writeU32(output, 0); err != nil {
		return wipiInvalid, err
	}
	size, allocated := r.heap.allocations[bufferID]
	if !allocated || offset < 0 || length <= 0 ||
		uint64(uint32(offset))+uint64(uint32(length)) > uint64(size) {
		return wipiBadFormat, nil
	}
	data := make([]byte, int(length))
	if err := r.cpu.ReadMemory(bufferID+uint32(offset), data); err != nil {
		return wipiBadFormat, err
	}
	decoded, err := decodeWIPIImage(data, 0)
	if err != nil {
		return wipiBadFormat, nil
	}
	framebuffer, err := r.newFramebuffer(decoded.width, decoded.height, true)
	if err != nil {
		return wipiNoMemory, err
	}
	if framebuffer == 0 {
		return wipiNoMemory, nil
	}
	cleanupFramebuffer := true
	defer func() {
		if cleanupFramebuffer {
			r.destroyOwnedFramebuffer(framebuffer)
		}
	}()
	if err := r.paintImageFrame(framebuffer, decoded.frame, true); err != nil {
		return wipiBadFormat, err
	}
	handle, err := r.heap.allocate(wipiImageDescriptorSize, true)
	if err != nil {
		return wipiNoMemory, err
	}
	if handle == 0 {
		return wipiNoMemory, nil
	}
	descriptor := wipiImageDescriptor{
		framebuffer: framebuffer,
		frameIndex:  0,
		frameCount:  decoded.frameCount,
		animated:    decoded.animated,
		delayMS:     decoded.delayMS,
		loopCount:   decoded.loopCount,
	}
	if decoded.animated {
		descriptor.bufferID = bufferID
		descriptor.offset = uint32(offset)
		descriptor.length = uint32(length)
	}
	if err := r.writeImage(handle, descriptor); err != nil {
		r.heap.release(handle)
		return wipiNoMemory, err
	}
	if err := r.writeU32(output, handle); err != nil {
		r.heap.release(handle)
		return wipiInvalid, err
	}
	if !decoded.animated {
		r.heap.release(bufferID)
	}
	cleanupFramebuffer = false
	return wipiImageDone, nil
}

func (r *wipiRuntime) destroyImage(handle uint32) error {
	descriptor, ok, err := r.readImage(handle)
	if err != nil || !ok {
		return err
	}
	r.destroyOwnedFramebuffer(descriptor.framebuffer)
	if descriptor.bufferID != 0 {
		r.heap.release(descriptor.bufferID)
	}
	r.heap.release(handle)
	return nil
}

func (r *wipiRuntime) destroyOwnedFramebuffer(handle uint32) {
	framebuffer, ok := r.framebuffers[handle]
	if !ok || handle == r.screenHandle || !framebuffer.owns {
		return
	}
	r.heap.release(framebuffer.pixels)
	r.heap.release(framebuffer.handle)
	delete(r.framebuffers, handle)
}

func (r *wipiRuntime) decodeNextImage(handle uint32) (int32, error) {
	descriptor, ok, err := r.readImage(handle)
	if err != nil {
		return wipiBadFormat, err
	}
	if !ok {
		return wipiInvalid, nil
	}
	if !descriptor.animated || descriptor.frameCount <= 1 ||
		descriptor.frameIndex+1 >= descriptor.frameCount {
		return wipiImageDone, nil
	}
	data := make([]byte, descriptor.length)
	if err := r.cpu.ReadMemory(descriptor.bufferID+descriptor.offset, data); err != nil {
		return wipiBadFormat, err
	}
	nextIndex := descriptor.frameIndex + 1
	decoded, err := decodeWIPIImage(data, nextIndex)
	if err != nil {
		return wipiBadFormat, nil
	}
	if err := r.paintImageFrame(descriptor.framebuffer, decoded.frame, true); err != nil {
		return wipiBadFormat, err
	}
	descriptor.frameIndex = nextIndex
	descriptor.delayMS = decoded.delayMS
	if err := r.writeImage(handle, descriptor); err != nil {
		return wipiBadFormat, err
	}
	if nextIndex+1 >= descriptor.frameCount {
		return wipiImageDone, nil
	}
	return wipiImageFrameDone, nil
}

func decodeWIPIImage(data []byte, frameIndex uint32) (decodedWIPIImage, error) {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}):
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || !validWIPIImageSize(config.Width, config.Height) {
			return decodedWIPIImage{}, errors.New("invalid PNG")
		}
		decoded, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return decodedWIPIImage{}, err
		}
		return decodedWIPIImage{
			frame:      decoded,
			width:      config.Width,
			height:     config.Height,
			frameCount: 1,
		}, nil
	case len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) ||
		bytes.Equal(data[:6], []byte("GIF89a"))):
		decoded, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil || len(decoded.Image) == 0 ||
			!validWIPIImageSize(decoded.Config.Width, decoded.Config.Height) ||
			frameIndex >= uint32(len(decoded.Image)) {
			return decodedWIPIImage{}, errors.New("invalid GIF")
		}
		frame := compositeGIFFrame(decoded, int(frameIndex))
		delay := int32(0)
		if int(frameIndex) < len(decoded.Delay) {
			delay = int32(decoded.Delay[frameIndex]) * 10
		}
		return decodedWIPIImage{
			frame:      frame,
			width:      decoded.Config.Width,
			height:     decoded.Config.Height,
			animated:   len(decoded.Image) > 1,
			delayMS:    delay,
			loopCount:  int32(decoded.LoopCount),
			frameCount: uint32(len(decoded.Image)),
		}, nil
	case len(data) >= 2 && data[0] == 'B' && data[1] == 'M':
		decoded, err := decodeWIPIBMP(data)
		if err != nil {
			return decodedWIPIImage{}, err
		}
		return decodedWIPIImage{
			frame:      decoded,
			width:      decoded.Bounds().Dx(),
			height:     decoded.Bounds().Dy(),
			frameCount: 1,
		}, nil
	default:
		return decodedWIPIImage{}, errors.New("unsupported image format")
	}
}

func validWIPIImageSize(width, height int) bool {
	if width <= 0 || height <= 0 || width > 4096 || height > 4096 {
		return false
	}
	return uint64(width)*uint64(height)*4 <= uint64(guestHeapSize)
}

func compositeGIFFrame(decoded *gif.GIF, target int) *image.RGBA {
	bounds := image.Rect(0, 0, decoded.Config.Width, decoded.Config.Height)
	canvas := image.NewRGBA(bounds)
	var restore *image.RGBA
	for index := 0; index <= target; index++ {
		if index > 0 && index-1 < len(decoded.Disposal) {
			previous := decoded.Image[index-1].Bounds()
			switch decoded.Disposal[index-1] {
			case gif.DisposalBackground:
				stdDraw.Draw(canvas, previous, image.NewUniform(color.RGBA{}), image.Point{}, stdDraw.Src)
			case gif.DisposalPrevious:
				if restore != nil {
					stdDraw.Draw(canvas, bounds, restore, image.Point{}, stdDraw.Src)
				}
			}
		}
		if index < len(decoded.Disposal) && decoded.Disposal[index] == gif.DisposalPrevious {
			restore = image.NewRGBA(bounds)
			stdDraw.Draw(restore, bounds, canvas, image.Point{}, stdDraw.Src)
		}
		frameBounds := decoded.Image[index].Bounds()
		stdDraw.Draw(canvas, frameBounds, decoded.Image[index], frameBounds.Min, stdDraw.Over)
	}
	return canvas
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

func (r *wipiRuntime) paintImageFrame(handle uint32, decoded image.Image, clear bool) error {
	framebuffer, ok := r.framebuffers[handle]
	if !ok {
		return nil
	}
	if clear {
		if err := zeroGuestMemory(
			r.cpu,
			framebuffer.pixels,
			uint32(framebuffer.width*framebuffer.height)*
				framebuffer.bytesPerPixel(),
		); err != nil {
			return err
		}
	}
	bounds := decoded.Bounds()
	for y := max(0, bounds.Min.Y); y < min(framebuffer.height, bounds.Max.Y); y++ {
		for x := max(0, bounds.Min.X); x < min(framebuffer.width, bounds.Max.X); x++ {
			red, green, blue, _ := decoded.At(x, y).RGBA()
			pixel := r.pixelFromRGB(
				uint32(red>>8),
				uint32(green>>8),
				uint32(blue>>8),
			)
			if err := r.writeFramebufferPixel(
				framebuffer,
				x,
				y,
				pixel,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *wipiRuntime) drawImage(args []uint32) error {
	descriptor, ok, err := r.readImage(args[5])
	if err != nil || !ok {
		return err
	}
	translated := append([]uint32(nil), args...)
	translated[5] = descriptor.framebuffer
	return r.copyFramebuffer(translated)
}

func (r *wipiRuntime) drawArc(fill bool, args []uint32) error {
	framebuffer, ok := r.framebuffers[args[0]]
	if !ok {
		return nil
	}
	x, y := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	start, sweep := int(int32(args[5])), int(int32(args[6]))
	context := args[7]
	if width <= 0 || height <= 0 || width > framebuffer.width*2 ||
		height > framebuffer.height*2 || sweep == 0 {
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
	for row := max(0, y); row < min(framebuffer.height, y+height); row++ {
		for column := max(0, x); column < min(framebuffer.width, x+width); column++ {
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
			if inside && pointInWIPIArc(column-centerX, row-centerY, start, sweep) {
				if err := r.putPixel(args[0], column, row, context, nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func pointInWIPIArc(dx, dy, start, sweep int) bool {
	if sweep >= 360 || sweep <= -360 {
		return true
	}
	if sweep == 0 {
		return false
	}
	if dx == 0 && dy == 0 {
		return true
	}
	absoluteX, absoluteY := abs(dx), abs(dy)
	angle := absoluteY * 90 / (absoluteX + absoluteY)
	switch {
	case dx < 0 && dy >= 0:
		angle = 180 - angle
	case dx < 0 && dy < 0:
		angle = 180 + angle
	case dx >= 0 && dy < 0:
		angle = 360 - angle
	}
	normalize := func(value int) int {
		value %= 360
		if value < 0 {
			value += 360
		}
		return value
	}
	angle, begin, end := normalize(angle), normalize(start), normalize(start+sweep)
	if sweep > 0 {
		if begin <= end {
			return angle >= begin && angle <= end
		}
		return angle >= begin || angle <= end
	}
	if end <= begin {
		return angle >= end && angle <= begin
	}
	return angle >= end || angle <= begin
}

func (r *wipiRuntime) drawText(unicode bool, args []uint32) error {
	if args[3] == 0 {
		return nil
	}
	length := int32(args[4])
	var characters []uint16
	if unicode {
		if length <= 0 || uint64(uint32(length))*2 > uint64(maxWIPIString) {
			return nil
		}
		encoded := make([]byte, int(length)*2)
		if err := r.cpu.ReadMemory(args[3], encoded); err != nil {
			return err
		}
		for index := 0; index < int(length); index++ {
			value := binary.LittleEndian.Uint16(encoded[index*2:])
			if value == 0 {
				break
			}
			characters = append(characters, value)
		}
	} else {
		var encoded []byte
		switch {
		case length == -1:
			value, err := r.readCString(args[3])
			if err != nil {
				return err
			}
			encoded = value
		case length > 0 && uint32(length) <= maxWIPIString:
			encoded = make([]byte, int(length))
			if err := r.cpu.ReadMemory(args[3], encoded); err != nil {
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
	height := fontHeight(font)
	ascent := height - height/4
	characterWidth := max(1, height/2)
	x, baseline := int(int32(args[1])), int(int32(args[2]))
	for index, character := range characters {
		if character == 0x20 {
			continue
		}
		rectArgs := []uint32{
			args[0],
			uint32(int32(x + index*characterWidth)),
			uint32(int32(baseline - ascent)),
			uint32(int32(max(1, characterWidth-1))),
			uint32(int32(height)),
			args[5],
		}
		if err := r.drawRect(true, rectArgs); err != nil {
			return err
		}
	}
	return nil
}

func (r *wipiRuntime) graphicsContextFont(context uint32) (uint32, error) {
	graphicsContext, err := r.context(context)
	return graphicsContext.font, err
}

func (r *wipiRuntime) encodeImage(args []uint32) (uint32, error) {
	if args[5] != 0 {
		if err := r.writeU32(args[5], 0); err != nil {
			return 0, err
		}
	}
	framebuffer, ok := r.framebuffers[args[0]]
	x, y := int(int32(args[1])), int(int32(args[2]))
	width, height := int(int32(args[3])), int(int32(args[4]))
	if !ok || x < 0 || y < 0 || width <= 0 || height <= 0 ||
		x+width > framebuffer.width || y+height > framebuffer.height {
		return 0, nil
	}
	rowBytes := (width*3 + 3) &^ 3
	total := uint64(54) + uint64(rowBytes)*uint64(height)
	if total > uint64(guestHeapSize) {
		return 0, nil
	}
	encoded := make([]byte, int(total))
	copy(encoded[:2], "BM")
	binary.LittleEndian.PutUint32(encoded[2:6], uint32(total))
	binary.LittleEndian.PutUint32(encoded[10:14], 54)
	binary.LittleEndian.PutUint32(encoded[14:18], 40)
	binary.LittleEndian.PutUint32(encoded[18:22], uint32(width))
	binary.LittleEndian.PutUint32(encoded[22:26], uint32(height))
	binary.LittleEndian.PutUint16(encoded[26:28], 1)
	binary.LittleEndian.PutUint16(encoded[28:30], 24)
	binary.LittleEndian.PutUint32(encoded[34:38], uint32(rowBytes*height))
	for destinationY := 0; destinationY < height; destinationY++ {
		sourceY := y + height - 1 - destinationY
		row := 54 + destinationY*rowBytes
		for column := 0; column < width; column++ {
			pixel, err := r.framebufferPixel(
				framebuffer,
				x+column,
				sourceY,
			)
			if err != nil {
				return 0, err
			}
			red, green, blue := r.rgbFromPixel(pixel)
			position := row + column*3
			encoded[position] = byte(blue)
			encoded[position+1] = byte(green)
			encoded[position+2] = byte(red)
		}
	}
	buffer, err := r.heap.allocate(uint32(total), false)
	if err != nil || buffer == 0 {
		return 0, err
	}
	if err := r.cpu.WriteMemory(buffer, encoded); err != nil {
		r.heap.release(buffer)
		return 0, err
	}
	if args[5] != 0 {
		if err := r.writeU32(args[5], uint32(total)); err != nil {
			r.heap.release(buffer)
			return 0, err
		}
	}
	return buffer, nil
}

func (r *wipiRuntime) drawPolygon(fill bool, args []uint32) error {
	framebuffer, ok := r.framebuffers[args[0]]
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
		maximumY = min(framebuffer.height-1, maximumY)
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
				end := min(framebuffer.width-1, nodes[index+1])
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

func (r *wipiRuntime) readCoordinateArray(address uint32, count int) ([]int, error) {
	encoded := make([]byte, count*4)
	if err := r.cpu.ReadMemory(address, encoded); err != nil {
		return nil, err
	}
	result := make([]int, count)
	for index := range result {
		result[index] = int(int32(binary.LittleEndian.Uint32(encoded[index*4:])))
	}
	return result, nil
}
