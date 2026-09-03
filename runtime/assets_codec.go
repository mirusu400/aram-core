package runtime

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
	"strings"
	"time"
)

func decodeImageAsset(
	encoded []byte,
	options DecodeOptions,
	limits AssetLimits,
) ([]image.Image, []time.Duration, int32, string, error) {
	mediaType := strings.ToLower(strings.TrimSpace(options.MediaType))
	isLBMP := mediaType == "image/x-lbmp" ||
		(mediaType == "" && len(encoded) >= 4 && string(encoded[:4]) == "LBMP")
	if isLBMP {
		decoded, err := decodeLBMP(encoded, limits)
		if err != nil {
			return nil, nil, 0, "", err
		}
		return []image.Image{decoded}, []time.Duration{0}, 0, "image/x-lbmp", nil
	}
	isGIF := mediaType == "image/gif" ||
		(mediaType == "" && len(encoded) >= 6 &&
			(string(encoded[:6]) == "GIF87a" || string(encoded[:6]) == "GIF89a"))
	if isGIF {
		config, err := gif.DecodeConfig(bytes.NewReader(encoded))
		if err != nil {
			return nil, nil, 0, "", fmt.Errorf("%w: decode GIF config: %v", ErrInvalidArgument, err)
		}
		frameCount, err := inspectGIFFrames(
			encoded,
			config.Width,
			config.Height,
			limits,
		)
		if err != nil {
			return nil, nil, 0, "", err
		}
		if err := validateDecodedGeometry(
			config.Width,
			config.Height,
			frameCount,
			limits,
		); err != nil {
			return nil, nil, 0, "", err
		}
		animation, err := gif.DecodeAll(bytes.NewReader(encoded))
		if err != nil {
			return nil, nil, 0, "", fmt.Errorf("%w: decode GIF: %v", ErrInvalidArgument, err)
		}
		if len(animation.Image) != frameCount {
			return nil, nil, 0, "", fmt.Errorf(
				"%w: GIF frame count changed during decode",
				ErrInvalidArgument,
			)
		}
		if err := validateDecodedGeometry(
			animation.Config.Width,
			animation.Config.Height,
			len(animation.Image),
			limits,
		); err != nil {
			return nil, nil, 0, "", err
		}
		frames := compositeGIF(animation)
		delays := make([]time.Duration, len(frames))
		for index := range frames {
			delay := animation.Delay[index]
			if delay < 0 || int64(delay) > math.MaxInt64/int64(10*time.Millisecond) {
				return nil, nil, 0, "", fmt.Errorf("%w: invalid GIF frame delay", ErrInvalidArgument)
			}
			delays[index] = time.Duration(delay) * 10 * time.Millisecond
		}
		return frames, delays, int32(animation.LoopCount), "image/gif", nil
	}

	if mediaType == "image/bmp" ||
		(mediaType == "" && len(encoded) >= 2 && string(encoded[:2]) == "BM") {
		decoded, err := decodeBMP(encoded, limits)
		if err != nil {
			return nil, nil, 0, "", err
		}
		return []image.Image{decoded}, []time.Duration{0}, 0, "image/bmp", nil
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(encoded))
	if err != nil {
		return nil, nil, 0, "", fmt.Errorf("%w: decode image config: %v", ErrInvalidArgument, err)
	}
	if mediaType != "" && mediaType != "image/"+strings.ToLower(format) &&
		!(mediaType == "image/jpg" && format == "jpeg") {
		return nil, nil, 0, "", fmt.Errorf("%w: image type does not match %q", ErrInvalidArgument, mediaType)
	}
	if err := validateDecodedGeometry(config.Width, config.Height, 1, limits); err != nil {
		return nil, nil, 0, "", err
	}
	decoded, format, err := image.Decode(bytes.NewReader(encoded))
	if err != nil {
		return nil, nil, 0, "", fmt.Errorf("%w: decode image: %v", ErrInvalidArgument, err)
	}
	return []image.Image{decoded}, []time.Duration{0}, 0, "image/" + strings.ToLower(format), nil
}

