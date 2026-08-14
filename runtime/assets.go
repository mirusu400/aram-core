package runtime

import (
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
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
