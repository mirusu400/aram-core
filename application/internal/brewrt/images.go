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
	aeeROTransparent     = uint32(7)
)

type brewNativeImage struct {
	encoded uint32
	span    uint32
}

// setupNativeImage implements the BREW AEEHelperFuncs SetupNativeImage contract
// for bounded BMP inputs. The returned software IBitmap is also an IDIB. When
// an indexed caller allocation reserves enough room, conversion happens in
// place so later guest RGB565 drawing remains visible through the bitmap.
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
	var header [54]byte
	if err := r.cpu.ReadMemory(buffer, header[:]); err != nil {
		return fmt.Errorf("read BREW native-image header: %w", err)
	}
	encodedSize, ok := nativeBMPSpan(header[:])
	if !ok {
		if object, handled, err := r.setupPalettelessNativeFramebuffer(buffer, header[:], info); handled || err != nil {
			if err != nil {
				return err
			}
			return r.cpu.WriteRegister(cpu.RegisterR0, object)
		}
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	encoded := make([]byte, encodedSize)
	if err := r.cpu.ReadMemory(buffer, encoded); err != nil {
		return fmt.Errorf("read BREW native image: %w", err)
	}
	decoded, err := decodeNativeBMP(encoded, encodedSize)
	if err != nil {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	// Some handset games decode the same full-screen staging BMP on every
	// animation tick without releasing the previous IDIB. Reuse that IDIB's
	// storage when its source buffer and geometry are unchanged; otherwise a
	// few seconds of gameplay exhausts the emulated heap with identical-sized
	// surfaces. Smaller bitmaps retain independent snapshot semantics.
	object := uint32(0)
	if decoded.Bounds().Dx() == int(r.screenWidth) && decoded.Bounds().Dy() == int(r.screenHeight) {
		object, err = r.reuseNativeBitmap(buffer, encodedSize, decoded)
		if err != nil {
			return err
		}
	}
	if object == 0 {
		object, err = r.createNativeBitmap(decoded)
		if err != nil {
			return err
		}
	}
	if info != 0 {
		width, height := uint16(decoded.Bounds().Dx()), uint16(decoded.Bounds().Dy())
		data := make([]byte, 10)
		binary.LittleEndian.PutUint16(data[0:], width)
		binary.LittleEndian.PutUint16(data[2:], height)
		binary.LittleEndian.PutUint16(data[8:], width)
		if err := r.cpu.WriteMemory(info, data); err != nil {
			return fmt.Errorf("write BREW native-image info: %w", err)
		}
	}
	if _, reused := r.nativeImages[object]; !reused {
		direct, err := r.attachExpandedRGB565Backing(object, buffer, encoded, decoded)
		if err != nil {
			return err
		}
		if !direct {
			r.nativeImages[object] = brewNativeImage{encoded: buffer, span: encodedSize}
		}
	}
	// A decoded BMP produces a native bitmap allocation. The ownership flag is
	// part of the caller's render path: leaving it clear makes some games skip
	// BitBlt entirely even though the returned bitmap is valid. Caller-backed
	// RGB565 surfaces above retain the cleared flag because their pixels remain
	// owned by the original buffer.
	if reallocated != 0 {
		if err := r.cpu.WriteMemory(reallocated, []byte{1}); err != nil {
			return fmt.Errorf("mark BREW native-image allocation: %w", err)
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, object)
}

// Some handset engines pass a BMP-shaped header without an embedded palette,
// reserving the remainder of the same guest allocation for a live RGB565 IDIB.
// This is not a decodable BMP: SetupNativeImage must expose that caller-owned
// pixel storage instead of rejecting the surface and returning a null bitmap.
func (r *Runtime) setupPalettelessNativeFramebuffer(buffer uint32, header []byte, info uint32) (uint32, bool, error) {
	if string(header[:2]) != "BM" || binary.LittleEndian.Uint32(header[14:18]) != 40 ||
		binary.LittleEndian.Uint32(header[10:14]) != 54 || binary.LittleEndian.Uint16(header[26:28]) != 1 ||
		binary.LittleEndian.Uint16(header[28:30]) != 8 || binary.LittleEndian.Uint32(header[30:34]) != 0 {
		return 0, false, nil
	}
	width := binary.LittleEndian.Uint32(header[18:22])
	height := binary.LittleEndian.Uint32(header[22:26])
	if width == 0 || height == 0 || width > 0xffff || height > 0xffff || uint64(width)*uint64(height) > maxNativeImagePixels {
		return 0, false, nil
	}
	pitch := (width*2 + 3) &^ 3
	span := uint64(54) + uint64(pitch)*uint64(height)
	if pitch > 0xffff || span > uint64(maxNativeImageBytes) || span > uint64(r.heapAllocated[buffer]) {
		return 0, false, nil
	}
	object, err := r.allocateGuest(36)
	if err != nil {
		return 0, true, err
	}
	bitmap := make([]byte, 36)
	binary.LittleEndian.PutUint32(bitmap[0:], bitmapVTable)
	binary.LittleEndian.PutUint32(bitmap[8:], buffer+54)
	binary.LittleEndian.PutUint16(bitmap[20:], uint16(width))
	binary.LittleEndian.PutUint16(bitmap[22:], uint16(height))
	binary.LittleEndian.PutUint16(bitmap[24:], uint16(pitch))
	bitmap[28] = 16
	bitmap[29] = idibColorScheme565
	if err := r.cpu.WriteMemory(object, bitmap); err != nil {
		r.releaseGuest(object)
		return 0, true, fmt.Errorf("write BREW caller-backed bitmap: %w", err)
	}
	if info != 0 {
		data := make([]byte, 10)
		binary.LittleEndian.PutUint16(data[0:], uint16(width))
		binary.LittleEndian.PutUint16(data[2:], uint16(height))
		binary.LittleEndian.PutUint16(data[8:], uint16(width))
		if err := r.cpu.WriteMemory(info, data); err != nil {
			r.releaseGuest(object)
			return 0, true, fmt.Errorf("write BREW caller-backed image info: %w", err)
		}
	}
	return object, true, nil
}

func (r *Runtime) reuseNativeBitmap(buffer, span uint32, decoded image.Image) (uint32, error) {
	if r.heapAllocated[buffer] < span {
		return 0, nil
	}
	for object, native := range r.nativeImages {
		if native.encoded != buffer || native.span != span {
			continue
		}
		if _, live := r.heapAllocated[object]; !live {
			continue
		}
		var header [30]byte
		if err := r.cpu.ReadMemory(object, header[:]); err != nil || binary.LittleEndian.Uint32(header[:4]) != bitmapVTable {
			continue
		}
		width := int(binary.LittleEndian.Uint16(header[20:22]))
		height := int(binary.LittleEndian.Uint16(header[22:24]))
		pitch := int(binary.LittleEndian.Uint16(header[24:26]))
		pixels := binary.LittleEndian.Uint32(header[8:12])
		if width != decoded.Bounds().Dx() || height != decoded.Bounds().Dy() || pitch < width*2 ||
			r.heapAllocated[pixels] < uint32(pitch*height) {
			continue
		}
		if err := r.cpu.WriteMemory(pixels, nativeRGB565(decoded, pitch)); err != nil {
			return 0, fmt.Errorf("reuse BREW native bitmap pixels: %w", err)
		}
		return object, nil
	}
	return 0, nil
}

func nativeBMPSpan(header []byte) (uint32, bool) {
	if len(header) < 54 || string(header[:2]) != "BM" {
		return 0, false
	}
	infoSize := binary.LittleEndian.Uint32(header[14:18])
	if infoSize != 40 && infoSize != 108 && infoSize != 124 {
		return 0, false
	}
	width := int64(int32(binary.LittleEndian.Uint32(header[18:22])))
	height := int64(int32(binary.LittleEndian.Uint32(header[22:26])))
	if height < 0 {
		height = -height
	}
	bitsPerPixel := uint64(binary.LittleEndian.Uint16(header[28:30]))
	compression := binary.LittleEndian.Uint32(header[30:34])
	if width <= 0 || height <= 0 ||
		uint64(width)*uint64(height) > maxNativeImagePixels ||
		compression != 0 ||
		bitsPerPixel != 1 && bitsPerPixel != 2 && bitsPerPixel != 4 &&
			bitsPerPixel != 8 && bitsPerPixel != 24 && bitsPerPixel != 32 {
		return 0, false
	}
	pixelOffset := uint64(binary.LittleEndian.Uint32(header[10:14]))
	metadataEnd := uint64(14) + uint64(infoSize)
	if pixelOffset < metadataEnd {
		return 0, false
	}
	if bitsPerPixel <= 8 {
		paletteBytes := pixelOffset - metadataEnd
		paletteEntries := paletteBytes / 4
		if paletteBytes%4 != 0 || paletteEntries == 0 || paletteEntries > 1<<bitsPerPixel {
			return 0, false
		}
	} else if pixelOffset != metadataEnd {
		return 0, false
	}
	rowBytes := (uint64(width)*bitsPerPixel + 31) / 32 * 4
	span := pixelOffset + rowBytes*uint64(height)
	if span > uint64(maxNativeImageBytes) || span > uint64(^uint32(0)) {
		return 0, false
	}
	return uint32(span), true
}

func (r *Runtime) createNativeBitmap(source image.Image) (uint32, error) {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 || width > 0xffff || height > 0xffff {
		return 0, fmt.Errorf("BREW native bitmap dimensions %dx%d are out of range", width, height)
	}
	pitch64 := (uint64(width)*2 + 3) &^ 3
	pixels64 := pitch64 * uint64(height)
	if pitch64 > 0xffff || pixels64 > uint64(maxNativeImageBytes) {
		return 0, fmt.Errorf("BREW native bitmap storage %dx%d is out of range", pitch64, pixels64)
	}
	pitch := int(pitch64)
	pixelsSize := uint32(pixels64)
	object, err := r.allocateGuest(36)
	if err != nil {
		return 0, err
	}
	pixels, err := r.allocateGuest(pixelsSize)
	if err != nil {
		r.releaseGuest(object)
		return 0, err
	}
	data := nativeRGB565(source, pitch)
	if err := r.cpu.WriteMemory(pixels, data); err != nil {
		r.releaseGuest(pixels)
		r.releaseGuest(object)
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
		r.releaseGuest(pixels)
		r.releaseGuest(object)
		return 0, fmt.Errorf("write BREW native bitmap header: %w", err)
	}
	return object, nil
}

func nativeRGB565(source image.Image, pitch int) []byte {
	bounds := source.Bounds()
	data := make([]byte, pitch*bounds.Dy())
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			r, g, b, _ := source.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			value := uint16((r>>11)<<11 | (g>>10)<<5 | b>>11)
			binary.LittleEndian.PutUint16(data[y*pitch+x*2:], value)
		}
	}
	return data
}