// decodeLBMP decodes the little-endian LCD bitmap format used by SKVM.
// Pixels immediately follow the 24-byte header. Optional mask bytes are stored
// in eight-row pages: each page contains one byte per x coordinate, and each
// byte's low-to-high bits cover the page's y coordinates. Fixed-size guest
// buffers may contain additional trailing padding.
func decodeLBMP(encoded []byte, limits AssetLimits) (*image.NRGBA, error) {
	const headerSize = 24
	if len(encoded) < headerSize || string(encoded[:4]) != "LBMP" {
		return nil, fmt.Errorf("%w: truncated LBMP header", ErrInvalidArgument)
	}
	pixelType := binary.LittleEndian.Uint32(encoded[4:8])
	width := binary.LittleEndian.Uint32(encoded[8:12])
	height := binary.LittleEndian.Uint32(encoded[12:16])
	declaredSize := binary.LittleEndian.Uint32(encoded[16:20])
	hasMask := binary.LittleEndian.Uint32(encoded[20:24]) != 0
	var bytesPerPixel uint64
	switch pixelType {
	case 8:
		bytesPerPixel = 1
	case 16:
		bytesPerPixel = 2
	default:
		return nil, fmt.Errorf(
			"%w: unsupported LBMP pixel type %d",
			ErrInvalidArgument,
			pixelType,
		)
	}
	if uint64(width) > uint64(math.MaxInt) || uint64(height) > uint64(math.MaxInt) {
		return nil, fmt.Errorf("%w: LBMP geometry exceeds host limits", ErrLimitExceeded)
	}
	if err := validateDecodedGeometry(int(width), int(height), 1, limits); err != nil {
		return nil, err
	}
	pixelCount := uint64(width) * uint64(height)
	if pixelCount > math.MaxUint64/bytesPerPixel {
		return nil, fmt.Errorf("%w: LBMP pixel size overflows", ErrLimitExceeded)
	}
	pixelBytes := pixelCount * bytesPerPixel
	if uint64(declaredSize) != pixelBytes || pixelBytes > uint64(len(encoded)-headerSize) {
		return nil, fmt.Errorf("%w: invalid LBMP pixel payload", ErrInvalidArgument)
	}
	var mask []byte
	var maskPageStride uint64
	if hasMask {
		maskPageStride = uint64(width)
		maskPages := (uint64(height) + 7) / 8
		maskBytes := maskPageStride * maskPages
		remaining := uint64(len(encoded)-headerSize) - pixelBytes
		if maskBytes > remaining {
			return nil, fmt.Errorf("%w: truncated LBMP mask", ErrInvalidArgument)
		}
		mask = encoded[headerSize+int(pixelBytes) : headerSize+int(pixelBytes+maskBytes)]
	}
	decoded := image.NewNRGBA(image.Rect(0, 0, int(width), int(height)))
	payload := encoded[headerSize : headerSize+int(pixelBytes)]
	for index := uint64(0); index < pixelCount; index++ {
		destination := int(index) * 4
		if pixelType == 8 {
			value := payload[index]
			decoded.Pix[destination] = ((value >> 5) & 0x07) * 36
			decoded.Pix[destination+1] = ((value >> 2) & 0x07) * 36
			decoded.Pix[destination+2] = (value & 0x03) * 85
		} else {
			source := int(index) * 2
			value := binary.LittleEndian.Uint16(payload[source : source+2])
			decoded.Pix[destination] = expand5(uint8((value >> 11) & 0x1f))
			decoded.Pix[destination+1] = expand6(uint8((value >> 5) & 0x3f))
			decoded.Pix[destination+2] = expand5(uint8(value & 0x1f))
		}
		alpha := byte(0xff)
		if hasMask {
			x := index % uint64(width)
			y := index / uint64(width)
			maskIndex := (y/8)*maskPageStride + x
			if mask[maskIndex]&(1<<uint(y%8)) != 0 {
				alpha = 0
			}
		}
		decoded.Pix[destination+3] = alpha
	}
	return decoded, nil
}

