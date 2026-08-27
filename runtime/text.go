package runtime

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

type TextLimits struct {
	MaxFonts       uint32
	MaxGlyphs      uint32
	MaxGlyphPixels uint64
	MaxStringBytes uint32
}

func DefaultTextLimits() TextLimits {
	return TextLimits{
		MaxFonts: 128, MaxGlyphs: 65_536,
		MaxGlyphPixels: 16 << 20, MaxStringBytes: 1 << 20,
	}
}

func (l TextLimits) Validate() error {
	if l.MaxFonts == 0 || l.MaxGlyphs == 0 ||
		l.MaxGlyphPixels == 0 || l.MaxStringBytes == 0 {
		return fmt.Errorf("%w: invalid text limits", ErrInvalidArgument)
	}
	return nil
}

type FontStyle uint8

const (
	FontBold FontStyle = 1 << iota
	FontItalic
	FontUnderlined
)

type FontDescriptor struct {
	Family string
	Size   int32
	Style  FontStyle
}

func (d FontDescriptor) Validate() error {
	if len(d.Family) > 127 || strings.IndexByte(d.Family, 0) >= 0 ||
		d.Size < 5 || d.Size > 256 || d.Style&^(FontBold|FontItalic|FontUnderlined) != 0 {
		return fmt.Errorf("%w: invalid font descriptor", ErrInvalidArgument)
	}
	return nil
}

type FontMetrics struct {
	Height  int32
	Ascent  int32
	Descent int32
	Leading int32
}

type Glyph struct {
	Rune     rune
	Width    int32
	Height   int32
	Advance  int32
	BearingX int32
	BearingY int32
	Alpha    []byte
}

type GlyphState struct {
	Rune     int32
	Width    int32
	Height   int32
	Advance  int32
	BearingX int32
	BearingY int32
	Alpha    []byte
}

type FontState struct {
	ID         ServiceID
	Owner      OwnerID
	Descriptor FontDescriptor
	Glyphs     []GlyphState
}

type TextState struct {
	Limits TextLimits
	Fonts  []FontState
}

type serviceFont struct {
	id         ServiceID
	owner      OwnerID
	descriptor FontDescriptor
	glyphs     map[rune]Glyph
}

type TextEncoding string

const (
	EncodingUTF8    TextEncoding = "utf-8"
	EncodingUTF16LE TextEncoding = "utf-16le"
	EncodingUTF16BE TextEncoding = "utf-16be"
	EncodingEUCKR   TextEncoding = "euc-kr"
)

type TextAnchor uint16

const (
	AnchorLeft TextAnchor = 1 << iota
	AnchorHorizontalCenter
	AnchorRight
	AnchorTop
	AnchorVerticalCenter
	AnchorBottom
	AnchorBaseline
)

// Text owns deterministic fallback font metrics, glyph caches, rasterization,
// measurement, and legacy Korean encoding conversion.
type Text struct {
	registry *Registry
	graphics *Graphics
	limits   TextLimits
	fallback *handsetFont
	fonts    map[ServiceID]*serviceFont
	glyphs   uint32
	pixels   uint64
}