func (r *Runtime) attachExpandedRGB565Backing(object, buffer uint32, encoded []byte, decoded image.Image) (bool, error) {
	// Legacy game engines commonly reserve room for a 16-bit native surface
	// behind an 8-bit BMP header. The handset helper expands the indexed pixels
	// there in place; the game then renders RGB565 directly into that storage.
	if binary.LittleEndian.Uint16(encoded[28:30]) != 8 {
		return false, nil
	}
	var header [30]byte
	if err := r.cpu.ReadMemory(object, header[:]); err != nil {
		return false, fmt.Errorf("read expanded RGB565 bitmap header: %w", err)
	}
	pixelOffset := binary.LittleEndian.Uint32(encoded[10:14])
	pitch := uint32(binary.LittleEndian.Uint16(header[24:26]))
	height := uint32(binary.LittleEndian.Uint16(header[22:24]))
	allocationSize, allocated := r.heapAllocated[buffer]
	required := uint64(pixelOffset) + uint64(pitch)*uint64(height)
	if !allocated || required > uint64(allocationSize) {
		return false, nil
	}
	pixels := buffer + pixelOffset
	initialPixels := nativeRGB565(decoded, int(pitch))
	if len(initialPixels) != int(pitch*height) {
		return false, fmt.Errorf("expanded RGB565 native-image storage mismatch")
	}
	if err := r.cpu.WriteMemory(pixels, initialPixels); err != nil {
		return false, fmt.Errorf("expand RGB565 native image: %w", err)
	}
	oldPixels := binary.LittleEndian.Uint32(header[8:12])
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], pixels)
	if err := r.cpu.WriteMemory(object+8, pointer[:]); err != nil {
		return false, fmt.Errorf("attach expanded RGB565 native-image pixels: %w", err)
	}
	if oldSize, ok := r.heapAllocated[oldPixels]; ok {
		if err := r.cpu.WriteMemory(oldPixels, make([]byte, oldSize)); err != nil {
			return false, fmt.Errorf("clear detached RGB565 native-image pixels: %w", err)
		}
	}
	r.releaseGuest(oldPixels)
	return true, nil
}