// inspectGIFFrames walks only the GIF container structure. It establishes the
// frame count and aggregate decoded bound before gif.DecodeAll allocates one
// paletted image per frame.
func inspectGIFFrames(
	encoded []byte,
	logicalWidth, logicalHeight int,
	limits AssetLimits,
) (int, error) {
	if len(encoded) < 13 ||
		(string(encoded[:6]) != "GIF87a" && string(encoded[:6]) != "GIF89a") {
		return 0, fmt.Errorf("%w: truncated GIF header", ErrInvalidArgument)
	}
	if logicalWidth <= 0 || logicalHeight <= 0 {
		return 0, fmt.Errorf("%w: invalid GIF logical screen", ErrInvalidArgument)
	}
	offset := 13
	skipColorTable := func(packed byte) bool {
		if packed&0x80 == 0 {
			return true
		}
		size := 3 * (1 << (uint(packed&0x07) + 1))
		if size > len(encoded)-offset {
			return false
		}
		offset += size
		return true
	}
	skipSubBlocks := func() bool {
		for {
			if offset >= len(encoded) {
				return false
			}
			size := int(encoded[offset])
			offset++
			if size == 0 {
				return true
			}
			if size > len(encoded)-offset {
				return false
			}
			offset += size
		}
	}
	if !skipColorTable(encoded[10]) {
		return 0, fmt.Errorf("%w: truncated GIF color table", ErrInvalidArgument)
	}

	frames := uint32(0)
	var framePixels uint64
	for offset < len(encoded) {
		block := encoded[offset]
		offset++
		switch block {
		case 0x2c:
			if len(encoded)-offset < 9 {
				return 0, fmt.Errorf("%w: truncated GIF image descriptor", ErrInvalidArgument)
			}
			left := uint32(binary.LittleEndian.Uint16(encoded[offset:]))
			top := uint32(binary.LittleEndian.Uint16(encoded[offset+2:]))
			width := uint32(binary.LittleEndian.Uint16(encoded[offset+4:]))
			height := uint32(binary.LittleEndian.Uint16(encoded[offset+6:]))
			if width == 0 || height == 0 ||
				left+width > uint32(logicalWidth) ||
				top+height > uint32(logicalHeight) {
				return 0, fmt.Errorf(
					"%w: GIF frame is outside the logical screen",
					ErrInvalidArgument,
				)
			}
			pixels := uint64(width) * uint64(height)
			if pixels > limits.MaxDecodedBytes ||
				framePixels > limits.MaxDecodedBytes-pixels {
				return 0, fmt.Errorf(
					"%w: GIF frame pixels exceed decoded byte limit",
					ErrLimitExceeded,
				)
			}
			framePixels += pixels
			packed := encoded[offset+8]
			offset += 9
			if !skipColorTable(packed) || offset >= len(encoded) {
				return 0, fmt.Errorf("%w: truncated GIF image data", ErrInvalidArgument)
			}
			offset++ // LZW minimum code size.
			if !skipSubBlocks() {
				return 0, fmt.Errorf("%w: truncated GIF image data", ErrInvalidArgument)
			}
			if frames == limits.MaxFrames {
				return 0, fmt.Errorf(
					"%w: GIF frame count exceeds %d",
					ErrLimitExceeded,
					limits.MaxFrames,
				)
			}
			frames++
		case 0x21:
			if offset >= len(encoded) {
				return 0, fmt.Errorf("%w: truncated GIF extension", ErrInvalidArgument)
			}
			offset++ // Extension label.
			if !skipSubBlocks() {
				return 0, fmt.Errorf("%w: truncated GIF extension", ErrInvalidArgument)
			}
		case 0x3b:
			if frames == 0 {
				return 0, fmt.Errorf("%w: GIF has no frames", ErrInvalidArgument)
			}
			return int(frames), nil
		default:
			return 0, fmt.Errorf(
				"%w: invalid GIF block 0x%02x",
				ErrInvalidArgument,
				block,
			)
		}
	}
	return 0, fmt.Errorf("%w: GIF trailer is missing", ErrInvalidArgument)
}

func validateDecodedGeometry(width, height, frames int, limits AssetLimits) error {
	if width <= 0 || height <= 0 || frames <= 0 ||
		frames > int(limits.MaxFrames) {
		return fmt.Errorf("%w: invalid decoded image geometry", ErrInvalidArgument)
	}
	if uint64(width) > math.MaxUint64/uint64(height) {
		return fmt.Errorf("%w: decoded image geometry overflows", ErrLimitExceeded)
	}
	bytes := uint64(width) * uint64(height)
	if bytes > math.MaxUint64/4 || bytes*4 > math.MaxUint64/uint64(frames) ||
		bytes*4*uint64(frames) > limits.MaxDecodedBytes ||
		bytes*4*uint64(frames) > uint64(math.MaxInt) {
		return fmt.Errorf("%w: decoded image exceeds byte limit", ErrLimitExceeded)
	}
	return nil
}

