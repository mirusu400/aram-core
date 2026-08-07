package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"sort"
	"strings"
	"time"
)

type AssetLimits struct {
	MaxAssets       uint32
	MaxFrames       uint32
	MaxEncodedBytes uint64
	MaxDecodedBytes uint64
}

func DefaultAssetLimits() AssetLimits {
	return AssetLimits{
		MaxAssets:       1024,
		MaxFrames:       256,
		MaxEncodedBytes: 64 << 20,
		MaxDecodedBytes: 256 << 20,
	}
}

func (l AssetLimits) Validate() error {
	if l.MaxAssets == 0 || l.MaxFrames == 0 ||
		l.MaxEncodedBytes == 0 || l.MaxDecodedBytes == 0 {
		return fmt.Errorf("%w: invalid asset limits", ErrInvalidArgument)
	}
	return nil
}

type DecodeOptions struct {
	// MediaType may constrain decoding. Empty means sniff the bounded input.
	MediaType string
}

func (o DecodeOptions) validate() error {
	if len(o.MediaType) > 127 || strings.IndexByte(o.MediaType, 0) >= 0 {
		return fmt.Errorf("%w: invalid media type", ErrInvalidArgument)
	}
	return nil
}

type AssetFrame struct {
	Surface ServiceID
	Delay   time.Duration
}

type AssetInfo struct {
	ID        ServiceID
	Owner     OwnerID
	Digest    [sha256.Size]byte
	MediaType string
	Width     int32
	Height    int32
	LoopCount int32
	Frames    []AssetFrame
}

type AssetFrameState struct {
	Surface ServiceID
	DelayNS int64
}

type AssetState struct {
	ID                 ServiceID
	Owner              OwnerID
	Digest             [sha256.Size]byte
	MediaType          string
	RequestedMediaType string
	Width              int32
	Height             int32
	LoopCount          int32
	Frames             []AssetFrameState
}

type AssetsState struct {
	Limits AssetLimits
	Assets []AssetState
}

type decodedAsset struct {
	info     AssetInfo
	cacheKey string
	options  DecodeOptions
}

// Assets owns bounded image decoding and immutable decoded-frame caches.
// Encoded guest bytes never remain aliased after Decode returns.
type Assets struct {
	registry *Registry
	graphics *Graphics
	limits   AssetLimits
	assets   map[ServiceID]*decodedAsset
	cache    map[string]ServiceID
}