func (r *Runtime) refreshNativeBitmap(object uint32) error {
	native, ok := r.nativeImages[object]
	if !ok {
		return nil
	}
	var encodedHeader [54]byte
	if err := r.cpu.ReadMemory(native.encoded, encodedHeader[:]); err != nil {
		return fmt.Errorf("read live BREW native-image header: %w", err)
	}
	span, valid := nativeBMPSpan(encodedHeader[:])
	if !valid || span != native.span {
		// SetupNativeImage has already copied a decoded bitmap into its own IDIB.
		// Some applets release or reuse the original encoded buffer immediately;
		// stop observing it when its BMP identity/geometry disappears, retaining
		// the last valid pixels rather than decoding unrelated guest memory.
		delete(r.nativeImages, object)
		return nil
	}
	encoded := make([]byte, span)
	if err := r.cpu.ReadMemory(native.encoded, encoded); err != nil {
		return fmt.Errorf("read live BREW native image: %w", err)
	}
	decoded, err := decodeNativeBMP(encoded, span)
	if err != nil {
		return fmt.Errorf("decode live BREW native image: %w", err)
	}
	var bitmapHeader [30]byte
	if err := r.cpu.ReadMemory(object, bitmapHeader[:]); err != nil {
		return fmt.Errorf("read live BREW bitmap header: %w", err)
	}
	width := int(binary.LittleEndian.Uint16(bitmapHeader[20:22]))
	height := int(binary.LittleEndian.Uint16(bitmapHeader[22:24]))
	pitch := int(binary.LittleEndian.Uint16(bitmapHeader[24:26]))
	if decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height || pitch < width*2 {
		// The applet may reshape the returned IDIB for its own blitter. Its
		// dimensions and pitch then belong to the guest, not the encoded BMP;
		// replaying the original pixels would overwrite guest-owned layout.
		delete(r.nativeImages, object)
		return nil
	}
	pixels := binary.LittleEndian.Uint32(bitmapHeader[8:12])
	if pixels == 0 {
		return fmt.Errorf("live BREW native-image bitmap has no pixels")
	}
	if err := r.cpu.WriteMemory(pixels, nativeRGB565(decoded, pitch)); err != nil {
		return fmt.Errorf("write live BREW native-image pixels: %w", err)
	}
	return nil
}

