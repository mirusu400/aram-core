package runtime

import (
	"crypto/sha256"
	"image"
	"time"
)

// Recreating a released image must recreate its guest handles and surfaces,
// but its immutable decode may be reused. This host-only cache owns no guest
// resources and is deliberately absent from save states.
const assetPixelCacheBytes = uint64(16 << 20)
const assetPixelCacheEntries = 128

type assetPixelKey struct {
	digest    [sha256.Size]byte
	mediaType string
}

type assetPixelFrame struct {
	width, height int32
	delay         time.Duration
	pixels        []byte
	source        image.Image // Used only while materializing a cold decode.
}

type assetPixelDecode struct {
	frames      []assetPixelFrame
	mediaType   string
	loopCount   int32
	bytes, used uint64
	cacheable   bool
}

func (a *Assets) decodePixelAsset(digest [sha256.Size]byte, encoded []byte, options DecodeOptions) (*assetPixelDecode, error) {
	key := assetPixelKey{digest, options.MediaType}
	if cached := a.decodedPixels[key]; cached != nil {
		a.decodedPixelClock++
		cached.used = a.decodedPixelClock
		return cached, nil
	}
	frames, delays, loops, mediaType, err := decodeImageAsset(encoded, options, a.limits)
	if err != nil {
		return nil, err
	}
	decoded := &assetPixelDecode{mediaType: mediaType, loopCount: loops, frames: make([]assetPixelFrame, len(frames))}
	for index, frame := range frames {
		bounds := frame.Bounds()
		decoded.frames[index] = assetPixelFrame{width: int32(bounds.Dx()), height: int32(bounds.Dy()), delay: delays[index], source: frame}
		decoded.bytes += uint64(bounds.Dx()) * uint64(bounds.Dy()) * 4
	}
	decoded.cacheable = decoded.bytes <= min(assetPixelCacheBytes, a.limits.MaxDecodedBytes)
	return decoded, nil
}

func (a *Assets) rememberPixelAsset(key assetPixelKey, decoded *assetPixelDecode) {
	if !decoded.cacheable || a.decodedPixels[key] == decoded {
		return
	}
	limit := min(assetPixelCacheBytes, a.limits.MaxDecodedBytes)
	if a.decodedPixels == nil {
		a.decodedPixels = make(map[assetPixelKey]*assetPixelDecode)
	}
	for len(a.decodedPixels) >= assetPixelCacheEntries || a.decodedPixelBytes+decoded.bytes > limit {
		var oldestKey assetPixelKey
		var oldest *assetPixelDecode
		for k, value := range a.decodedPixels {
			if oldest == nil || value.used < oldest.used {
				oldestKey, oldest = k, value
			}
		}
		if oldest == nil {
			return
		}
		delete(a.decodedPixels, oldestKey)
		a.decodedPixelBytes -= oldest.bytes
	}
	a.decodedPixelClock++
	decoded.used = a.decodedPixelClock
	a.decodedPixels[key] = decoded
	a.decodedPixelBytes += decoded.bytes
}