// compositeGIF flattens a GIF's frames into standalone images. The canvas
// starts transparent and a frame disposed to the background clears back to
// transparent rather than to the file's background color: a WIPI title's
// sprite sheet keys its background out with the transparent index, and
// painting the background color there turns every keyed-out pixel opaque -
// 고기집타이쿤's GUI sheets name 0xff4955, so its whole overlay arrived as a
// magenta plate (issue #134). Web decoders resolve the background the same
// way, and a sheet that really wants an opaque backdrop paints one.
func compositeGIF(animation *gif.GIF) []image.Image {
	width, height := animation.Config.Width, animation.Config.Height
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	transparent := &image.Uniform{C: color.RGBA{}}
	result := make([]image.Image, 0, len(animation.Image))
	var previous *image.RGBA
	for index, frame := range animation.Image {
		if index != 0 {
			switch animation.Disposal[index-1] {
			case gif.DisposalBackground:
				draw.Draw(
					canvas,
					animation.Image[index-1].Bounds(),
					transparent,
					image.Point{},
					draw.Src,
				)
			case gif.DisposalPrevious:
				if previous != nil {
					copy(canvas.Pix, previous.Pix)
				}
			}
		}
		if animation.Disposal[index] == gif.DisposalPrevious {
			previous = image.NewRGBA(canvas.Bounds())
			copy(previous.Pix, canvas.Pix)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		snapshot := image.NewRGBA(canvas.Bounds())
		copy(snapshot.Pix, canvas.Pix)
		result = append(result, snapshot)
	}
	return result
}

func decodeBMP(encoded []byte, limits AssetLimits) (*image.NRGBA, error) {
	if len(encoded) < 54 || string(encoded[:2]) != "BM" {
		return nil, fmt.Errorf("%w: truncated BMP header", ErrInvalidArgument)
	}
	pixelOffset := uint64(binary.LittleEndian.Uint32(encoded[10:14]))
	dibSize := binary.LittleEndian.Uint32(encoded[14:18])
	if dibSize < 40 || uint64(14)+uint64(dibSize) > uint64(len(encoded)) {
		return nil, fmt.Errorf("%w: unsupported BMP header", ErrInvalidArgument)
	}
	width := int64(int32(binary.LittleEndian.Uint32(encoded[18:22])))
	rawHeight := int64(int32(binary.LittleEndian.Uint32(encoded[22:26])))
	planes := binary.LittleEndian.Uint16(encoded[26:28])
	bits := binary.LittleEndian.Uint16(encoded[28:30])
	compression := binary.LittleEndian.Uint32(encoded[30:34])
	if width <= 0 || rawHeight == 0 || rawHeight == math.MinInt32 ||
		planes != 1 || compression != 0 ||
		(bits != 1 && bits != 4 && bits != 8 && bits != 24 && bits != 32) {
		return nil, fmt.Errorf("%w: unsupported BMP geometry or format", ErrInvalidArgument)
	}
	height := rawHeight
	topDown := height < 0
	if topDown {
		height = -height
	}
	if width > math.MaxInt || height > math.MaxInt {
		return nil, fmt.Errorf("%w: BMP geometry exceeds host limits", ErrLimitExceeded)
	}
	if err := validateDecodedGeometry(int(width), int(height), 1, limits); err != nil {
		return nil, err
	}
	if uint64(width) > math.MaxUint64/uint64(bits) {
		return nil, fmt.Errorf("%w: BMP row size overflows", ErrLimitExceeded)
	}
	rowBits := uint64(width) * uint64(bits)
	rowBytes := ((rowBits + 31) / 32) * 4
	if rowBytes > math.MaxUint64/uint64(height) ||
		pixelOffset > uint64(len(encoded)) ||
		rowBytes*uint64(height) > uint64(len(encoded))-pixelOffset {
		return nil, fmt.Errorf("%w: truncated BMP pixels", ErrInvalidArgument)
	}
	var palette []color.NRGBA
	if bits <= 8 {
		count := binary.LittleEndian.Uint32(encoded[46:50])
		if count == 0 {
			count = 1 << bits
		}
		paletteStart := uint64(14) + uint64(dibSize)
		if count > 256 || paletteStart > pixelOffset ||
			uint64(count)*4 > pixelOffset-paletteStart {
			return nil, fmt.Errorf("%w: invalid BMP palette", ErrInvalidArgument)
		}
		palette = make([]color.NRGBA, count)
		for index := range palette {
			offset := paletteStart + uint64(index)*4
			palette[index] = color.NRGBA{
				R: encoded[offset+2],
				G: encoded[offset+1],
				B: encoded[offset],
				A: 0xff,
			}
		}
		// Handset packages ship paletted sprites that reserve one palette
		// entry as a transparent color key. The file header's first reserved
		// field — which the BMP format requires to be zero — flags such a
		// sprite, and biClrImportant then holds that entry's index instead of
		// a count. Every marked sprite in 메이플스토리 도적편 points the index
		// at the same green and paints its border with it, so honoring the
		// flag is what keeps a sprite's surround from covering the background
		// with a solid green block. The entry keeps its color and only loses
		// its alpha: a color-keyed consumer that cannot carry alpha needs to
		// know which color the key is. An index outside the palette is stale
		// header noise — the stock KTF Annunciator.bmp carries index 304 for a
		// 256-entry palette — and leaves the image opaque.
		if binary.LittleEndian.Uint16(encoded[6:8]) != 0 {
			key := binary.LittleEndian.Uint32(encoded[50:54])
			if key < uint32(len(palette)) {
				palette[key].A = 0
			}
		}
	}
	result := image.NewNRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		sourceY := y
		if !topDown {
			sourceY = int(height) - 1 - y
		}
		row := encoded[pixelOffset+uint64(sourceY)*rowBytes:]
		for x := 0; x < int(width); x++ {
			var value color.NRGBA
			switch bits {
			case 1, 4, 8:
				var index int
				switch bits {
				case 1:
					index = int(row[x/8] >> (7 - uint(x%8)) & 1)
				case 4:
					index = int(row[x/2])
					if x%2 == 0 {
						index >>= 4
					} else {
						index &= 0x0f
					}
				case 8:
					index = int(row[x])
				}
				if index >= len(palette) {
					return nil, fmt.Errorf("%w: BMP palette index out of range", ErrInvalidArgument)
				}
				value = palette[index]
			case 24:
				offset := x * 3
				value = color.NRGBA{R: row[offset+2], G: row[offset+1], B: row[offset], A: 0xff}
			case 32:
				offset := x * 4
				// BI_RGB's fourth byte is reserved, not an alpha channel.
				value = color.NRGBA{R: row[offset+2], G: row[offset+1], B: row[offset], A: 0xff}
			}
			result.SetNRGBA(x, y, value)
		}
	}
	return result, nil
}

