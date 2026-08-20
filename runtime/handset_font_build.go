package runtime

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// handsetCellAscent is the number of rows above the baseline inside the 12x12
// cell. It matches the runtime's Metrics ascent at the native 12-pixel size
// ((12*3+3)/4 == 9) so glyph baselines land where Draw expects them.
const handsetCellAscent = 9

// sourceGlyph is one glyph decoded from a font source: an advance in 12-pixel
// units, a 12x12 grid of four-bit alpha values (one byte per pixel, 0..15), and
// whether any set pixel fell outside the cell (which drops it from the extra
// block; Hangul is always kept and simply clipped).
type sourceGlyph struct {
	advance int
	cell    []byte
	clipped bool
}

// BuildHandsetPack converts a BDF or TrueType/OpenType font into the packed
// handset glyph payload (see handset_font.go for the layout). It auto-detects
// the source format and returns the payload bytes and the number of non-Hangul
// extra records. It is used both by the offline generator (which then compresses
// and embeds the result) and at runtime for user-supplied fonts.
func BuildHandsetPack(data []byte) ([]byte, int, error) {
	glyphs, err := decodeFontGlyphs(data)
	if err != nil {
		return nil, 0, err
	}
	payload, extraCount := assembleHandsetPack(glyphs)
	return payload, extraCount, nil
}

func decodeFontGlyphs(data []byte) (map[rune]sourceGlyph, error) {
	switch {
	case looksLikeBDF(data):
		return decodeBDFGlyphs(data)
	case looksLikeSFNT(data):
		return decodeTTFGlyphs(data)
	default:
		return nil, fmt.Errorf("%w: unrecognized font format (expected BDF or TrueType/OpenType)", ErrInvalidArgument)
	}
}

func looksLikeBDF(data []byte) bool {
	head := data
	if len(head) > 64 {
		head = head[:64]
	}
	return strings.HasPrefix(strings.TrimLeft(string(head), " \t\r\n"), "STARTFONT")
}

func looksLikeSFNT(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	switch string(data[:4]) {
	case "\x00\x01\x00\x00", "OTTO", "true", "ttcf", "typ1":
		return true
	}
	return false
}

// assembleHandsetPack lays glyphs into the dense Hangul block followed by the
// sorted non-Hangul extra records, mirroring the embedded payload format.
func assembleHandsetPack(glyphs map[rune]sourceGlyph) ([]byte, int) {
	var extras []rune
	for cp, g := range glyphs {
		if isHandsetExtraRune(cp) && !g.clipped {
			extras = append(extras, cp)
		}
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i] < extras[j] })

	total := handsetHangulDataBytes + len(extras)*handsetExtraRecordBytes
	payload := make([]byte, 0, total)
	blank := make([]byte, handsetGlyphBytes)
	for cp := rune(handsetHangulFirst); cp <= handsetHangulLast; cp++ {
		if g, ok := glyphs[cp]; ok {
			payload = append(payload, packHandsetCell(g.cell)...)
		} else {
			payload = append(payload, blank...)
		}
	}
	for _, cp := range extras {
		g := glyphs[cp]
		var rec [handsetExtraRecordBytes]byte
		binary.BigEndian.PutUint32(rec[0:4], uint32(cp))
		rec[4] = clampHandsetAdvance(g.advance)
		copy(rec[5:], packHandsetCell(g.cell))
		payload = append(payload, rec[:]...)
	}
	return payload, len(extras)
}

func isHandsetExtraRune(cp rune) bool {
	switch {
	case cp < 0x20:
		return false
	case cp >= handsetHangulFirst && cp <= handsetHangulLast:
		return false
	case cp >= 0x4E00 && cp <= 0x9FFF: // CJK unified ideographs (Hanja)
		return false
	case cp >= 0xE000 && cp <= 0xF8FF: // Private Use Area
		return false
	case cp >= 0x10000:
		return false
	}
	return true
}

func clampHandsetAdvance(advance int) byte {
	if advance < 0 {
		return 0
	}
	if advance > 0xFF {
		return 0xFF
	}
	return byte(advance)
}

