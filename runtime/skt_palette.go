package runtime

import "image/color"

// SKTGammaColor converts a public Mobile C palette selector into the packed
// RGB332 value used by the SKT display path at gamma levels 0 through 6.
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

// SKTGammaRGBA applies the same handset gamma curve to a true-color value.
// Gamma 3 is the documented neutral setting and preserves all source bits.
func SKTGammaRGBA(gamma uint8, value color.RGBA) (color.RGBA, bool) {
	if gamma > 6 {
		return color.RGBA{}, false
	}
	if value.A == 0 || gamma == 3 {
		return value, true
	}
	packed := byte(uint16(value.R)*7/255)<<5 |
		byte(uint16(value.G)*7/255)<<2 |
		byte(uint16(value.B)*3/255)
	result := SKTPackedColor(applySKTGamma(gamma, packed))
	result.A = value.A
	return result, true
}

// SKTPackedColor expands an RGB332 display value to portable RGBA.
func SKTPackedColor(index byte) color.RGBA {
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