func NewAssets(
	registry *Registry,
	graphics *Graphics,
	limits AssetLimits,
) (*Assets, error) {
	if registry == nil || graphics == nil {
		return nil, fmt.Errorf("%w: asset dependencies are nil", ErrInvalidArgument)
	}
	if limits == (AssetLimits{}) {
		limits = DefaultAssetLimits()
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Assets{
		registry: registry,
		graphics: graphics,
		limits:   limits,
		assets:   make(map[ServiceID]*decodedAsset),
		cache:    make(map[string]ServiceID),
	}, nil
}

func (a *Assets) Decode(
	owner OwnerID,
	encoded []byte,
	options DecodeOptions,
) (ServiceID, error) {
	options.MediaType = strings.ToLower(strings.TrimSpace(options.MediaType))
	if err := options.validate(); err != nil {
		return 0, err
	}
	if len(encoded) == 0 || uint64(len(encoded)) > a.limits.MaxEncodedBytes {
		return 0, fmt.Errorf("%w: encoded asset size %d", ErrLimitExceeded, len(encoded))
	}
	digest := sha256.Sum256(encoded)
	cacheKey := assetCacheKey(owner, digest, options)
	if id := a.cache[cacheKey]; id != 0 {
		if err := a.registry.Validate(id, owner, KindImage); err != nil {
			return 0, fmt.Errorf("%w: invalid asset cache entry", ErrInvalidState)
		}
		if err := a.registry.Retain(id); err != nil {
			return 0, err
		}
		return id, nil
	}
	if uint32(len(a.assets)) >= a.limits.MaxAssets {
		return 0, fmt.Errorf("%w: asset count reached %d", ErrLimitExceeded, a.limits.MaxAssets)
	}

	frames, delays, loopCount, mediaType, err := decodeImageAsset(encoded, options, a.limits)
	if err != nil {
		return 0, err
	}
	id, err := a.registry.Create(owner, KindImage)
	if err != nil {
		return 0, err
	}
	surfaces := make([]ServiceID, 0, len(frames))
	rollback := func() {
		for _, surface := range surfaces {
			_ = a.graphics.DestroySurface(owner, surface)
		}
		_ = a.registry.Destroy(id, owner, KindImage)
	}
	for _, frame := range frames {
		bounds := frame.Bounds()
		surface, createErr := a.graphics.CreateSurface(owner, SurfaceDescriptor{
			Width:  int32(bounds.Dx()),
			Height: int32(bounds.Dy()),
			Format: PixelRGBA8888,
		})
		if createErr != nil {
			rollback()
			return 0, createErr
		}
		surfaces = append(surfaces, surface)
		pixels := rgbaBytes(frame)
		if replaceErr := a.graphics.ReplacePixels(owner, surface, pixels); replaceErr != nil {
			rollback()
			return 0, replaceErr
		}
	}
	info := AssetInfo{
		ID:        id,
		Owner:     owner,
		Digest:    digest,
		MediaType: mediaType,
		Width:     int32(frames[0].Bounds().Dx()),
		Height:    int32(frames[0].Bounds().Dy()),
		LoopCount: loopCount,
		Frames:    make([]AssetFrame, len(surfaces)),
	}
	for index, surface := range surfaces {
		info.Frames[index] = AssetFrame{Surface: surface, Delay: delays[index]}
	}
	a.assets[id] = &decodedAsset{info: info, cacheKey: cacheKey, options: options}
	a.cache[cacheKey] = id
	return id, nil
}

func (a *Assets) Retain(owner OwnerID, id ServiceID) error {
	if _, err := a.get(owner, id); err != nil {
		return err
	}
	return a.registry.Retain(id)
}

func (a *Assets) Release(owner OwnerID, id ServiceID) error {
	asset, err := a.get(owner, id)
	if err != nil {
		return err
	}
	entry, err := a.registry.lookup(id)
	if err != nil {
		return err
	}
	if entry.refs == 1 {
		for _, frame := range asset.info.Frames {
			if frame.Surface == a.graphics.Screen() {
				return fmt.Errorf(
					"%w: asset %s frame is the active screen",
					ErrInvalidState,
					id,
				)
			}
			if _, err := a.graphics.get(frame.Surface, owner); err != nil {
				return fmt.Errorf(
					"release asset %s surface %s: %w",
					id,
					frame.Surface,
					err,
				)
			}
		}
	}
	destroy, err := a.registry.Release(id)
	if err != nil || !destroy {
		return err
	}
	for _, frame := range asset.info.Frames {
		if err := a.graphics.DestroySurface(owner, frame.Surface); err != nil {
			return fmt.Errorf("release asset %s surface %s: %w", id, frame.Surface, err)
		}
	}
	delete(a.cache, asset.cacheKey)
	delete(a.assets, id)
	return nil
}

func (a *Assets) Info(owner OwnerID, id ServiceID) (AssetInfo, error) {
	asset, err := a.get(owner, id)
	if err != nil {
		return AssetInfo{}, err
	}
	return cloneAssetInfo(asset.info), nil
}

// EncodeSurface encodes a bounded surface region. An empty region selects the
// whole surface. BMP, PNG, JPEG/JPG, and single-frame GIF are deterministic
// headless encoders.
func (a *Assets) EncodeSurface(
	owner OwnerID,
	surfaceID ServiceID,
	mediaType string,
	region Rectangle,
) ([]byte, error) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	switch mediaType {
	case "image/bmp", "image/png", "image/jpeg", "image/jpg", "image/gif":
	default:
		return nil, fmt.Errorf(
			"%w: unsupported encoded image type %q",
			ErrInvalidArgument,
			mediaType,
		)
	}
	descriptor, err := a.graphics.Descriptor(owner, surfaceID)
	if err != nil {
		return nil, err
	}
	bounds := Rectangle{
		Width:  descriptor.Width,
		Height: descriptor.Height,
	}
	if region == (Rectangle{}) {
		region = bounds
	}
	if !region.Valid() || region.Empty() || region.Intersect(bounds) != region {
		return nil, fmt.Errorf("%w: encoded image region is out of bounds", ErrInvalidArgument)
	}
	if err := validateDecodedGeometry(
		int(region.Width),
		int(region.Height),
		1,
		a.limits,
	); err != nil {
		return nil, err
	}
	pixels, err := a.graphics.RGBA(owner, surfaceID)
	if err != nil {
		return nil, err
	}
	imageValue := image.NewNRGBA(image.Rect(
		0,
		0,
		int(region.Width),
		int(region.Height),
	))
	for y := int32(0); y < region.Height; y++ {
		source := (int(region.Y+y)*int(descriptor.Width) + int(region.X)) * 4
		destination := int(y) * imageValue.Stride
		copy(
			imageValue.Pix[destination:destination+int(region.Width)*4],
			pixels[source:source+int(region.Width)*4],
		)
	}
	if mediaType == "image/bmp" {
		return encodeBMP24(imageValue, a.limits.MaxEncodedBytes)
	}
	output := &boundedAssetBuffer{limit: a.limits.MaxEncodedBytes}
	switch mediaType {
	case "image/png":
		err = png.Encode(output, imageValue)
	case "image/jpeg", "image/jpg":
		err = jpeg.Encode(output, imageValue, &jpeg.Options{Quality: 90})
	case "image/gif":
		err = gif.Encode(output, imageValue, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: encode %s: %v", ErrLimitExceeded, mediaType, err)
	}
	return cloneBytes(output.Bytes()), nil
}

func (a *Assets) Snapshot() AssetsState {
	state := AssetsState{Limits: a.limits}
	ids := make([]ServiceID, 0, len(a.assets))
	for id := range a.assets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		asset := a.assets[id]
		info := asset.info
		saved := AssetState{
			ID:                 info.ID,
			Owner:              info.Owner,
			Digest:             info.Digest,
			MediaType:          info.MediaType,
			RequestedMediaType: asset.options.MediaType,
			Width:              info.Width,
			Height:             info.Height,
			LoopCount:          info.LoopCount,
		}
		for _, frame := range info.Frames {
			saved.Frames = append(saved.Frames, AssetFrameState{
				Surface: frame.Surface,
				DelayNS: int64(frame.Delay),
			})
		}
		state.Assets = append(state.Assets, saved)
	}
	return state
}

