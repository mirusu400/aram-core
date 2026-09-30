package gnex

import (
	"bytes"
	"encoding/binary"
)

// GNEX32Symbol describes one four-byte-word symbol in a GNEX version 4 image.
// The descriptor flags are retained without assigning their runtime meaning.
type GNEX32Symbol struct {
	Flags uint16
	Words uint16
	Data  []byte
}

// GNEX32Media describes one byte-addressed media record in a GNEX version 4
// image. The descriptor flags are retained without assigning a resource type.
type GNEX32Media struct {
	Flags uint32
	Data  []byte
}

// GNEX32Image owns the structurally decoded 32-bit image. Code, symbols, and
// media alias Buffer. EntryField preserves the raw header value; Entry adds the
// observed 32-byte prefix, consistent with the table pointers. CodePointers
// exposes the eleven candidate pointer slots at header offsets 0x1c..0x44;
// zero denotes an absent slot. Their event meanings and execution have not
// been validated. Decoding does not infer mutability.
type GNEX32Image struct {
	Header       Header
	Buffer       []byte
	Entry        uint32
	EntryField   uint32
	CodePointers [11]uint32
	Code         []byte
	Symbols      []GNEX32Symbol
	Media        []GNEX32Media
}

// GNEX32AddressInstruction describes the storage of a recognized
// address-bearing code word. Target and Next are byte offsets into Code.
// Word is an uninterpreted operand present only when HasWord is true.
// It deliberately does not assign branch, call, or stack semantics.
type GNEX32AddressInstruction struct {
	Opcode  uint16
	Word    uint16
	HasWord bool
	Target  int
	Next    int
}

// DecodeCodeAddress reads a candidate code address from two little-endian
// 16-bit words at operandOffset in Code. The high word precedes the low word;
// the resulting address is relative to the 32-byte SGS prefix. The returned
// index is relative to Code. This storage order is observed in the version-4
// sample's repeated control-like sequences; it does not assign an opcode or
// control-flow meaning to the operand.
func (image GNEX32Image) DecodeCodeAddress(operandOffset int) (int, error) {
	location := int64(image.Header.BodyOffset) + int64(operandOffset)
	if operandOffset < 0 || operandOffset > len(image.Code) || len(image.Code)-operandOffset < 4 {
		return 0, formatError("SGS", location, "GNEX32 code address operand is truncated")
	}
	high := uint32(binary.LittleEndian.Uint16(image.Code[operandOffset:]))
	low := uint32(binary.LittleEndian.Uint16(image.Code[operandOffset+2:]))
	address := high<<16 | low
	const codeStartRelative = 0x80
	if address < codeStartRelative || address-codeStartRelative >= uint32(len(image.Code)) || address&1 != 0 {
		return 0, formatError("SGS", location, "GNEX32 code address outside aligned code")
	}
	return int(address - codeStartRelative), nil
}

// DecodeAddressInstruction reads the observed GNEX32 address-bearing layouts
// at a known instruction boundary. The seven listed opcodes are structural
// candidates from the version-4 sample; this does not establish their effects
// or that an arbitrary aligned word is an instruction boundary.
func (image GNEX32Image) DecodeAddressInstruction(codeOffset int) (GNEX32AddressInstruction, error) {
	location := int64(image.Header.BodyOffset) + int64(codeOffset)
	if codeOffset < 0 || codeOffset&1 != 0 || codeOffset > len(image.Code) || len(image.Code)-codeOffset < 2 {
		return GNEX32AddressInstruction{}, formatError("SGS", location, "GNEX32 opcode outside aligned code")
	}
	opcode := binary.LittleEndian.Uint16(image.Code[codeOffset:])
	instruction := GNEX32AddressInstruction{Opcode: opcode}
	addressOffset := codeOffset + 2
	switch opcode {
	case 0x7b, 0x7c, 0x7d, 0x7e, 0x7f, 0x80:
		if len(image.Code)-addressOffset < 2 {
			return GNEX32AddressInstruction{}, formatError("SGS", location, "GNEX32 instruction operand is truncated")
		}
		instruction.Word = binary.LittleEndian.Uint16(image.Code[addressOffset:])
		instruction.HasWord = true
		addressOffset += 2
	case 0x81, 0x82, 0x83, 0x85:
	default:
		return GNEX32AddressInstruction{}, formatError("SGS", location, "GNEX32 opcode has no known address layout")
	}
	target, err := image.DecodeCodeAddress(addressOffset)
	if err != nil {
		return GNEX32AddressInstruction{}, err
	}
	instruction.Target = target
	instruction.Next = addressOffset + 4
	return instruction, nil
}