func NewText(
	registry *Registry,
	graphics *Graphics,
	limits TextLimits,
	fallbackFont string,
) (*Text, error) {
	if registry == nil || graphics == nil {
		return nil, fmt.Errorf("%w: text dependencies are nil", ErrInvalidArgument)
	}
	if limits == (TextLimits{}) {
		limits = DefaultTextLimits()
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Text{
		registry: registry, graphics: graphics, limits: limits,
		fallback: lookupHandsetFont(fallbackFont),
		fonts:    make(map[ServiceID]*serviceFont),
	}, nil
}

func (t *Text) CreateFont(owner OwnerID, descriptor FontDescriptor) (ServiceID, error) {
	if descriptor.Family == "" {
		descriptor.Family = "aram-fallback"
	}
	if err := descriptor.Validate(); err != nil {
		return 0, err
	}
	if uint32(len(t.fonts)) >= t.limits.MaxFonts {
		return 0, fmt.Errorf("%w: font count reached %d", ErrLimitExceeded, t.limits.MaxFonts)
	}
	id, err := t.registry.Create(owner, KindFont)
	if err != nil {
		return 0, err
	}
	t.fonts[id] = &serviceFont{
		id: id, owner: owner, descriptor: descriptor, glyphs: make(map[rune]Glyph),
	}
	return id, nil
}

// EnsureFont returns the oldest live font with the same owner and descriptor,
// or creates it when none exists. Adapters use this for integer font handles
// whose service association can be reconstructed after restoring a snapshot.
func (t *Text) EnsureFont(owner OwnerID, descriptor FontDescriptor) (ServiceID, error) {
	if descriptor.Family == "" {
		descriptor.Family = "aram-fallback"
	}
	if err := descriptor.Validate(); err != nil {
		return 0, err
	}
	var existing ServiceID
	for id, font := range t.fonts {
		if font.owner == owner && font.descriptor == descriptor &&
			(existing == 0 || id < existing) {
			existing = id
		}
	}
	if existing != 0 {
		return existing, nil
	}
	return t.CreateFont(owner, descriptor)
}

func (t *Text) DestroyFont(owner OwnerID, id ServiceID) error {
	font, err := t.font(owner, id)
	if err != nil {
		return err
	}
	for _, glyph := range font.glyphs {
		t.glyphs--
		t.pixels -= uint64(len(glyph.Alpha))
	}
	if err := t.registry.Destroy(id, owner, KindFont); err != nil {
		return err
	}
	delete(t.fonts, id)
	return nil
}

func (t *Text) Metrics(owner OwnerID, id ServiceID) (FontMetrics, error) {
	font, err := t.font(owner, id)
	if err != nil {
		return FontMetrics{}, err
	}
	size := font.descriptor.Size
	// Handset system fonts keep a short descent, so the ascent share rounds
	// up: the KTF 10-pixel font places its baseline 8 rows below the glyph
	// top, and truncating to 7 would misplace every baseline-positioned run.
	ascent := max(int32(1), (size*3+3)/4)
	descent := max(int32(1), size-ascent)
	return FontMetrics{
		Height: size, Ascent: ascent, Descent: descent,
	}, nil
}

func (t *Text) Glyph(owner OwnerID, id ServiceID, character rune) (Glyph, error) {
	font, err := t.font(owner, id)
	if err != nil {
		return Glyph{}, err
	}
	if !utf8.ValidRune(character) {
		character = unicode.ReplacementChar
	}
	if glyph, ok := font.glyphs[character]; ok {
		return cloneGlyph(glyph), nil
	}
	glyph := rasterFallbackGlyph(t.fallback, font.descriptor, character)
	if t.glyphs >= t.limits.MaxGlyphs ||
		uint64(len(glyph.Alpha)) > t.limits.MaxGlyphPixels-t.pixels {
		return Glyph{}, fmt.Errorf("%w: glyph cache limit", ErrLimitExceeded)
	}
	font.glyphs[character] = cloneGlyph(glyph)
	t.glyphs++
	t.pixels += uint64(len(glyph.Alpha))
	return glyph, nil
}

func (t *Text) Measure(owner OwnerID, id ServiceID, text string) (int32, error) {
	if len(text) > int(t.limits.MaxStringBytes) || !utf8.ValidString(text) {
		return 0, fmt.Errorf("%w: invalid or excessive text", ErrInvalidArgument)
	}
	font, err := t.font(owner, id)
	if err != nil {
		return 0, err
	}
	_, width, err := t.prepareGlyphs(font, text, false)
	return width, err
}

func (t *Text) Draw(
	owner OwnerID,
	fontID, surfaceID ServiceID,
	text string,
	x, y int32,
	anchor TextAnchor,
	color Color,
) error {
	if len(text) > int(t.limits.MaxStringBytes) || !utf8.ValidString(text) ||
		!validTextAnchor(anchor) {
		return fmt.Errorf("%w: invalid text draw", ErrInvalidArgument)
	}
	font, err := t.font(owner, fontID)
	if err != nil {
		return err
	}
	if _, err := t.graphics.get(surfaceID, owner); err != nil {
		return err
	}
	glyphs, width, err := t.prepareGlyphs(font, text, true)
	if err != nil {
		return err
	}
	metrics, err := t.Metrics(owner, fontID)
	if err != nil {
		return err
	}
	drawX, drawY := int64(x), int64(y)
	switch {
	case anchor&AnchorRight != 0:
		drawX -= int64(width)
	case anchor&AnchorHorizontalCenter != 0:
		drawX -= int64(width / 2)
	}
	switch {
	case anchor&AnchorBottom != 0:
		drawY -= int64(metrics.Height)
	case anchor&AnchorVerticalCenter != 0:
		drawY -= int64(metrics.Height / 2)
	case anchor&AnchorBaseline != 0:
		drawY -= int64(metrics.Ascent)
	}
	cursor := drawX
	var rasterPixels uint64
	for _, glyph := range glyphs {
		if uint64(len(glyph.Alpha)) > t.limits.MaxGlyphPixels-rasterPixels {
			return fmt.Errorf("%w: text raster work exceeds limit", ErrLimitExceeded)
		}
		rasterPixels += uint64(len(glyph.Alpha))
		for row := int32(0); row < glyph.Height; row++ {
			for column := int32(0); column < glyph.Width; column++ {
				alpha := glyph.Alpha[row*glyph.Width+column]
				if alpha == 0 {
					continue
				}
				pixelX := cursor + int64(glyph.BearingX) + int64(column)
				pixelY := drawY + int64(glyph.BearingY) + int64(row)
				if pixelX < mathMinInt32 || pixelX > mathMaxInt32 ||
					pixelY < mathMinInt32 || pixelY > mathMaxInt32 {
					continue
				}
				pixel := color
				pixel.A = uint8(uint16(pixel.A) * uint16(alpha) / 255)
				if err := t.graphics.SetPixel(
					owner,
					surfaceID,
					int32(pixelX),
					int32(pixelY),
					pixel,
				); err != nil {
					return err
				}
			}
		}
		cursor += int64(glyph.Advance)
	}
	return nil
}

const (
	mathMinInt32 = int64(-1 << 31)
	mathMaxInt32 = int64(1<<31 - 1)
)

func validTextAnchor(anchor TextAnchor) bool {
	const known = AnchorLeft | AnchorHorizontalCenter | AnchorRight |
		AnchorTop | AnchorVerticalCenter | AnchorBottom | AnchorBaseline
	if anchor&^known != 0 {
		return false
	}
	horizontal := 0
	for _, value := range []TextAnchor{
		AnchorLeft,
		AnchorHorizontalCenter,
		AnchorRight,
	} {
		if anchor&value != 0 {
			horizontal++
		}
	}
	vertical := 0
	for _, value := range []TextAnchor{
		AnchorTop,
		AnchorVerticalCenter,
		AnchorBottom,
		AnchorBaseline,
	} {
		if anchor&value != 0 {
			vertical++
		}
	}
	return horizontal <= 1 && vertical <= 1
}

// prepareGlyphs validates cache and width limits before committing any newly
// rasterized glyph, so a failed measurement or draw is observationally atomic.
func (t *Text) prepareGlyphs(
	font *serviceFont,
	value string,
	boundRasterWork bool,
) ([]Glyph, int32, error) {
	glyphs := make([]Glyph, 0, utf8.RuneCountInString(value))
	pending := make(map[rune]Glyph)
	var pendingPixels uint64
	var rasterPixels uint64
	var width int64
	for _, character := range value {
		glyph, ok := font.glyphs[character]
		if !ok {
			glyph, ok = pending[character]
			if !ok {
				glyph = rasterFallbackGlyph(t.fallback, font.descriptor, character)
				pending[character] = glyph
				pendingPixels += uint64(len(glyph.Alpha))
			}
		}
		width += int64(glyph.Advance)
		if width > mathMaxInt32 {
			return nil, 0, fmt.Errorf("%w: measured text is too wide", ErrLimitExceeded)
		}
		if boundRasterWork {
			if uint64(len(glyph.Alpha)) >
				t.limits.MaxGlyphPixels-rasterPixels {
				return nil, 0, fmt.Errorf("%w: text raster work exceeds limit", ErrLimitExceeded)
			}
			rasterPixels += uint64(len(glyph.Alpha))
		}
		glyphs = append(glyphs, glyph)
	}
	if t.glyphs > t.limits.MaxGlyphs || t.pixels > t.limits.MaxGlyphPixels ||
		uint64(len(pending)) > uint64(t.limits.MaxGlyphs-t.glyphs) ||
		pendingPixels > t.limits.MaxGlyphPixels-t.pixels {
		return nil, 0, fmt.Errorf("%w: glyph cache limit", ErrLimitExceeded)
	}
	for character, glyph := range pending {
		font.glyphs[character] = cloneGlyph(glyph)
		t.glyphs++
		t.pixels += uint64(len(glyph.Alpha))
	}
	return glyphs, int32(width), nil
}

func (t *Text) Decode(data []byte, encoding TextEncoding) (string, error) {
	if len(data) > int(t.limits.MaxStringBytes) {
		return "", fmt.Errorf("%w: encoded string exceeds limit", ErrLimitExceeded)
	}
	switch encoding {
	case EncodingUTF8:
		if !utf8.Valid(data) {
			return "", fmt.Errorf("%w: invalid UTF-8", ErrInvalidArgument)
		}
		return string(data), nil
	case EncodingUTF16LE, EncodingUTF16BE:
		if len(data)%2 != 0 {
			return "", fmt.Errorf("%w: odd UTF-16 byte count", ErrInvalidArgument)
		}
		units := make([]uint16, len(data)/2)
		for index := range units {
			if encoding == EncodingUTF16LE {
				units[index] = binary.LittleEndian.Uint16(data[index*2:])
			} else {
				units[index] = binary.BigEndian.Uint16(data[index*2:])
			}
		}
		for index := 0; index < len(units); {
			unit := units[index]
			switch {
			case unit >= 0xd800 && unit <= 0xdbff:
				if index+1 >= len(units) ||
					units[index+1] < 0xdc00 ||
					units[index+1] > 0xdfff {
					return "", fmt.Errorf("%w: invalid UTF-16 surrogate", ErrInvalidArgument)
				}
				index += 2
			case unit >= 0xdc00 && unit <= 0xdfff:
				return "", fmt.Errorf("%w: invalid UTF-16 surrogate", ErrInvalidArgument)
			default:
				index++
			}
		}
		decoded := string(utf16.Decode(units))
		if len(decoded) > int(t.limits.MaxStringBytes) {
			return "", fmt.Errorf("%w: decoded UTF-16 exceeds limit", ErrLimitExceeded)
		}
		return decoded, nil
	case EncodingEUCKR:
		decoded, _, err := transform.Bytes(korean.EUCKR.NewDecoder(), data)
		if err != nil || !utf8.Valid(decoded) {
			return "", fmt.Errorf("%w: invalid EUC-KR", ErrInvalidArgument)
		}
		if len(decoded) > int(t.limits.MaxStringBytes) {
			return "", fmt.Errorf("%w: decoded EUC-KR exceeds limit", ErrLimitExceeded)
		}
		return string(decoded), nil
	default:
		return "", fmt.Errorf("%w: unsupported text encoding %q", ErrInvalidArgument, encoding)
	}
}

func (t *Text) Encode(value string, encoding TextEncoding) ([]byte, error) {
	if !utf8.ValidString(value) || len(value) > int(t.limits.MaxStringBytes) {
		return nil, fmt.Errorf("%w: invalid or excessive Unicode text", ErrInvalidArgument)
	}
	switch encoding {
	case EncodingUTF8:
		return []byte(value), nil
	case EncodingUTF16LE, EncodingUTF16BE:
		units := utf16.Encode([]rune(value))
		if len(units) > int(t.limits.MaxStringBytes)/2 {
			return nil, fmt.Errorf("%w: UTF-16 output exceeds limit", ErrLimitExceeded)
		}
		result := make([]byte, len(units)*2)
		for index, unit := range units {
			if encoding == EncodingUTF16LE {
				binary.LittleEndian.PutUint16(result[index*2:], unit)
			} else {
				binary.BigEndian.PutUint16(result[index*2:], unit)
			}
		}
		return result, nil
	case EncodingEUCKR:
		encoded, _, err := transform.Bytes(korean.EUCKR.NewEncoder(), []byte(value))
		if err != nil {
			return nil, fmt.Errorf("%w: text is not representable in EUC-KR", ErrInvalidArgument)
		}
		if len(encoded) > int(t.limits.MaxStringBytes) {
			return nil, fmt.Errorf("%w: EUC-KR output exceeds limit", ErrLimitExceeded)
		}
		return encoded, nil
	default:
		return nil, fmt.Errorf("%w: unsupported text encoding %q", ErrInvalidArgument, encoding)
	}
}

func (t *Text) Snapshot() TextState {
	state := TextState{Limits: t.limits}
	ids := make([]ServiceID, 0, len(t.fonts))
	for id := range t.fonts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		font := t.fonts[id]
		saved := FontState{
			ID: id, Owner: font.owner, Descriptor: font.descriptor,
		}
		characters := make([]rune, 0, len(font.glyphs))
		for character := range font.glyphs {
			characters = append(characters, character)
		}
		sort.Slice(characters, func(i, j int) bool { return characters[i] < characters[j] })
		for _, character := range characters {
			glyph := font.glyphs[character]
			saved.Glyphs = append(saved.Glyphs, GlyphState{
				Rune: int32(character), Width: glyph.Width, Height: glyph.Height,
				Advance: glyph.Advance, BearingX: glyph.BearingX,
				BearingY: glyph.BearingY, Alpha: cloneBytes(glyph.Alpha),
			})
		}
		state.Fonts = append(state.Fonts, saved)
	}
	return state
}

