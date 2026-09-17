package brewrt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"

	"github.com/mirusu400/aram-core/cpu"
	"golang.org/x/image/bmp"
)

const (
	maxNativeImageBytes  = uint32(8 << 20)
	maxNativeImagePixels = uint64(2_000_000)
	idibColorScheme565   = byte(16)
)

// setupNativeImage implements the BREW AEEHelperFuncs SetupNativeImage contract
// for bounded BMP inputs. The returned software IBitmap is also an IDIB, so its
// public fields and owned RGB565 pixel buffer are visible to guest code.
func (r *Runtime) setupNativeImage() error {
	buffer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW native-image buffer: %w", err)
	}
	info, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW native-image info pointer: %w", err)
	}
	reallocated, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW native-image ownership pointer: %w", err)
	}
	if reallocated != 0 {
		if err := r.cpu.WriteMemory(reallocated, []byte{0}); err != nil {
			return fmt.Errorf("clear BREW native-image ownership flag: %w", err)
		}
	}
	if buffer == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	var header [6]byte
	if err := r.cpu.ReadMemory(buffer, header[:]); err != nil {
		return fmt.Errorf("read BREW native-image header: %w", err)
	}
	if string(header[:2]) != "BM" {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	size := binary.LittleEndian.Uint32(header[2:])
	if size < 14 || size > maxNativeImageBytes {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	encoded := make([]byte, size)
	if err := r.cpu.ReadMemory(buffer, encoded); err != nil {
		return fmt.Errorf("read BREW native image: %w", err)
	}
	config, err := bmp.DecodeConfig(bytes.NewReader(encoded))
	if err != nil || config.Width <= 0 || config.Height <= 0 ||
		uint64(config.Width)*uint64(config.Height) > maxNativeImagePixels {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	decoded, err := bmp.Decode(bytes.NewReader(encoded))
	if err != nil {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	object, err := r.createNativeBitmap(decoded)
	if err != nil {
		return err
	}
	if info != 0 {
		width, height := uint16(config.Width), uint16(config.Height)
		data := make([]byte, 10)
		binary.LittleEndian.PutUint16(data[0:], width)
		binary.LittleEndian.PutUint16(data[2:], height)
		binary.LittleEndian.PutUint16(data[8:], width)
		if err := r.cpu.WriteMemory(info, data); err != nil {
			return fmt.Errorf("write BREW native-image info: %w", err)
		}
	}
	if reallocated != 0 {
		if err := r.cpu.WriteMemory(reallocated, []byte{1}); err != nil {
			return fmt.Errorf("write BREW native-image ownership flag: %w", err)
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, object)
}

func (r *Runtime) createNativeBitmap(source image.Image) (uint32, error) {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	pitch := (width*2 + 3) &^ 3
	pixelsSize := uint32(pitch * height)
	object, err := r.allocateGuest(36)
	if err != nil {
		return 0, err
	}
	pixels, err := r.allocateGuest(pixelsSize)
	if err != nil {
		return 0, err
	}
	data := make([]byte, pixelsSize)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, _ := source.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			value := uint16((r>>11)<<11 | (g>>10)<<5 | b>>11)
			binary.LittleEndian.PutUint16(data[y*pitch+x*2:], value)
		}
	}
	if err := r.cpu.WriteMemory(pixels, data); err != nil {
		return 0, fmt.Errorf("write BREW native bitmap pixels: %w", err)
	}
	header := make([]byte, 36)
	binary.LittleEndian.PutUint32(header[0:], bitmapVTable)
	binary.LittleEndian.PutUint32(header[8:], pixels)
	binary.LittleEndian.PutUint16(header[20:], uint16(width))
	binary.LittleEndian.PutUint16(header[22:], uint16(height))
	binary.LittleEndian.PutUint16(header[24:], uint16(pitch))
	header[28] = 16
	header[29] = idibColorScheme565
	if err := r.cpu.WriteMemory(object, header); err != nil {
		return 0, fmt.Errorf("write BREW native bitmap header: %w", err)
	}
	return object, nil
}

func (r *Runtime) imageBitmap(object uint32) (uint32, error) {
	var encoded [8]byte
	if err := r.cpu.ReadMemory(object, encoded[:]); err != nil {
		return 0, fmt.Errorf("read BREW image object: %w", err)
	}
	if binary.LittleEndian.Uint32(encoded[:4]) != imageVTable {
		return 0, fmt.Errorf("invalid BREW image object")
	}
	return binary.LittleEndian.Uint32(encoded[4:]), nil
}

func (r *Runtime) drawImage(frame bool) error {
	object, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return err
	}
	xRegister, yRegister := uint32(cpu.RegisterR1), uint32(cpu.RegisterR2)
	if frame {
		xRegister, yRegister = cpu.RegisterR2, cpu.RegisterR3
	}
	x, err := r.cpu.ReadRegister(xRegister)
	if err != nil {
		return err
	}
	y, err := r.cpu.ReadRegister(yRegister)
	if err != nil {
		return err
	}
	bitmap, err := r.imageBitmap(object)
	if err != nil {
		return err
	}
	return r.drawBitmapAt(bitmap, int32(x), int32(y))
}

func (r *Runtime) returnImageInfo() error {
	object, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return err
	}
	destination, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return err
	}
	bitmap, err := r.imageBitmap(object)
	if err != nil {
		return err
	}
	header := make([]byte, 36)
	if err := r.cpu.ReadMemory(bitmap, header); err != nil {
		return fmt.Errorf("read BREW image bitmap: %w", err)
	}
	var info [10]byte
	copy(info[0:4], header[20:24])
	binary.LittleEndian.PutUint16(info[4:], 1)
	info[6] = header[28]
	binary.LittleEndian.PutUint16(info[8:], binary.LittleEndian.Uint16(header[20:22]))
	if destination != 0 {
		if err := r.cpu.WriteMemory(destination, info[:]); err != nil {
			return fmt.Errorf("write BREW image info: %w", err)
		}
	}
	return nil
}

