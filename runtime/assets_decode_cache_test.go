package runtime

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"
)

func TestAssetPixelCacheRetainsFreshResourceSemantics(t *testing.T) {
	hot, err := NewServices(Config{})
	check(t, err)
	cold, err := NewServices(Config{})
	check(t, err)
	encoded := testPNG(t)
	for repeat := 0; repeat < 4; repeat++ {
		cold.Assets.decodedPixels = nil
		cold.Assets.decodedPixelBytes = 0
		for _, services := range []*Services{hot, cold} {
			id, err := services.Assets.Decode(7, encoded, DecodeOptions{})
			check(t, err)
			info, err := services.Assets.Info(7, id)
			check(t, err)
			pixels, err := services.Graphics.RGBA(7, info.Frames[0].Surface)
			check(t, err)
			if !bytes.Equal(pixels, []byte{255, 0, 0, 255, 0, 255, 0, 128}) {
				t.Fatal("cached decode was mutated through a live surface")
			}
			check(t, services.Graphics.ReplacePixels(7, info.Frames[0].Surface, make([]byte, len(pixels))))
			check(t, services.Assets.Release(7, id))
		}
		if !reflect.DeepEqual(hot.Snapshot(), cold.Snapshot()) {
			t.Fatal("host cache changed guest handles, surfaces or save state")
		}
	}
	if len(hot.Assets.decodedPixels) != 1 || hot.Assets.decodedPixelBytes != 8 {
		t.Fatal("released decode was not retained")
	}
	state := hot.Snapshot()
	check(t, hot.Restore(state))
	if len(hot.Assets.decodedPixels) != 0 || hot.Assets.decodedPixelBytes != 0 {
		t.Fatal("restore retained a cache under prior limits")
	}
}

func TestAssetPixelCacheKeepsConstraintsAndOwnerIsolation(t *testing.T) {
	services, err := NewServices(Config{})
	check(t, err)
	encoded := testPNG(t)
	id, err := services.Assets.Decode(7, encoded, DecodeOptions{})
	check(t, err)
	check(t, services.Assets.Release(7, id))
	if _, err := services.Assets.Decode(7, encoded, DecodeOptions{MediaType: "image/jpeg"}); err == nil {
		t.Fatal("host cache bypassed requested media type")
	}
	other, err := services.Assets.Decode(8, encoded, DecodeOptions{})
	check(t, err)
	if _, err := services.Assets.Info(7, other); err == nil {
		t.Fatal("cache exposed another owner's resource")
	}
	check(t, services.Assets.Release(8, other))
	state := services.Snapshot()
	state.Assets.Limits.MaxDecodedBytes = 4
	check(t, services.Assets.Restore(state.Assets))
	if _, err := services.Assets.Decode(7, encoded, DecodeOptions{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("restored decode limit bypassed: %v", err)
	}
}

func TestAssetPixelCacheEvictsByBytesAndEntryCount(t *testing.T) {
	services, err := NewServices(Config{})
	check(t, err)
	a := services.Assets
	a.limits.MaxDecodedBytes = 32
	keys := []assetPixelKey{{digest: sha256.Sum256([]byte("first"))}, {digest: sha256.Sum256([]byte("second"))}, {digest: sha256.Sum256([]byte("third"))}}
	for _, key := range keys[:2] {
		a.rememberPixelAsset(key, &assetPixelDecode{bytes: 16, cacheable: true})
	}
	a.decodedPixelClock++
	a.decodedPixels[keys[0]].used = a.decodedPixelClock
	a.rememberPixelAsset(keys[2], &assetPixelDecode{bytes: 16, cacheable: true})
	if a.decodedPixels[keys[1]] != nil || a.decodedPixels[keys[0]] == nil || a.decodedPixelBytes != 32 {
		t.Fatal("cache did not evict the least recently used decode")
	}
	a.decodedPixels = nil
	a.decodedPixelBytes = 0
	a.limits.MaxDecodedBytes = assetPixelCacheBytes
	for n := 0; n < assetPixelCacheEntries+20; n++ {
		key := assetPixelKey{digest: sha256.Sum256([]byte{byte(n), byte(n >> 8)})}
		a.rememberPixelAsset(key, &assetPixelDecode{bytes: 1, cacheable: true})
	}
	if len(a.decodedPixels) != assetPixelCacheEntries || a.decodedPixelBytes != assetPixelCacheEntries {
		t.Fatal("cache exceeded its entry bound")
	}
}