func packHandsetCell(cell []byte) []byte {
	packed := make([]byte, handsetGlyphBytes)
	for p := 0; p < handsetGlyphSourceWidth*handsetGlyphSourceHeight; p++ {
		v := cell[p] & 0x0f
		if v == 0 {
			continue
		}
		if p&1 == 0 {
			packed[p>>1] |= v << 4
		} else {
			packed[p>>1] |= v
		}
	}
	return packed
}

// ---- BDF ----

type bdfGlyph struct {
	w, h, xoff, yoff int
	dwidth           int
	rows             []uint64
}

func decodeBDFGlyphs(data []byte) (map[rune]sourceGlyph, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	out := make(map[rune]sourceGlyph)

	enc := -1
	var cur bdfGlyph
	reading := false
	rowsLeft := 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "STARTCHAR"):
			enc, cur, reading, rowsLeft = -1, bdfGlyph{}, false, 0
		case strings.HasPrefix(line, "ENCODING"):
			enc = atoiField(line, 1)
		case strings.HasPrefix(line, "DWIDTH"):
			cur.dwidth = atoiField(line, 1)
		case strings.HasPrefix(line, "BBX"):
			cur.w = atoiField(line, 1)
			cur.h = atoiField(line, 2)
			cur.xoff = atoiField(line, 3)
			cur.yoff = atoiField(line, 4)
			cur.rows = make([]uint64, 0, cur.h)
		case strings.HasPrefix(line, "BITMAP"):
			reading, rowsLeft = true, cur.h
		case strings.HasPrefix(line, "ENDCHAR"):
			reading = false
			if enc >= 0 {
				out[rune(enc)] = rasterBDFGlyph(cur)
			}
		case reading && rowsLeft > 0:
			hex := strings.TrimSpace(line)
			if hex == "" {
				continue
			}
			v, err := strconv.ParseUint(hex, 16, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: bad BDF bitmap row %q", ErrInvalidArgument, hex)
			}
			if shift := (cur.w+7)/8*8 - cur.w; shift > 0 && shift < 64 {
				v >>= uint(shift)
			}
			cur.rows = append(cur.rows, v)
			rowsLeft--
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: BDF contained no glyphs", ErrInvalidArgument)
	}
	return out, nil
}

func rasterBDFGlyph(g bdfGlyph) sourceGlyph {
	glyph := sourceGlyph{advance: g.dwidth, cell: make([]byte, handsetGlyphSourceWidth*handsetGlyphSourceHeight)}
	top := handsetCellAscent - (g.yoff + g.h)
	for i := 0; i < g.h && i < len(g.rows); i++ {
		row := g.rows[i]
		ry := top + i
		for j := 0; j < g.w; j++ {
			if (row>>(uint(g.w-1-j)))&1 == 0 {
				continue
			}
			if !setCellPixel(glyph.cell, g.xoff+j, ry, 15) {
				glyph.clipped = true
			}
		}
	}
	return glyph
}

// ---- TrueType / OpenType ----

func decodeTTFGlyphs(data []byte) (map[rune]sourceGlyph, error) {
	parsed, err := opentype.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%w: parse TrueType font: %v", ErrInvalidArgument, err)
	}

	size, baselineRow, crisp := chooseTTFRender(parsed)

	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size: size, DPI: 72, Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: build TrueType face: %v", ErrInvalidArgument, err)
	}
	defer face.Close()

	out := make(map[rune]sourceGlyph)
	baseline := fixed.P(0, baselineRow)
	addGlyph := func(cp rune) {
		if _, ok := face.GlyphAdvance(cp); !ok {
			return
		}
		dr, mask, maskp, adv, ok := face.Glyph(baseline, cp)
		if !ok {
			return
		}
		glyph := sourceGlyph{advance: adv.Round(), cell: make([]byte, handsetGlyphSourceWidth*handsetGlyphSourceHeight)}
		for y := dr.Min.Y; y < dr.Max.Y; y++ {
			for x := dr.Min.X; x < dr.Max.X; x++ {
				_, _, _, a := mask.At(maskp.X+(x-dr.Min.X), maskp.Y+(y-dr.Min.Y)).RGBA()
				if a == 0 {
					continue
				}
				alpha := byte(a >> 12) // 16-bit alpha -> 4-bit coverage
				if crisp {
					// A pixel font rendered 1:1 has only fully-covered
					// pixels; snap the near-full ones to solid ink so the
					// result stays perfectly crisp with no gray fringes.
					if a < 0x8000 {
						continue
					}
					alpha = 0x0f
				}
				if !setCellPixel(glyph.cell, x, y, alpha) {
					glyph.clipped = true
				}
			}
		}
		out[cp] = glyph
	}

	for cp := rune(0x20); cp <= 0x33FF; cp++ {
		addGlyph(cp)
	}
	for cp := rune(handsetHangulFirst); cp <= handsetHangulLast; cp++ {
		addGlyph(cp)
	}
	for cp := rune(0xFF00); cp <= 0xFFEF; cp++ {
		addGlyph(cp)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: TrueType font produced no usable glyphs", ErrInvalidArgument)
	}
	return out, nil
}

