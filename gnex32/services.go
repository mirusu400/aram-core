package gnex32

import "sort"

var gnex32ServiceNames = map[uint16]string{
	0x01: "GetSysMin", 0x02: "GetSysUserID", 0x04: "GetDate", 0x05: "GetTime", 0x07: "BackToBrowser",
	0x22: "SetClip", 0x23: "ResetClip", 0x25: "SetGamma", 0x26: "SetColor",
	0x27: "SetColorRGB", 0x28: "SetFontType", 0x29: "SetFont", 0x2f: "SetPaletteColorRGB",
	0x36: "Clear", 0x38: "ClearRGB", 0x3a: "ClearWhite", 0x3b: "ClearBlack",
	0x42: "DrawLine", 0x43: "DrawHLine", 0x44: "DrawVLine", 0x45: "DrawRect", 0x46: "FillRect",
	0x47: "FillRectEx", 0x48: "DrawRectRound", 0x49: "FillRectRound",
	0x51: "SaveLCD", 0x52: "RestoreLCD", 0x55: "DrawStr", 0x56: "DrawStrSolid",
	0x5f: "Flush", 0x60: "FlushPartial", 0x82: "PlaySound", 0x83: "StopSound",
	0x8e: "StartVib", 0x8f: "StopVib",
	0x90: "SetKeyTone", 0x91: "SetBackLight", 0x93: "GetUserNV", 0x94: "PutUserNV",
	0x95: "SetTimer0", 0x96: "SetTimer1", 0x97: "SetTimer2",
	0x98: "ResetTimer0", 0x99: "ResetTimer1", 0x9a: "ResetTimer2",
	0x9d: "GetMediaSize", 0x9e: "SetMediaSize", 0x9f: "StrInit", 0xa0: "StrLen",
	0xa1: "StrCpy", 0xa2: "StrSub", 0xa3: "StrCat", 0xa4: "StrCmp", 0xa5: "GetChar",
	0xa6: "PutChar", 0xa7: "AsciiToInt", 0xa8: "IntToAscii", 0xa9: "PutByte",
	0xaa: "GetByte", 0xab: "PutBytes", 0xac: "GetBytes", 0xad: "MakeStrStr",
	0xae: "MakeStr1", 0xaf: "MakeStr2", 0xb0: "MakeStr3", 0xb1: "MakeStr4", 0xb2: "MakeStr5",
	0xc8: "RandSeed", 0xc9: "Rand", 0xcb: "Abs", 0xcd: "Sin100", 0xce: "Cos100",
	0xd6: "Max3", 0xd8: "Min", 0xdd: "ArrayToVar",
}

// ServiceName reports the SDK-style name of a service implemented by Runtime.
func ServiceName(id uint16) (string, bool) {
	name, ok := gnex32ServiceNames[id]
	return name, ok
}

// SupportedServiceIDs returns the implemented service IDs in ascending order.
func SupportedServiceIDs() []uint16 {
	ids := make([]uint16, 0, len(gnex32ServiceNames))
	for id := range gnex32ServiceNames {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