func normalizeNativeBMP(encoded []byte, span uint32) {
	binary.LittleEndian.PutUint32(encoded[2:6], span)
	bitsPerPixel := binary.LittleEndian.Uint16(encoded[28:30])
	if bitsPerPixel <= 8 {
		infoSize := binary.LittleEndian.Uint32(encoded[14:18])
		pixelOffset := binary.LittleEndian.Uint32(encoded[10:14])
		paletteEntries := (pixelOffset - (14 + infoSize)) / 4
		binary.LittleEndian.PutUint32(encoded[46:50], paletteEntries)
	}
}

func decodeNativeBMP(encoded []byte, span uint32) (image.Image, error) {
	// Several handset titles build a full in-memory BMP but leave bfSize at the
	// size of the header's initial allocation. Some also report one fewer palette
	// entry than is present before bfOffBits. The handset decoder trusts the
	// geometry and pixel offset; normalize those redundant fields for the strict
	// host decoder after the bounded span has been established independently.
	normalizeNativeBMP(encoded, span)
	return bmp.Decode(bytes.NewReader(encoded))
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

func (r *Runtime) returnBitmapPixel() error {
	object, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return err
	}
	x, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return err
	}
	y, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return err
	}
	output, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return err
	}
	if err := r.refreshNativeBitmap(object); err != nil {
		return err
	}
	var header [30]byte
	if output == 0 || r.cpu.ReadMemory(object, header[:]) != nil ||
		binary.LittleEndian.Uint32(header[:4]) != bitmapVTable {
		return r.cpu.WriteRegister(cpu.RegisterR0, 2)
	}
	width := uint32(binary.LittleEndian.Uint16(header[20:22]))
	height := uint32(binary.LittleEndian.Uint16(header[22:24]))
	pitch := uint32(binary.LittleEndian.Uint16(header[24:26]))
	pixels := binary.LittleEndian.Uint32(header[8:12])
	if x >= width || y >= height || pixels == 0 || pitch < width*2 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 2)
	}
	var pixel [2]byte
	if err := r.cpu.ReadMemory(pixels+y*pitch+x*2, pixel[:]); err != nil {
		return fmt.Errorf("read BREW bitmap pixel: %w", err)
	}
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], uint32(binary.LittleEndian.Uint16(pixel[:])))
	if err := r.cpu.WriteMemory(output, encoded[:]); err != nil {
		return fmt.Errorf("write BREW bitmap pixel result: %w", err)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, 0)
}