func (a *Assets) Restore(state AssetsState) error {
	if err := state.Limits.Validate(); err != nil ||
		len(state.Assets) > int(state.Limits.MaxAssets) {
		return fmt.Errorf("%w: invalid asset state limits", ErrInvalidState)
	}
	assets := make(map[ServiceID]*decodedAsset, len(state.Assets))
	cache := make(map[string]ServiceID, len(state.Assets))
	usedSurfaces := make(map[ServiceID]ServiceID)
	var previous ServiceID
	for index, saved := range state.Assets {
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previous) ||
			saved.Width <= 0 || saved.Height <= 0 ||
			len(saved.Frames) == 0 || len(saved.Frames) > int(state.Limits.MaxFrames) ||
			saved.LoopCount < -1 ||
			!validAssetMediaType(saved.MediaType) ||
			(DecodeOptions{MediaType: saved.RequestedMediaType}).validate() != nil ||
			saved.RequestedMediaType !=
				strings.ToLower(strings.TrimSpace(saved.RequestedMediaType)) ||
			!assetMediaTypeMatchesRequest(
				saved.MediaType,
				saved.RequestedMediaType,
			) ||
			a.registry.Validate(saved.ID, saved.Owner, KindImage) != nil {
			return fmt.Errorf("%w: invalid asset %d", ErrInvalidState, index)
		}
		if err := validateDecodedGeometry(
			int(saved.Width),
			int(saved.Height),
			len(saved.Frames),
			state.Limits,
		); err != nil {
			return fmt.Errorf("%w: invalid asset %d geometry", ErrInvalidState, index)
		}
		info := AssetInfo{
			ID:        saved.ID,
			Owner:     saved.Owner,
			Digest:    saved.Digest,
			MediaType: saved.MediaType,
			Width:     saved.Width,
			Height:    saved.Height,
			LoopCount: saved.LoopCount,
		}
		seenSurfaces := make(map[ServiceID]struct{}, len(saved.Frames))
		for frameIndex, frame := range saved.Frames {
			descriptor, err := a.graphics.Descriptor(saved.Owner, frame.Surface)
			if err != nil || descriptor.Width != saved.Width ||
				descriptor.Height != saved.Height ||
				descriptor.Format != PixelRGBA8888 ||
				frame.DelayNS < 0 {
				return fmt.Errorf(
					"%w: invalid asset %d frame %d",
					ErrInvalidState,
					index,
					frameIndex,
				)
			}
			if _, duplicate := seenSurfaces[frame.Surface]; duplicate {
				return fmt.Errorf("%w: duplicate asset frame surface", ErrInvalidState)
			}
			if other := usedSurfaces[frame.Surface]; other != 0 {
				return fmt.Errorf(
					"%w: surface %s belongs to assets %s and %s",
					ErrInvalidState,
					frame.Surface,
					other,
					saved.ID,
				)
			}
			seenSurfaces[frame.Surface] = struct{}{}
			usedSurfaces[frame.Surface] = saved.ID
			info.Frames = append(info.Frames, AssetFrame{
				Surface: frame.Surface,
				Delay:   time.Duration(frame.DelayNS),
			})
		}
		options := DecodeOptions{MediaType: saved.RequestedMediaType}
		key := assetCacheKey(saved.Owner, saved.Digest, options)
		if cache[key] != 0 {
			return fmt.Errorf("%w: duplicate asset cache key", ErrInvalidState)
		}
		assets[saved.ID] = &decodedAsset{
			info:     info,
			cacheKey: key,
			options:  options,
		}
		cache[key] = saved.ID
		previous = saved.ID
	}
	a.limits = state.Limits
	a.assets = assets
	a.cache = cache
	return nil
}

