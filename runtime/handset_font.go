package runtime

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Shared layout of every embedded handset bitmap font. Each glyph is a 12x12
// grid of four-bit alpha values, packed high nibble first, two pixels per byte.
// A font payload is a dense Hangul block (one glyph per precomposed syllable,
// advance implicit) followed by a sorted extra-glyph block (each record carries
// a 4-byte big-endian rune key, a 1-byte advance, and the packed glyph).
const (
	handsetHangulFirst       = 0xac00
	handsetHangulLast        = 0xd7a3
	handsetGlyphSourceWidth  = 12
	handsetGlyphSourceHeight = 12
	handsetGlyphPixels       = handsetGlyphSourceWidth * handsetGlyphSourceHeight
	handsetGlyphBytes        = handsetGlyphPixels / 2
	handsetHangulDataBytes   = (handsetHangulLast - handsetHangulFirst + 1) * handsetGlyphBytes
	handsetExtraRecordBytes  = 4 + 1 + handsetGlyphBytes
)

// handsetFont is one decoded fallback bitmap font. Instances are immutable
// after construction and safe for concurrent reads.
type handsetFont struct {
	name       string
	data       []byte
	extraCount int
}

// defaultHandsetFontName is the fallback font used when a machine does not
// request one (or requests an unknown name). Galmuri9 renders crisp 1:1 at the
// common 12-pixel size; NeoDunggeunmo remains selectable for its softer look.
const defaultHandsetFontName = "galmuri9"

// customHandsetFontPrefix marks names of user-supplied fonts registered at
// runtime. The name embeds a content hash so a given font file always resolves
// to the same identifier, keeping the hashed machine configuration reproducible.
const customHandsetFontPrefix = "custom:"

var (
	handsetFontsMu sync.RWMutex
	handsetFonts   = map[string]*handsetFont{
		"neodgm":   newHandsetFont("neodgm", neodgmBitmapDataBase64, neodgmExtraGlyphCount),
		"galmuri9": newHandsetFont("galmuri9", galmuri9BitmapDataBase64, galmuri9ExtraGlyphCount),
		"mulmaru":  newHandsetFont("mulmaru", mulmaruBitmapDataBase64, mulmaruExtraGlyphCount),
	}
)

// handsetFontNames returns the built-in fallback font names, sorted, so the
// service layer and tests can validate selections deterministically. Custom
// runtime-registered fonts are intentionally excluded.
func handsetFontNames() []string {
	handsetFontsMu.RLock()
	defer handsetFontsMu.RUnlock()
	names := make([]string, 0, len(handsetFonts))
	for name := range handsetFonts {
		if len(name) >= len(customHandsetFontPrefix) && name[:len(customHandsetFontPrefix)] == customHandsetFontPrefix {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// lookupHandsetFont resolves a font name, falling back to the default for an
// empty or unknown name so glyph rasterization can never be left without data.
func lookupHandsetFont(name string) *handsetFont {
	handsetFontsMu.RLock()
	defer handsetFontsMu.RUnlock()
	if font, ok := handsetFonts[name]; ok {
		return font
	}
	return handsetFonts[defaultHandsetFontName]
}

// RegisterHandsetFont builds a fallback font from a user-supplied BDF or
// TrueType/OpenType file and registers it under a content-addressed name, which
// it returns for use as Config.FallbackFont. Re-registering the same bytes is
// idempotent. The composition root (e.g. aram-emu) calls this when the user
// selects a custom font file.
func RegisterHandsetFont(data []byte) (string, error) {
	payload, extraCount, err := BuildHandsetPack(data)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	name := customHandsetFontPrefix + hex.EncodeToString(sum[:8])

	handsetFontsMu.Lock()
	defer handsetFontsMu.Unlock()
	if _, ok := handsetFonts[name]; !ok {
		handsetFonts[name] = &handsetFont{name: name, data: payload, extraCount: extraCount}
	}
	return name, nil
}

func newHandsetFont(name, encoded string, extraCount int) *handsetFont {
	want := handsetHangulDataBytes + extraCount*handsetExtraRecordBytes
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		panic(fmt.Sprintf("runtime: decode embedded %s bitmap: %v", name, err))
	}
	reader, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		panic(fmt.Sprintf("runtime: open embedded %s bitmap: %v", name, err))
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, int64(want)+1))
	closeErr := reader.Close()
	if readErr != nil {
		panic(fmt.Sprintf("runtime: inflate embedded %s bitmap: %v", name, readErr))
	}
	if closeErr != nil {
		panic(fmt.Sprintf("runtime: close embedded %s bitmap: %v", name, closeErr))
	}
	if len(data) != want {
		panic(fmt.Sprintf(
			"runtime: embedded %s bitmap is %d bytes, want %d",
			name, len(data), want,
		))
	}
	return &handsetFont{name: name, data: data, extraCount: extraCount}
}

func (f *handsetFont) hangulGlyph(character rune) []byte {
	index := int(character - handsetHangulFirst)
	offset := index * handsetGlyphBytes
	return f.data[offset : offset+handsetGlyphBytes]
}

func (f *handsetFont) extraGlyph(character rune) (uint8, []byte, bool) {
	data := f.data[handsetHangulDataBytes:]
	low, high := 0, f.extraCount
	for low < high {
		middle := low + (high-low)/2
		offset := middle * handsetExtraRecordBytes
		candidate := rune(binary.BigEndian.Uint32(data[offset : offset+4]))
		switch {
		case candidate < character:
			low = middle + 1
		case candidate > character:
			high = middle
		default:
			advance := data[offset+4]
			bitmap := data[offset+5 : offset+handsetExtraRecordBytes]
			return advance, bitmap, true
		}
	}
	return 0, nil, false
}

func handsetBitmapAlpha(bitmap []byte, x, y int32) byte {
	pixel := int(y)*handsetGlyphSourceWidth + int(x)
	packed := bitmap[pixel/2]
	if pixel&1 == 0 {
		packed >>= 4
	} else {
		packed &= 0x0f
	}
	return packed * 0x11
}
