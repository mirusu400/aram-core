package runtime

import "unicode"

func rasterFallbackGlyph(font *handsetFont, descriptor FontDescriptor, character rune) Glyph {
	if character >= 0xac00 && character <= 0xd7a3 {
		return rasterHangulGlyph(font, descriptor, character)
	}
	if sourceAdvance, bitmap, ok := font.extraGlyph(character); ok {
		return rasterHandsetGlyph(
			descriptor,
			character,
			int32(sourceAdvance),
			bitmap,
		)
	}
	height := descriptor.Size
	scale := max(int32(1), height/8)
	width := 5 * scale
	advance := 6 * scale
	glyph := Glyph{
		Rune: character, Width: width, Height: 7 * scale,
		Advance: advance, BearingY: max(int32(0), (height-7*scale)/2),
		Alpha: make([]byte, width*7*scale),
	}
	rows := fallbackGlyphRows(character)
	for row, bits := range rows {
		for column := 0; column < 5; column++ {
			if bits&(1<<uint(4-column)) == 0 {
				continue
			}
			for dy := int32(0); dy < scale; dy++ {
				for dx := int32(0); dx < scale; dx++ {
					x := int32(column)*scale + dx
					y := int32(row)*scale + dy
					glyph.Alpha[y*glyph.Width+x] = 0xff
				}
			}
		}
	}
	if descriptor.Style&FontBold != 0 {
		for y := int32(0); y < glyph.Height; y++ {
			for x := glyph.Width - 1; x > 0; x-- {
				if glyph.Alpha[y*glyph.Width+x-1] != 0 {
					glyph.Alpha[y*glyph.Width+x] = 0xff
				}
			}
		}
	}
	return glyph
}

func rasterHangulGlyph(font *handsetFont, descriptor FontDescriptor, character rune) Glyph {
	return rasterHandsetGlyph(
		descriptor,
		character,
		handsetGlyphSourceWidth,
		font.hangulGlyph(character),
	)
}

func rasterHandsetGlyph(
	descriptor FontDescriptor,
	character rune,
	sourceAdvance int32,
	bitmap []byte,
) Glyph {
	height := max(int32(8), descriptor.Size)
	sourceWidth := sourceAdvance
	if sourceWidth == 0 {
		sourceWidth = handsetGlyphSourceWidth
	}
	width := max(
		int32(1),
		(sourceWidth*height+handsetGlyphSourceHeight/2)/
			handsetGlyphSourceHeight,
	)
	advance := max(
		int32(0),
		(sourceAdvance*height+handsetGlyphSourceHeight/2)/
			handsetGlyphSourceHeight,
	)
	glyph := Glyph{
		Rune: character, Width: width, Height: height,
		Advance: advance,
		Alpha:   make([]byte, width*height),
	}
	if sourceAdvance == 0 {
		glyph.BearingX = -width
	}
	// Each destination pixel keeps the strongest source pixel it covers.
	// Point sampling instead would drop whole source rows and columns when
	// shrinking, and a one-pixel vowel stroke that disappears turns one
	// Hangul syllable into another.
	for y := int32(0); y < height; y++ {
		sourceY := y * handsetGlyphSourceHeight / height
		sourceYEnd := max(
			sourceY+1,
			(y+1)*handsetGlyphSourceHeight/height,
		)
		for x := int32(0); x < width; x++ {
			sourceX := x * sourceWidth / width
			sourceXEnd := max(sourceX+1, (x+1)*sourceWidth/width)
			var alpha byte
			for row := sourceY; row < sourceYEnd; row++ {
				for column := sourceX; column < sourceXEnd; column++ {
					alpha = max(
						alpha,
						handsetBitmapAlpha(bitmap, column, row),
					)
				}
			}
			glyph.Alpha[y*width+x] = alpha
		}
	}
	if descriptor.Style&FontBold != 0 {
		for y := int32(0); y < glyph.Height; y++ {
			for x := glyph.Width - 1; x > 0; x-- {
				previous := glyph.Alpha[y*glyph.Width+x-1]
				if previous > glyph.Alpha[y*glyph.Width+x] {
					glyph.Alpha[y*glyph.Width+x] = previous
				}
			}
		}
	}
	return glyph
}

func fallbackGlyphRows(character rune) [7]uint8 {
	if unicode.IsSpace(character) {
		return [7]uint8{}
	}
	if character >= 'a' && character <= 'z' {
		character = unicode.ToUpper(character)
	}
	if rows, ok := fallbackASCII[character]; ok {
		return rows
	}
	middle := uint8((uint32(character) ^ uint32(character>>5)) & 0x0e)
	return [7]uint8{
		0x1f, 0x11, 0x11 | middle, 0x11,
		0x11 | middle, 0x11, 0x1f,
	}
}

var fallbackASCII = map[rune][7]uint8{
	'0': {0x0e, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0e},
	'1': {0x04, 0x0c, 0x14, 0x04, 0x04, 0x04, 0x1f},
	'2': {0x0e, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1f},
	'3': {0x1e, 0x01, 0x01, 0x0e, 0x01, 0x01, 0x1e},
	'4': {0x02, 0x06, 0x0a, 0x12, 0x1f, 0x02, 0x02},
	'5': {0x1f, 0x10, 0x10, 0x1e, 0x01, 0x01, 0x1e},
	'6': {0x0e, 0x10, 0x10, 0x1e, 0x11, 0x11, 0x0e},
	'7': {0x1f, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8': {0x0e, 0x11, 0x11, 0x0e, 0x11, 0x11, 0x0e},
	'9': {0x0e, 0x11, 0x11, 0x0f, 0x01, 0x01, 0x0e},
	'A': {0x0e, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11},
	'B': {0x1e, 0x11, 0x11, 0x1e, 0x11, 0x11, 0x1e},
	'C': {0x0e, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0e},
	'D': {0x1e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1e},
	'E': {0x1f, 0x10, 0x10, 0x1e, 0x10, 0x10, 0x1f},
	'F': {0x1f, 0x10, 0x10, 0x1e, 0x10, 0x10, 0x10},
	'G': {0x0e, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0f},
	'H': {0x11, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11},
	'I': {0x0e, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0e},
	'J': {0x07, 0x02, 0x02, 0x02, 0x12, 0x12, 0x0c},
	'K': {0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11},
	'L': {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1f},
	'M': {0x11, 0x1b, 0x15, 0x15, 0x11, 0x11, 0x11},
	'N': {0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11},
	'O': {0x0e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e},
	'P': {0x1e, 0x11, 0x11, 0x1e, 0x10, 0x10, 0x10},
	'Q': {0x0e, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0d},
	'R': {0x1e, 0x11, 0x11, 0x1e, 0x14, 0x12, 0x11},
	'S': {0x0f, 0x10, 0x10, 0x0e, 0x01, 0x01, 0x1e},
	'T': {0x1f, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U': {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e},
	'V': {0x11, 0x11, 0x11, 0x11, 0x11, 0x0a, 0x04},
	'W': {0x11, 0x11, 0x11, 0x15, 0x15, 0x1b, 0x11},
	'X': {0x11, 0x11, 0x0a, 0x04, 0x0a, 0x11, 0x11},
	'Y': {0x11, 0x11, 0x0a, 0x04, 0x04, 0x04, 0x04},
	'Z': {0x1f, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1f},
}
