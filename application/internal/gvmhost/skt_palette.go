package gvmhost

// SKTGammaColor converts the public Mobile C palette indices used by the
// selected SKT corpus into the packed byte consumed by the GVM display path.
//
// The supported subset is deliberate: the four documented fixed colors and
// the 124 normal colors. Gray-blink and color-blink indices remain unsupported
// until their temporal behavior is modeled. Index 4 is transparent and returns
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
	default:
		if index < 16 || index > 139 {
			return 0, false
		}
		packed = normalSKTPaletteByte(index)
	}

	return applySKTGamma(gamma, packed), true
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
