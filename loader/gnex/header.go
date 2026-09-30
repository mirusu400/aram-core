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

// ErrHeaderNotFound is returned when an .SGS payload does not contain a
// recognized SinjiSoft GVM/GNEX header shape.
var ErrHeaderNotFound = errors.New("gnex: SGS header not found")

// maxHeaderScan bounds the legacy GVM header scan. In the older 12-title
// reference corpus, one repackaged title has a 32-byte zero prefix; the
// others have a header at offset zero. GNEX version 4 has a separate decoder.
const maxHeaderScan = 48

// maxTitleBytes bounds the cp949/EUC-KR title string ParseHeader accepts.
// The longest title observed in the reference corpus is under 20 bytes;
// this is a generous ceiling against corrupt input, not an observed limit.
const maxTitleBytes = 96

// Header describes the recognized prefix of a SinjiSoft SGS payload. The
// following notes concern the legacy GVM version-1/2 header; the prefixed
// version-4 GNEX layout is decoded separately by DecodeGNEX32Image.
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
	// FormatVersion is the raw version byte (observed: 1, 2, or 4).
	FormatVersion byte
	// TitleChecksum is the raw little-endian bytes 6:8 value. Its checksum
	// interpretation for legacy GVM is unconfirmed; its meaning in version 4
	// is unknown. Treat it as informational only.
	TitleChecksum uint16
	// Title is the cp949/EUC-KR-decoded title string.
	Title string
	// PrefixOffset is where this header was found. Legacy samples use zero
	// or a 32-byte zero prefix; the observed version-4 sample has a 32-byte
	// length prefix.
	PrefixOffset int
	// BodyOffset is the first byte after the legacy title terminator,
	// or the observed code start for a GNEX version-4 image.
	BodyOffset int
}

// ParseHeader scans for a legacy GVM header, then tries the structurally
// validated prefixed GNEX version-4 layout. It returns ErrHeaderNotFound if
// neither shape matches.
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
	if image, err := DecodeGNEX32Image(data); err == nil {
		return image.Header, nil
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
// descriptor scanner. Without corroborating metadata, accept the observed
// zero/unprefixed legacy placements or a fully validated 32-byte-prefixed
// GNEX32 image. Require a nonempty body, without claiming bytecode execution.
func standaloneHeader(data []byte) (Header, error) {
	header, err := ParseHeader(data)
	if err != nil {
		return Header{}, err
	}
	if header.PrefixOffset != 0 && header.PrefixOffset != 32 {
		return Header{}, ErrHeaderNotFound
	}
	if header.FormatVersion != 4 {
		for _, b := range data[:header.PrefixOffset] {
			if b != 0 {
				return Header{}, ErrHeaderNotFound
			}
		}
	}
	if header.BodyOffset >= len(data) {
		return Header{}, ErrHeaderNotFound
	}
	return header, nil
}
