package gvmhost

import (
	"errors"
	"fmt"
	"image/color"
)

var ErrUnsupportedSKTPaletteIndex = errors.New("gvmhost: unsupported SKT palette index")

// SKTCompatibilityPalette supplies the exact selected-corpus palette-to-packed
// mapping and a portable RGB332 presentation policy. The latter is deliberately
// independent of the unresolved native normal-orientation component ramps.
type SKTCompatibilityPalette struct{}

func (SKTCompatibilityPalette) Map(mapping uint8, selector int16) (byte, error) {
	if selector < 0 || selector > 181 {
		return 0, fmt.Errorf("%w: mapping %d selector %d", ErrUnsupportedSKTPaletteIndex, mapping, selector)
	}
	packed, ok := SKTGammaColor(mapping, uint8(selector))
	if !ok {
		return 0, fmt.Errorf("%w: mapping %d selector %d", ErrUnsupportedSKTPaletteIndex, mapping, selector)
	}
	return packed, nil
}

func (SKTCompatibilityPalette) Color(index byte) color.RGBA {
	switch index {
	case 0x00:
		return color.RGBA{A: 0xff}
	case 0x49:
		return color.RGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}
	case 0x92:
		return color.RGBA{R: 0xc0, G: 0xc0, B: 0xc0, A: 0xff}
	case 0xff:
		return color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	default:
		return color.RGBA{
			R: uint8(uint16(index>>5) * 255 / 7),
			G: uint8(uint16((index>>2)&7) * 255 / 7),
			B: uint8(uint16(index&3) * 255 / 3),
			A: 0xff,
		}
	}
}

// SKTGammaColor converts the public Mobile C palette indices used by the
// selected SKT corpus into the packed byte consumed by the GVM display path.
//
// The supported subset includes fixed and normal colors, plus the initial
// phase of the reference player's blinking colors. The display currently
// holds that phase steady. Index 4 is transparent and returns
// a zero byte with ok=true; callers must preserve transparency separately.
func SKTGammaColor(gamma, index uint8) (packed byte, ok bool) {
	if gamma > 6 {
		return 0, false
	}

	switch index {
	case 0: // white
		packed = 0xff
	case 1: // light gray
		packed = 0x92
	case 2: // dark gray
		packed = 0x49
	case 3, 4: // black, transparent
		packed = 0
	case 5, 11, 15:
		packed = 0xff
	case 6, 8, 12:
		packed = 0x92
	case 7, 9, 13:
		packed = 0x49
	case 10, 14:
		packed = 0
	default:
		switch {
		case index >= 16 && index <= 139:
			packed = normalSKTPaletteByte(index)
		case index >= 140 && index <= 181:
			packed = initialSKTBlinkColor(index)
		default:
			return 0, false
		}
	}

	return applySKTGamma(gamma, packed), true
}

// initialSKTBlinkColor returns the RGB332 color selected at the first blink
// phase. Each three-selector group uses black or one of the standard RGB332
// primaries and mixtures. Time-dependent palette cycling is separate.
func initialSKTBlinkColor(index uint8) byte {
	colors := [...][3]byte{
		{0, 0xe0, 0x1c}, {0, 0xfc, 0x48}, {0, 0x1c, 0x08},
		{0, 0x1f, 0x09}, {0, 0x03, 0x01}, {0, 0xe3, 0x41},
		{0, 0xe0, 0x40}, {0xe0, 0, 0xfc}, {0x48, 0xfd, 0},
		{0x1c, 0x08, 0x1c}, {0, 0x1f, 0x09}, {0x1f, 0, 0x03},
		{0x01, 0x03, 0}, {0xe3, 0x41, 0xe3},
	}
	offset := index - 140
	return colors[offset/3][offset%3]
}

// normalSKTPaletteByte is the compact monotonic form of the documented
// 0x10..0x8b normal-color range. It produces 124 distinct packed RGB332 bytes
// without embedding a copied lookup table.
func normalSKTPaletteByte(index uint8) byte {
	value := int(index) - 15
	if index >= 47 {
		value += 32
	}
	if index >= 56 {
		value++
	}
	if index >= 78 {
		value += 32
	}
	if index >= 96 {
		value++
	}
	if index >= 109 {
		value += 64
	}
	return byte(value)
}

func applySKTGamma(gamma uint8, packed byte) byte {
	red := [...][8]byte{
		{5, 5, 6, 6, 7, 7, 7, 7},
		{4, 4, 5, 5, 6, 6, 7, 7},
		{2, 3, 4, 4, 5, 6, 7, 7},
		{0, 1, 2, 3, 4, 5, 6, 7},
		{0, 0, 1, 1, 1, 2, 2, 2},
		{0, 0, 0, 1, 1, 1, 1, 1},
		{},
	}
	green := red
	green[1] = [8]byte{4, 4, 5, 5, 6, 7, 7, 7}
	blue := [...][4]byte{
		{3, 3, 3, 3},
		{2, 3, 3, 3},
		{1, 2, 3, 3},
		{0, 1, 2, 3},
		{0, 0, 1, 1},
		{},
		{},
	}
	return red[gamma][packed>>5]<<5 |
		green[gamma][(packed>>2)&7]<<2 |
		blue[gamma][packed&3]
}
