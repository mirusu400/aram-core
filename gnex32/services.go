package gnex32

import "sort"

// ServiceContract is the successful value-stack effect of one SDK service.
// Pop counts arguments removed from the top of the stack; Push counts return
// values appended in their place. Failed services leave the VM stack intact.
type ServiceContract struct {
	Name string
	Pop  int
	Push int
}

var gnex32ServiceContracts = map[uint16]ServiceContract{
	0x01: {"GetSysMin", 1, 0},
	0x02: {"GetSysUserID", 1, 1},
	0x04: {"GetDate", 1, 0},
	0x05: {"GetTime", 1, 0},
	0x07: {"BackToBrowser", 1, 0},

	0x22: {"SetClip", 4, 0},
	0x23: {"ResetClip", 0, 0},
	0x25: {"SetGamma", 1, 0},
	0x26: {"SetColor", 1, 0},
	0x27: {"SetColorRGB", 3, 0},
	0x28: {"SetFontType", 4, 0},
	0x29: {"SetFont", 1, 0},
	0x2f: {"SetPaletteColorRGB", 5, 1},

	0x36: {"Clear", 1, 0},
	0x38: {"ClearRGB", 3, 0},
	0x3a: {"ClearWhite", 0, 0},
	0x3b: {"ClearBlack", 0, 0},

	0x42: {"DrawLine", 4, 0},
	0x43: {"DrawHLine", 3, 0},
	0x44: {"DrawVLine", 3, 0},
	0x45: {"DrawRect", 4, 0},
	0x46: {"FillRect", 4, 0},
	0x47: {"FillRectEx", 5, 0},
	0x48: {"DrawRectRound", 5, 0},
	0x49: {"FillRectRound", 5, 0},

	0x51: {"SaveLCD", 0, 0},
	0x52: {"RestoreLCD", 0, 0},
	0x55: {"DrawStr", 3, 0},
	0x56: {"DrawStrSolid", 3, 0},
	0x5f: {"Flush", 0, 0},
	0x60: {"FlushPartial", 4, 0},

	0x82: {"PlaySound", 1, 0},
	0x83: {"StopSound", 0, 0},
	0x8e: {"StartVib", 1, 0},
	0x8f: {"StopVib", 0, 0},
	0x90: {"SetKeyTone", 1, 0},
	0x91: {"SetBackLight", 1, 0},
	0x93: {"GetUserNV", 2, 0},
	0x94: {"PutUserNV", 2, 0},
	0x95: {"SetTimer0", 2, 0},
	0x96: {"SetTimer1", 2, 0},
	0x97: {"SetTimer2", 2, 0},
	0x98: {"ResetTimer0", 0, 0},
	0x99: {"ResetTimer1", 0, 0},
	0x9a: {"ResetTimer2", 0, 0},

	0x9d: {"GetMediaSize", 1, 1},
	0x9e: {"SetMediaSize", 2, 1},
	0x9f: {"StrInit", 2, 0},
	0xa0: {"StrLen", 1, 1},
	0xa1: {"StrCpy", 2, 0},
	0xa2: {"StrSub", 4, 0},
	0xa3: {"StrCat", 2, 0},
	0xa4: {"StrCmp", 2, 1},
	0xa5: {"GetChar", 2, 1},
	0xa6: {"PutChar", 3, 0},
	0xa7: {"AsciiToInt", 1, 1},
	0xa8: {"IntToAscii", 2, 0},
	0xa9: {"PutByte", 3, 0},
	0xaa: {"GetByte", 2, 1},
	0xab: {"PutBytes", 4, 0},
	0xac: {"GetBytes", 4, 0},
	0xad: {"MakeStrStr", 3, 0},
	0xae: {"MakeStr1", 3, 0},
	0xaf: {"MakeStr2", 4, 0},
	0xb0: {"MakeStr3", 5, 0},
	0xb1: {"MakeStr4", 6, 0},
	0xb2: {"MakeStr5", 7, 0},

	0xc8: {"RandSeed", 1, 0},
	0xc9: {"Rand", 2, 1},
	0xcb: {"Abs", 1, 1},
	0xcd: {"Sin100", 1, 1},
	0xce: {"Cos100", 1, 1},
	0xd6: {"Max3", 3, 1},
	0xd8: {"Min", 2, 1},
	0xdd: {"ArrayToVar", 4, 0},
	0xe1: {"Exit", 0, 0},

	0x101: {"NetConnect", 2, 0},
	0x102: {"NetDisconnect", 0, 0},
	0x104: {"NetSend", 2, 0},
	0x1d8: {"GetSysInfo", 2, 1},
}

// ServiceContractFor returns the SDK-style name and successful stack effect of
// a service implemented by Runtime.
func ServiceContractFor(id uint16) (ServiceContract, bool) {
	contract, ok := gnex32ServiceContracts[id]
	return contract, ok
}

// ServiceName reports the SDK-style name of a service implemented by Runtime.
func ServiceName(id uint16) (string, bool) {
	contract, ok := ServiceContractFor(id)
	return contract.Name, ok
}

// SupportedServiceIDs returns the implemented service IDs in ascending order.
func SupportedServiceIDs() []uint16 {
	ids := make([]uint16, 0, len(gnex32ServiceContracts))
	for id := range gnex32ServiceContracts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
