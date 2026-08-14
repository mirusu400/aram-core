package skvm

import "unicode/utf16"

// Java DataOutputStream.writeUTF uses modified UTF-8: NUL is a two-byte
// sequence and supplementary code points are encoded as UTF-16 surrogates.
func decodeModifiedUTF8(data []byte) (string, bool) {
	units := make([]uint16, 0, len(data))
	for offset := 0; offset < len(data); {
		first := data[offset]
		switch {
		case first > 0 && first < 0x80:
			units = append(units, uint16(first))
			offset++
		case first&0xe0 == 0xc0 && offset+1 < len(data):
			second := data[offset+1]
			if second&0xc0 != 0x80 {
				return "", false
			}
			value := uint16(first&0x1f)<<6 | uint16(second&0x3f)
			if value != 0 && value < 0x80 {
				return "", false
			}
			units = append(units, value)
			offset += 2
		case first&0xf0 == 0xe0 && offset+2 < len(data):
			second, third := data[offset+1], data[offset+2]
			if second&0xc0 != 0x80 || third&0xc0 != 0x80 {
				return "", false
			}
			value := uint16(first&0x0f)<<12 |
				uint16(second&0x3f)<<6 | uint16(third&0x3f)
			if value < 0x800 {
				return "", false
			}
			units = append(units, value)
			offset += 3
		default:
			return "", false
		}
	}
	for index, unit := range units {
		if 0xd800 <= unit && unit <= 0xdbff {
			if index+1 >= len(units) || units[index+1] < 0xdc00 ||
				units[index+1] > 0xdfff {
				return "", false
			}
		} else if 0xdc00 <= unit && unit <= 0xdfff &&
			(index == 0 || units[index-1] < 0xd800 || units[index-1] > 0xdbff) {
			return "", false
		}
	}
	return string(utf16.Decode(units)), true
}