func (r *Runtime) setImageParameter() error {
	object, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return err
	}
	parameter, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return err
	}
	value, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return err
	}
	result, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return err
	}
	status := uint32(1)
	if parameter == 10 && value != 0 { // IPARM_GETBITMAP
		bitmap, bitmapErr := r.imageBitmap(object)
		if bitmapErr != nil {
			return bitmapErr
		}
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], bitmap)
		if err := r.cpu.WriteMemory(value, encoded[:]); err != nil {
			return fmt.Errorf("write BREW image bitmap output: %w", err)
		}
		status = 0
	}
	if result != 0 {
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], status)
		if err := r.cpu.WriteMemory(result, encoded[:]); err != nil {
			return fmt.Errorf("write BREW image parameter status: %w", err)
		}
	}
	return nil
}

func (r *Runtime) drawBitmapAt(bitmap uint32, destinationX, destinationY int32) error {
	header := make([]byte, 36)
	if err := r.cpu.ReadMemory(bitmap, header); err != nil {
		return fmt.Errorf("read BREW image bitmap: %w", err)
	}
	pixels := binary.LittleEndian.Uint32(header[8:])
	width := uint32(binary.LittleEndian.Uint16(header[20:]))
	height := uint32(binary.LittleEndian.Uint16(header[22:]))
	pitch := uint32(binary.LittleEndian.Uint16(header[24:]))
	if pixels == 0 || pitch < width*2 || header[28] != 16 || header[29] != idibColorScheme565 {
		return nil
	}
	for row := uint32(0); row < height; row++ {
		targetY := destinationY + int32(row)
		if targetY < 0 || targetY >= int32(framebufferHeight) {
			continue
		}
		for column := uint32(0); column < width; column++ {
			targetX := destinationX + int32(column)
			if targetX < 0 || targetX >= int32(framebufferWidth) {
				continue
			}
			var pixel [2]byte
			if err := r.cpu.ReadMemory(pixels+row*pitch+column*2, pixel[:]); err != nil {
				return fmt.Errorf("read BREW image pixel: %w", err)
			}
			address := framebufferBase + (uint32(targetY)*framebufferWidth+uint32(targetX))*2
			if err := r.cpu.WriteMemory(address, pixel[:]); err != nil {
				return fmt.Errorf("write BREW image pixel: %w", err)
			}
		}
	}
	return nil
}

func (r *Runtime) blitDisplayBitmap() error {
	destinationX, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW BitBlt destination x: %w", err)
	}
	destinationY, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW BitBlt destination y: %w", err)
	}
	width, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW BitBlt width: %w", err)
	}
	stack, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return fmt.Errorf("read BREW BitBlt stack: %w", err)
	}
	args := make([]byte, 20)
	if err := r.cpu.ReadMemory(stack, args); err != nil {
		return fmt.Errorf("read BREW BitBlt arguments: %w", err)
	}
	height := binary.LittleEndian.Uint32(args[0:])
	bitmap := binary.LittleEndian.Uint32(args[4:])
	sourceX := binary.LittleEndian.Uint32(args[8:])
	sourceY := binary.LittleEndian.Uint32(args[12:])
	if bitmap == 0 || width == 0 || height == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	header := make([]byte, 36)
	if err := r.cpu.ReadMemory(bitmap, header); err != nil {
		return fmt.Errorf("read BREW BitBlt bitmap: %w", err)
	}
	pixels := binary.LittleEndian.Uint32(header[8:])
	sourceWidth := uint32(binary.LittleEndian.Uint16(header[20:]))
	sourceHeight := uint32(binary.LittleEndian.Uint16(header[22:]))
	pitch := uint32(binary.LittleEndian.Uint16(header[24:]))
	if pixels == 0 || sourceWidth == 0 || sourceHeight == 0 || pitch < sourceWidth*2 ||
		header[28] != 16 || header[29] != idibColorScheme565 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	copyWidth := width
	if sourceX >= sourceWidth || sourceY >= sourceHeight {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	if copyWidth > sourceWidth-sourceX {
		copyWidth = sourceWidth - sourceX
	}
	copyHeight := height
	if copyHeight > sourceHeight-sourceY {
		copyHeight = sourceHeight - sourceY
	}
	dx, dy := int32(destinationX), int32(destinationY)
	for row := uint32(0); row < copyHeight; row++ {
		targetY := dy + int32(row)
		if targetY < 0 || targetY >= int32(framebufferHeight) {
			continue
		}
		for column := uint32(0); column < copyWidth; column++ {
			targetX := dx + int32(column)
			if targetX < 0 || targetX >= int32(framebufferWidth) {
				continue
			}
			var pixel [2]byte
			sourceAt := pixels + (sourceY+row)*pitch + (sourceX+column)*2
			if err := r.cpu.ReadMemory(sourceAt, pixel[:]); err != nil {
				return fmt.Errorf("read BREW BitBlt pixel: %w", err)
			}
			targetAt := framebufferBase + (uint32(targetY)*framebufferWidth+uint32(targetX))*2
			if err := r.cpu.WriteMemory(targetAt, pixel[:]); err != nil {
				return fmt.Errorf("write BREW BitBlt pixel: %w", err)
			}
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, 0)
}