func (t *Text) Restore(state TextState) error {
	if err := state.Limits.Validate(); err != nil ||
		len(state.Fonts) > int(state.Limits.MaxFonts) {
		return fmt.Errorf("%w: invalid text state limits", ErrInvalidState)
	}
	fonts := make(map[ServiceID]*serviceFont, len(state.Fonts))
	var previous ServiceID
	var glyphCount uint32
	var pixelCount uint64
	for index, saved := range state.Fonts {
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previous) ||
			saved.Descriptor.Family == "" ||
			saved.Descriptor.Validate() != nil ||
			t.registry.Validate(saved.ID, saved.Owner, KindFont) != nil {
			return fmt.Errorf("%w: invalid font state %d", ErrInvalidState, index)
		}
		font := &serviceFont{
			id: saved.ID, owner: saved.Owner, descriptor: saved.Descriptor,
			glyphs: make(map[rune]Glyph, len(saved.Glyphs)),
		}
		var previousRune int32 = -1
		for glyphIndex, savedGlyph := range saved.Glyphs {
			character := rune(savedGlyph.Rune)
			size := int64(savedGlyph.Width) * int64(savedGlyph.Height)
			if !utf8.ValidRune(character) ||
				(glyphIndex != 0 && savedGlyph.Rune <= previousRune) ||
				savedGlyph.Width < 0 || savedGlyph.Height < 0 ||
				savedGlyph.Advance < 0 || size < 0 ||
				size != int64(len(savedGlyph.Alpha)) {
				return fmt.Errorf(
					"%w: invalid glyph %d in font %d",
					ErrInvalidState,
					glyphIndex,
					index,
				)
			}
			expected := rasterFallbackGlyph(t.fallback, saved.Descriptor, character)
			if savedGlyph.Width != expected.Width ||
				savedGlyph.Height != expected.Height ||
				savedGlyph.Advance != expected.Advance ||
				savedGlyph.BearingX != expected.BearingX ||
				savedGlyph.BearingY != expected.BearingY ||
				!bytes.Equal(savedGlyph.Alpha, expected.Alpha) {
				return fmt.Errorf(
					"%w: non-canonical glyph %d in font %d",
					ErrInvalidState,
					glyphIndex,
					index,
				)
			}
			glyphCount++
			pixelCount += uint64(len(savedGlyph.Alpha))
			if glyphCount > state.Limits.MaxGlyphs ||
				pixelCount > state.Limits.MaxGlyphPixels {
				return fmt.Errorf("%w: saved glyph cache exceeds limits", ErrInvalidState)
			}
			font.glyphs[character] = Glyph{
				Rune: character, Width: savedGlyph.Width, Height: savedGlyph.Height,
				Advance: savedGlyph.Advance, BearingX: savedGlyph.BearingX,
				BearingY: savedGlyph.BearingY, Alpha: cloneBytes(savedGlyph.Alpha),
			}
			previousRune = savedGlyph.Rune
		}
		fonts[saved.ID] = font
		previous = saved.ID
	}
	t.limits = state.Limits
	t.fonts = fonts
	t.glyphs = glyphCount
	t.pixels = pixelCount
	return nil
}

func (t *Text) font(owner OwnerID, id ServiceID) (*serviceFont, error) {
	if err := t.registry.Validate(id, owner, KindFont); err != nil {
		return nil, err
	}
	font := t.fonts[id]
	if font == nil {
		return nil, fmt.Errorf("%w: font %s", ErrInvalidState, id)
	}
	return font, nil
}

func cloneGlyph(glyph Glyph) Glyph {
	glyph.Alpha = cloneBytes(glyph.Alpha)
	return glyph
}