// DecodeGNEX32Image decodes the observed prefixed GNEX version 4 layout. The
// decoded table offsets are relative to the 32-byte prefix. It rejects
// incomplete descriptors and non-exact data spans before allocating any views.
func DecodeGNEX32Image(data []byte) (GNEX32Image, error) {
	bad := func(offset int, reason string) (GNEX32Image, error) {
		return GNEX32Image{}, formatError("SGS", int64(offset), reason)
	}
	const prefix = 32
	const codeStart = prefix + 0x80
	if len(data) < codeStart || len(data) > int(MaxMemberSize) {
		return bad(0, "GNEX32 image size outside supported bounds")
	}
	if binary.LittleEndian.Uint32(data[:4]) != prefix ||
		!bytes.Equal(data[4:prefix], make([]byte, prefix-4)) ||
		data[prefix] != 4 || data[prefix+5] != 4 {
		return GNEX32Image{}, ErrUnsupportedExecutionVariant
	}
	if int(binary.LittleEndian.Uint32(data[prefix+0x78:])) != len(data)-prefix {
		return bad(prefix+0x78, "GNEX32 image length mismatch")
	}
	word := func(offset int) uint32 {
		return binary.LittleEndian.Uint32(data[prefix+offset:])
	}
	abs := func(value uint32) (int, bool) {
		if uint64(value)+prefix > uint64(len(data)) {
			return 0, false
		}
		return int(value) + prefix, true
	}
	ds, okDS := abs(word(0x48))
	ps, okPS := abs(word(0x4c))
	dm, okDM := abs(word(0x50))
	pm, okPM := abs(word(0x54))
	if !okDS || !okPS || !okDM || !okPM ||
		ds < codeStart || ds > ps || ps > dm || dm > pm || pm > len(data) {
		return bad(prefix+0x48, "invalid GNEX32 table ordering")
	}
	if (ps-ds)%8 != 0 || (pm-dm)%12 != 0 {
		return bad(prefix+0x48, "incomplete GNEX32 descriptor")
	}
	entryField := word(0x1c)
	var codePointers [11]uint32
	for i := range codePointers {
		fieldOffset := 0x1c + 4*i
		field := word(fieldOffset)
		if field == 0 && i > 0 {
			continue
		}
		pointer, ok := abs(field)
		if !ok || pointer < codeStart || pointer >= ds {
			return bad(prefix+fieldOffset, "GNEX32 code pointer outside code")
		}
		codePointers[i] = uint32(pointer)
	}
	const maxDescriptors = 1 << 16
	ns, nm := (ps-ds)/8, (pm-dm)/12
	if ns > maxDescriptors || nm > maxDescriptors {
		return bad(prefix+0x48, "GNEX32 descriptor count exceeds limit")
	}
	for i, cursor := 0, ps; i < ns; i++ {
		offset := ds + 8*i
		size := uint64(binary.LittleEndian.Uint16(data[offset+2:])) * 4
		if size > uint64(dm-cursor) {
			return bad(offset, "GNEX32 symbol data exceeds span")
		}
		cursor += int(size)
		if i == ns-1 && cursor != dm {
			return bad(offset, "GNEX32 symbol data span is not exact")
		}
	}
	if ns == 0 && ps != dm {
		return bad(ps, "GNEX32 symbol data without descriptors")
	}
	for i, cursor := 0, pm; i < nm; i++ {
		offset := dm + 12*i
		size := uint64(binary.LittleEndian.Uint32(data[offset+4:]))
		if size > uint64(len(data)-cursor) {
			return bad(offset, "GNEX32 media data exceeds span")
		}
		cursor += int(size)
		if i == nm-1 && cursor != len(data) {
			return bad(offset, "GNEX32 media data span is not exact")
		}
	}
	if nm == 0 && pm != len(data) {
		return bad(pm, "GNEX32 media data without descriptors")
	}
	titleRaw := data[prefix+10 : prefix+24]
	if end := bytes.IndexByte(titleRaw, 0); end >= 0 {
		titleRaw = titleRaw[:end]
	}
	title, err := decodeEUCKR(titleRaw)
	if err != nil || title == "" {
		return bad(prefix+10, "invalid GNEX32 title")
	}
	image := GNEX32Image{
		Header: Header{FormatVersion: 4, TitleChecksum: binary.LittleEndian.Uint16(data[prefix+6:]), Title: title, PrefixOffset: prefix, BodyOffset: codeStart},
		Buffer: bytes.Clone(data), Entry: codePointers[0], EntryField: entryField,
		CodePointers: codePointers,
		Symbols:      make([]GNEX32Symbol, ns), Media: make([]GNEX32Media, nm),
	}
	image.Code = image.Buffer[codeStart:ds]
	for i, cursor := 0, ps; i < ns; i++ {
		offset := ds + 8*i
		size := 4 * int(binary.LittleEndian.Uint16(data[offset+2:]))
		image.Symbols[i] = GNEX32Symbol{Flags: binary.LittleEndian.Uint16(data[offset:]), Words: uint16(size / 4), Data: image.Buffer[cursor : cursor+size : cursor+size]}
		cursor += size
	}
	for i, cursor := 0, pm; i < nm; i++ {
		offset := dm + 12*i
		size := int(binary.LittleEndian.Uint32(data[offset+4:]))
		image.Media[i] = GNEX32Media{Flags: binary.LittleEndian.Uint32(data[offset:]), Data: image.Buffer[cursor : cursor+size : cursor+size]}
		cursor += size
	}
	return image, nil
}
