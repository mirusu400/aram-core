package gnex

import (
	"bytes"
	"errors"
	"testing"

	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

// eucKR encodes a UTF-8 string to cp949/EUC-KR bytes for test fixtures, or
// fails the test - every fixture title here is chosen to be representable.
func eucKR(t *testing.T, s string) []byte {
	t.Helper()
	encoded, _, err := transform.Bytes(korean.EUCKR.NewEncoder(), []byte(s))
	if err != nil {
		t.Fatalf("encode %q as EUC-KR: %v", s, err)
	}
	return encoded
}

// buildSGS assembles a minimal synthetic .SGS payload: prefixZeros zero
// bytes, then the header shape ParseHeader looks for, the given title, its
// terminator, and trailing body bytes.
func buildSGS(version byte, prefixZeros int, title []byte, body []byte) []byte {
	var buf bytes.Buffer
	buf.Write(make([]byte, prefixZeros))
	buf.WriteByte(version)                    // byte 0: format version
	buf.Write([]byte{0xff, 0x0c, 0xff, 0xff}) // bytes 1-4: unmodeled
	buf.WriteByte(0x01)                       // byte 5: constant
	buf.Write([]byte{0x00, 0x85})             // bytes 6-7: title checksum (arbitrary)
	buf.Write([]byte{0x00, 0x00})             // bytes 8-9: unmodeled
	buf.Write(title)
	buf.Write([]byte{0x00, 0x00}) // title terminator
	buf.Write(body)
	return buf.Bytes()
}

func TestParseHeaderAtOffsetZero(t *testing.T) {
	title := eucKR(t, "강호동의천생연분")
	data := buildSGS(1, 0, title, []byte{0x64, 0x00, 0x00, 0x00})

	header, err := ParseHeader(data)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if header.FormatVersion != 1 {
		t.Errorf("FormatVersion = %d, want 1", header.FormatVersion)
	}
	if header.Title != "강호동의천생연분" {
		t.Errorf("Title = %q, want %q", header.Title, "강호동의천생연분")
	}
	if header.PrefixOffset != 0 {
		t.Errorf("PrefixOffset = %d, want 0", header.PrefixOffset)
	}
	wantBody := 10 + len(title) + 2
	if header.BodyOffset != wantBody {
		t.Errorf("BodyOffset = %d, want %d", header.BodyOffset, wantBody)
	}
	if !bytes.Equal(data[header.BodyOffset:], []byte{0x64, 0x00, 0x00, 0x00}) {
		t.Errorf("body bytes after BodyOffset = %x, want 64000000", data[header.BodyOffset:])
	}
}

func TestParseHeaderWithZeroPaddedPrefix(t *testing.T) {
	// Mirrors the one corpus title (창세기전 외전 크로우) whose repack
	// carries a 32-byte zero prefix ahead of the normal header shape.
	title := eucKR(t, "창세기전외전")
	data := buildSGS(2, 32, title, nil)

	header, err := ParseHeader(data)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if header.PrefixOffset != 32 {
		t.Errorf("PrefixOffset = %d, want 32", header.PrefixOffset)
	}
	if header.Title != "창세기전외전" {
		t.Errorf("Title = %q, want %q", header.Title, "창세기전외전")
	}
}

func TestParseHeaderVersion2(t *testing.T) {
	title := eucKR(t, "놈")
	data := buildSGS(2, 0, title, nil)

	header, err := ParseHeader(data)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if header.FormatVersion != 2 {
		t.Errorf("FormatVersion = %d, want 2", header.FormatVersion)
	}
}

func TestParseHeaderRejectsUnrelatedData(t *testing.T) {
	cases := map[string][]byte{
		"empty":            {},
		"too short":        {0x01, 0x02, 0x03},
		"random bytes":     bytes.Repeat([]byte{0xaa, 0x55}, 32),
		"zeroes only":      make([]byte, 128),
		"no title, no NUL": append([]byte{0x01, 0, 0, 0, 0, 0x01, 0, 0, 0, 0}, bytes.Repeat([]byte{0x41}, 200)...),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseHeader(data); !errors.Is(err, ErrHeaderNotFound) {
				t.Errorf("ParseHeader(%s) error = %v, want ErrHeaderNotFound", name, err)
			}
		})
	}
}

func TestParseHeaderRejectsVersionOutOfRange(t *testing.T) {
	title := eucKR(t, "북벌")
	data := buildSGS(3, 0, title, nil) // only 1 and 2 are observed versions
	if _, err := ParseHeader(data); !errors.Is(err, ErrHeaderNotFound) {
		t.Errorf("ParseHeader error = %v, want ErrHeaderNotFound", err)
	}
}

func TestParseHeaderRejectsOversizedTitle(t *testing.T) {
	data := buildSGS(1, 0, bytes.Repeat([]byte{0x41}, maxTitleBytes+10), nil)
	if _, err := ParseHeader(data); !errors.Is(err, ErrHeaderNotFound) {
		t.Errorf("ParseHeader error = %v, want ErrHeaderNotFound", err)
	}
}
