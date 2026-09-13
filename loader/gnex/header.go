package gnex

import (
	"encoding/binary"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

// ErrHeaderNotFound is returned when an .SGS payload does not contain the
// recognized SinjiSoft GVM header shape (see Header for what is known about
// it) within the leading bytes this package scans.
var ErrHeaderNotFound = errors.New("gnex: SGS header not found")

// maxHeaderScan bounds how far into an .SGS payload ParseHeader looks for the
// header shape. Every sample in the reference corpus (12 titles, two package
// generations) has it at offset 0; one repackaged title carries a 32-byte
// zero-padded prefix ahead of it. The margin above 32 is headroom for
// variants not present in the corpus, not a confirmed offset.
const maxHeaderScan = 48

// maxTitleBytes bounds the cp949/EUC-KR title string ParseHeader accepts.
// The longest title observed in the reference corpus is under 20 bytes;
// this is a generous ceiling against corrupt input, not an observed limit.
const maxTitleBytes = 96

// Header is what this package has reverse-engineered of the fixed-shape
// prefix SinjiSoft's GVM runtime ("SGS" = "Sinji Game Script") puts at the
// front of every .SGS payload, ahead of the GVM bytecode and resource body.
//
// Confirmed from static analysis of SinjiSoft's PC-side "cr32256_Magic.exe"
// player/emulator (which reports itself as "GVM 2x Emulator (128KB)") and
// cross-checked by round-tripping the title string against all 12 corpus
// titles' known display names:
//   - byte 0 is a format/VM version tag; only 1 and 2 are observed (matching
//     the emulator's "GVM1X"/"GVM2X" self-identification).
//   - byte 5 is constant 0x01 across every sample; combined with the title
//     decoding cleanly as cp949, this is the signature ParseHeader scans for.
//   - bytes 6:8 are a little-endian uint16 that behaves like a checksum of
//     the title bytes (every sample falls in 0x8400-0x85ff); its exact
//     algorithm is not reverse-engineered, so it is exposed but not
//     validated.
//   - the title string starts at byte 10, is cp949/EUC-KR encoded, and ends
//     at the first 0x00 0x00 pair (cp949 lead/trail bytes and ASCII text
//     never produce a bare 0x00, so this terminator is unambiguous).
//
// Bytes 1-4 and 8-9, and everything from BodyOffset onward (the GVM bytecode,
// symbol/media tables, and in-title image data), are not decoded here. See
// docs/gnex-format.md for what is and is not known about them.
type Header struct {
	// FormatVersion is the raw byte 0 value (observed: 1 or 2).
	FormatVersion byte
	// TitleChecksum is the raw little-endian bytes 6:8 value. Its algorithm
	// is not confirmed; treat it as informational, not a validated checksum.
	TitleChecksum uint16
	// Title is the cp949/EUC-KR-decoded title string.
	Title string
	// PrefixOffset is where this header was found. It is 0 for every corpus
	// sample but one, which carries a 32-byte zero prefix ahead of it.
	PrefixOffset int
	// BodyOffset is the offset of the first byte after the title's
	// terminator, i.e. where the undecoded GVM body begins.
	BodyOffset int
}

// ParseHeader scans the leading bytes of an .SGS payload for the GVM header
// shape described on Header, returning the first match. It returns
// ErrHeaderNotFound if no scanned offset matches.
func ParseHeader(data []byte) (Header, error) {
	limit := maxHeaderScan
	if limit > len(data) {
		limit = len(data)
	}
	for prefix := 0; prefix+10 <= limit; prefix++ {
		version := data[prefix]
		if version != 1 && version != 2 {
			continue
		}
		if data[prefix+5] != 1 {
			continue
		}
		titleStart := prefix + 10
		end, ok := findTitleEnd(data, titleStart)
		if !ok {
			continue
		}
		decoded, decodeErr := decodeEUCKR(data[titleStart:end])
		if decodeErr != nil || decoded == "" {
			continue
		}
		return Header{
			FormatVersion: version,
			TitleChecksum: binary.LittleEndian.Uint16(data[prefix+6 : prefix+8]),
			Title:         decoded,
			PrefixOffset:  prefix,
			BodyOffset:    end + 2,
		}, nil
	}
	return Header{}, ErrHeaderNotFound
}

// findTitleEnd looks for the 0x00 0x00 terminator that ends the title
// string starting at start, bounded by maxTitleBytes.
func findTitleEnd(data []byte, start int) (int, bool) {
	end := start
	for end+1 < len(data) {
		if data[end] == 0 && data[end+1] == 0 {
			return end, true
		}
		if end-start >= maxTitleBytes {
			return 0, false
		}
		end++
	}
	return 0, false
}

func decodeEUCKR(raw []byte) (string, error) {
	decoded, _, err := transform.Bytes(korean.EUCKR.NewDecoder(), raw)
	// The decoder replaces malformed sequences with RuneError without always
	// returning an error. Replacement characters and controls are not a clean
	// title decode, and must not turn unrelated bytes into a recognized SGS.
	if err != nil || !utf8.Valid(decoded) || strings.ContainsRune(string(decoded), utf8.RuneError) {
		return "", errors.New("gnex: invalid EUC-KR title")
	}
	for _, r := range string(decoded) {
		if unicode.IsControl(r) {
			return "", errors.New("gnex: control character in title")
		}
	}
	return string(decoded), nil
}

// standaloneHeader is deliberately narrower than the historical paired
// descriptor scanner. Without corroborating metadata, accept only the two
// observed placements: offset zero or a 32-byte all-zero prefix. Require a
// nonempty body, but make no claim that the undecoded body is valid bytecode.
func standaloneHeader(data []byte) (Header, error) {
	header, err := ParseHeader(data)
	if err != nil {
		return Header{}, err
	}
	if header.PrefixOffset != 0 && header.PrefixOffset != 32 {
		return Header{}, ErrHeaderNotFound
	}
	for _, b := range data[:header.PrefixOffset] {
		if b != 0 {
			return Header{}, ErrHeaderNotFound
		}
	}
	if header.BodyOffset >= len(data) {
		return Header{}, ErrHeaderNotFound
	}
	return header, nil
}