// chooseTTFRender decides how to rasterize a TrueType/OpenType source into the
// 12x12 handset cell. Pixel fonts (whose outlines are axis-aligned squares on
// an integer grid) render crisp only when one font pixel maps onto one output
// pixel, so it scans the integer sizes that fit the cell and, when one renders
// essentially free of anti-aliased edges, uses it 1:1 with the font's natural
// baseline. Smooth outline fonts fall back to the legacy behaviour: scale so
// the ascent lands on the cell ascent and keep the grayscale coverage.
func chooseTTFRender(parsed *opentype.Font) (size float64, baseline int, crisp bool) {
	sample := []rune{'가', '한', '국', '방', '글', '음', 'A', 'H', 'g', '8', '@', 'W'}
	bestSize, bestBaseline, bestRatio := 0, 0, 1.0
	for px := 8; px <= handsetGlyphSourceHeight; px++ {
		face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
			Size: float64(px), DPI: 72, Hinting: font.HintingFull,
		})
		if err != nil {
			continue
		}
		ascent := face.Metrics().Ascent.Ceil()
		partial, ink := ttfEdgeStats(face, sample, ascent)
		face.Close()
		if ink == 0 || ascent <= 0 || ascent > handsetGlyphSourceHeight {
			continue
		}
		if ratio := float64(partial) / float64(ink); ratio < bestRatio {
			bestRatio, bestSize, bestBaseline = ratio, px, ascent
		}
	}
	if bestSize != 0 && bestRatio < 0.05 {
		return float64(bestSize), bestBaseline, true
	}

	size = float64(handsetGlyphSourceHeight)
	if probe, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size: size, DPI: 72, Hinting: font.HintingFull,
	}); err == nil {
		if ascent := probe.Metrics().Ascent.Ceil(); ascent > 0 {
			size = size * float64(handsetCellAscent) / float64(ascent)
		}
		probe.Close()
	}
	return size, handsetCellAscent, false
}

// ttfEdgeStats counts, over a sample of glyphs rendered at the given baseline,
// how many inked pixels are partially covered (anti-aliased edges) versus the
// total inked pixels. A pixel font rendered 1:1 reports zero partial pixels.
func ttfEdgeStats(face font.Face, sample []rune, baseline int) (partial, ink int) {
	dot := fixed.P(0, baseline)
	for _, cp := range sample {
		dr, mask, maskp, _, ok := face.Glyph(dot, cp)
		if !ok {
			continue
		}
		for y := dr.Min.Y; y < dr.Max.Y; y++ {
			for x := dr.Min.X; x < dr.Max.X; x++ {
				_, _, _, a := mask.At(maskp.X+(x-dr.Min.X), maskp.Y+(y-dr.Min.Y)).RGBA()
				if a == 0 {
					continue
				}
				ink++
				if a > 0x2000 && a < 0xE000 {
					partial++
				}
			}
		}
	}
	return partial, ink
}

func setCellPixel(cell []byte, x, y int, alpha byte) bool {
	if x < 0 || x >= handsetGlyphSourceWidth || y < 0 || y >= handsetGlyphSourceHeight {
		return false
	}
	cell[y*handsetGlyphSourceWidth+x] = alpha
	return true
}

func atoiField(line string, n int) int {
	fields := strings.Fields(line)
	if n < len(fields) {
		v, _ := strconv.Atoi(fields[n])
		return v
	}
	return 0
}