type boundedAssetBuffer struct {
	bytes.Buffer
	limit uint64
}

func (b *boundedAssetBuffer) Write(data []byte) (int, error) {
	if uint64(b.Len()) > b.limit ||
		uint64(len(data)) > b.limit-uint64(b.Len()) {
		return 0, fmt.Errorf("encoded asset exceeds %d bytes", b.limit)
	}
	return b.Buffer.Write(data)
}

func encodeBMP24(source *image.NRGBA, limit uint64) ([]byte, error) {
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	rowBytes := (uint64(width)*3 + 3) &^ 3
	if rowBytes > math.MaxUint64/uint64(height) {
		return nil, fmt.Errorf("%w: encoded BMP size overflows", ErrLimitExceeded)
	}
	total := uint64(54) + rowBytes*uint64(height)
	if total > limit || total > uint64(math.MaxInt) ||
		total > math.MaxUint32 {
		return nil, fmt.Errorf("%w: encoded BMP exceeds byte limit", ErrLimitExceeded)
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
	binary.LittleEndian.PutUint32(
		encoded[34:38],
		uint32(rowBytes*uint64(height)),
	)
	for destinationY := 0; destinationY < height; destinationY++ {
		sourceY := height - 1 - destinationY
		destination := 54 + uint64(destinationY)*rowBytes
		for x := 0; x < width; x++ {
			sourceOffset := sourceY*source.Stride + x*4
			outputOffset := destination + uint64(x)*3
			encoded[outputOffset] = source.Pix[sourceOffset+2]
			encoded[outputOffset+1] = source.Pix[sourceOffset+1]
			encoded[outputOffset+2] = source.Pix[sourceOffset]
		}
	}
	return encoded, nil
}

func validAssetMediaType(mediaType string) bool {
	switch mediaType {
	case "image/bmp", "image/png", "image/jpeg", "image/gif", "image/x-lbmp":
		return true
	default:
		return false
	}
}

func assetMediaTypeMatchesRequest(actual, requested string) bool {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" || requested == actual {
		return true
	}
	return requested == "image/jpg" && actual == "image/jpeg"
}