func (r *Runtime) blitBitmapIn() error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return err
	}
	destinationX, _ := r.cpu.ReadRegister(cpu.RegisterR1)
	destinationY, _ := r.cpu.ReadRegister(cpu.RegisterR2)
	width, _ := r.cpu.ReadRegister(cpu.RegisterR3)
	stack, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return err
	}
	var arguments [20]byte
	if err := r.cpu.ReadMemory(stack, arguments[:]); err != nil {
		return fmt.Errorf("read BREW bitmap blit arguments: %w", err)
	}
	height := binary.LittleEndian.Uint32(arguments[0:4])
	source := binary.LittleEndian.Uint32(arguments[4:8])
	sourceX := binary.LittleEndian.Uint32(arguments[8:12])
	sourceY := binary.LittleEndian.Uint32(arguments[12:16])
	if destination == 0 || source == 0 || width == 0 || height == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 2)
	}
	if err := r.refreshNativeBitmap(source); err != nil {
		return err
	}
	var destinationHeader, sourceHeader [30]byte
	if err := r.cpu.ReadMemory(destination, destinationHeader[:]); err != nil {
		return fmt.Errorf("read BREW destination bitmap: %w", err)
	}
	if err := r.cpu.ReadMemory(source, sourceHeader[:]); err != nil {
		return fmt.Errorf("read BREW source bitmap: %w", err)
	}
	if binary.LittleEndian.Uint32(destinationHeader[:4]) != bitmapVTable ||
		binary.LittleEndian.Uint32(sourceHeader[:4]) != bitmapVTable {
		return r.cpu.WriteRegister(cpu.RegisterR0, 2)
	}
	destinationPixels := binary.LittleEndian.Uint32(destinationHeader[8:12])
	destinationWidth := uint32(binary.LittleEndian.Uint16(destinationHeader[20:22]))
	destinationHeight := uint32(binary.LittleEndian.Uint16(destinationHeader[22:24]))
	destinationPitch := uint32(binary.LittleEndian.Uint16(destinationHeader[24:26]))
	sourcePixels := binary.LittleEndian.Uint32(sourceHeader[8:12])
	sourceWidth := uint32(binary.LittleEndian.Uint16(sourceHeader[20:22]))
	sourceHeight := uint32(binary.LittleEndian.Uint16(sourceHeader[22:24]))
	sourcePitch := uint32(binary.LittleEndian.Uint16(sourceHeader[24:26]))
	if sourceX >= sourceWidth || sourceY >= sourceHeight || destinationX >= destinationWidth || destinationY >= destinationHeight ||
		destinationPixels == 0 || sourcePixels == 0 || destinationPitch < destinationWidth*2 || sourcePitch < sourceWidth*2 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 2)
	}
	copyWidth := min(width, min(sourceWidth-sourceX, destinationWidth-destinationX))
	copyHeight := min(height, min(sourceHeight-sourceY, destinationHeight-destinationY))
	rows := make([]byte, copyWidth*copyHeight*2)
	for row := uint32(0); row < copyHeight; row++ {
		span := rows[row*copyWidth*2 : (row+1)*copyWidth*2]
		if err := r.cpu.ReadMemory(sourcePixels+(sourceY+row)*sourcePitch+sourceX*2, span); err != nil {
			return fmt.Errorf("read BREW bitmap blit row: %w", err)
		}
	}
	for row := uint32(0); row < copyHeight; row++ {
		span := rows[row*copyWidth*2 : (row+1)*copyWidth*2]
		if err := r.cpu.WriteMemory(destinationPixels+(destinationY+row)*destinationPitch+destinationX*2, span); err != nil {
			return fmt.Errorf("write BREW bitmap blit row: %w", err)
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, 0)
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
	// IImage::SetParm is void and its fourth argument is the second parameter
	// value, not a status output pointer. Legacy games commonly pass small
	// coordinates, rates, and flags there.
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
	}
	return nil
}