func (a *Assets) get(owner OwnerID, id ServiceID) (*decodedAsset, error) {
	if err := a.registry.Validate(id, owner, KindImage); err != nil {
		return nil, err
	}
	asset := a.assets[id]
	if asset == nil {
		return nil, fmt.Errorf("%w: asset %s", ErrInvalidState, id)
	}
	return asset, nil
}

func assetCacheKey(owner OwnerID, digest [sha256.Size]byte, options DecodeOptions) string {
	return fmt.Sprintf("%08x:%x:%s", owner, digest, strings.ToLower(options.MediaType))
}

func cloneAssetInfo(info AssetInfo) AssetInfo {
	info.Frames = append([]AssetFrame(nil), info.Frames...)
	return info
}

func rgbaBytes(source image.Image) []byte {
	bounds := source.Bounds()
	result := make([]byte, bounds.Dx()*bounds.Dy()*4)
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			value := color.NRGBAModel.Convert(
				source.At(bounds.Min.X+x, bounds.Min.Y+y),
			).(color.NRGBA)
			offset := (y*bounds.Dx() + x) * 4
			result[offset] = value.R
			result[offset+1] = value.G
			result[offset+2] = value.B
			result[offset+3] = value.A
		}
	}
	return result
}

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

func compositeGIF(animation *gif.GIF) []image.Image {
	width, height := animation.Config.Width, animation.Config.Height
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	background := color.RGBA{}
	if palette, ok := animation.Config.ColorModel.(color.Palette); ok {
		if index := int(animation.BackgroundIndex); index < len(palette) {
			background = color.RGBAModel.Convert(palette[index]).(color.RGBA)
		}
	}
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)
	result := make([]image.Image, 0, len(animation.Image))
	var previous *image.RGBA
	for index, frame := range animation.Image {
		if index != 0 {
			switch animation.Disposal[index-1] {
			case gif.DisposalBackground:
				draw.Draw(
					canvas,
					animation.Image[index-1].Bounds(),
					&image.Uniform{C: background},
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

func decodeBMP(encoded []byte, limits AssetLimits) (*image.RGBA, error) {
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
	var palette []color.RGBA
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
		palette = make([]color.RGBA, count)
		for index := range palette {
			offset := paletteStart + uint64(index)*4
			palette[index] = color.RGBA{
				R: encoded[offset+2],
				G: encoded[offset+1],
				B: encoded[offset],
				A: 0xff,
			}
		}
	}
	result := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		sourceY := y
		if !topDown {
			sourceY = int(height) - 1 - y
		}
		row := encoded[pixelOffset+uint64(sourceY)*rowBytes:]
		for x := 0; x < int(width); x++ {
			var value color.RGBA
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
				value = color.RGBA{R: row[offset+2], G: row[offset+1], B: row[offset], A: 0xff}
			case 32:
				offset := x * 4
				// BI_RGB's fourth byte is reserved, not an alpha channel.
				value = color.RGBA{R: row[offset+2], G: row[offset+1], B: row[offset], A: 0xff}
			}
			result.SetRGBA(x, y, value)
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