func (r *Runtime) drawBitmapAt(bitmap uint32, destinationX, destinationY int32) error {
	if err := r.refreshNativeBitmap(bitmap); err != nil {
		return err
	}
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
		if targetY < 0 || targetY >= int32(r.screenHeight) {
			continue
		}
		for column := uint32(0); column < width; column++ {
			targetX := destinationX + int32(column)
			if targetX < 0 || targetX >= int32(r.screenWidth) {
				continue
			}
			var pixel [2]byte
			if err := r.cpu.ReadMemory(pixels+row*pitch+column*2, pixel[:]); err != nil {
				return fmt.Errorf("read BREW image pixel: %w", err)
			}
			// Legacy BREW image resources conventionally use RGB565 magenta as
			// their transparent color key. IImage::Draw composites those pixels;
			// it does not paint the key color into the destination surface.
			if binary.LittleEndian.Uint16(pixel[:]) == 0xf81f {
				continue
			}
			address := framebufferBase + (uint32(targetY)*r.screenWidth+uint32(targetX))*2
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
	rop := binary.LittleEndian.Uint32(args[16:])
	if bitmap == 0 || width == 0 || height == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	if err := r.refreshNativeBitmap(bitmap); err != nil {
		return err
	}
	header := make([]byte, 36)
	if err := r.cpu.ReadMemory(bitmap, header); err != nil {
		return fmt.Errorf("read BREW BitBlt bitmap: %w", err)
	}
	if bytes.Equal(header[:2], []byte("BM")) {
		return r.blitDisplayBMP(bitmap, destinationX, destinationY, width, height, sourceX, sourceY, rop)
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
		if targetY < 0 || targetY >= int32(r.screenHeight) {
			continue
		}
		for column := uint32(0); column < copyWidth; column++ {
			targetX := dx + int32(column)
			if targetX < 0 || targetX >= int32(r.screenWidth) {
				continue
			}
			var pixel [2]byte
			sourceAt := pixels + (sourceY+row)*pitch + (sourceX+column)*2
			if err := r.cpu.ReadMemory(sourceAt, pixel[:]); err != nil {
				return fmt.Errorf("read BREW BitBlt pixel: %w", err)
			}
			// AEE_RO_TRANSPARENT composites the RGB565 magenta key over the
			// existing display. AEE_RO_COPY must still copy magenta literally.
			if rop == aeeROTransparent && binary.LittleEndian.Uint16(pixel[:]) == 0xf81f {
				continue
			}
			targetAt := framebufferBase + (uint32(targetY)*r.screenWidth+uint32(targetX))*2
			if err := r.cpu.WriteMemory(targetAt, pixel[:]); err != nil {
				return fmt.Errorf("write BREW BitBlt pixel: %w", err)
			}
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, 0)
}

// Some KTF applets pass an in-memory indexed BMP directly as the IDisplay
// BitBlt source. The native handset accepts that surface without a separate
// IBitmap wrapper; decode it within the same bounded BMP contract used by
// SetupNativeImage, then copy only the requested source rectangle.
func (r *Runtime) blitDisplayBMP(bitmap, destinationX, destinationY, width, height, sourceX, sourceY, rop uint32) error {
	var header [54]byte
	if err := r.cpu.ReadMemory(bitmap, header[:]); err != nil {
		return fmt.Errorf("read BREW raw BitBlt BMP header: %w", err)
	}
	span, ok := nativeBMPSpan(header[:])
	if !ok {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	encoded := make([]byte, span)
	if err := r.cpu.ReadMemory(bitmap, encoded); err != nil {
		return fmt.Errorf("read BREW raw BitBlt BMP: %w", err)
	}
	decoded, err := decodeNativeBMP(encoded, span)
	if err != nil {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	sourceWidth, sourceHeight := uint32(decoded.Bounds().Dx()), uint32(decoded.Bounds().Dy())
	if sourceX >= sourceWidth || sourceY >= sourceHeight {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	copyWidth := min(width, sourceWidth-sourceX)
	copyHeight := min(height, sourceHeight-sourceY)
	dx, dy := int32(destinationX), int32(destinationY)
	for row := uint32(0); row < copyHeight; row++ {
		targetY := dy + int32(row)
		if targetY < 0 || targetY >= int32(r.screenHeight) {
			continue
		}
		for column := uint32(0); column < copyWidth; column++ {
			targetX := dx + int32(column)
			if targetX < 0 || targetX >= int32(r.screenWidth) {
				continue
			}
			red, green, blue, _ := decoded.At(decoded.Bounds().Min.X+int(sourceX+column), decoded.Bounds().Min.Y+int(sourceY+row)).RGBA()
			native := uint16((red>>11)<<11 | (green>>10)<<5 | blue>>11)
			if rop == aeeROTransparent && native == 0xf81f {
				continue
			}
			var pixel [2]byte
			binary.LittleEndian.PutUint16(pixel[:], native)
			targetAt := framebufferBase + (uint32(targetY)*r.screenWidth+uint32(targetX))*2
			if err := r.cpu.WriteMemory(targetAt, pixel[:]); err != nil {
				return fmt.Errorf("write BREW raw BitBlt pixel: %w", err)
			}
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, 0)
}
