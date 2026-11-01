package brewrt

import (
	"testing"
	"unicode/utf16"
)

// 드래곤판타지(작화) hands DrawText and MeasureText its EUC-KR char strings
// through the AECHAR pointer. A one-byte ASCII character shifts every later
// Korean pair across an AECHAR boundary, so the per-AECHAR packed reading
// cannot recover it and the intro and settings text came out as random
// Hangul (issue #566). The byte stream itself is plain KS X 1001 text.
func TestDecodeBREWAECHARReadsMisalignedEUCKRByteStream(t *testing.T) {
	for _, test := range []struct {
		units []uint16
		want  string
	}{
		// 전쟁에서 인간과, NUL in the high byte of the last unit.
		{[]uint16{0xfcc0, 0xefc0, 0xa1bf, 0xadbc, 0xc020, 0xb0ce, 0xb0a3, 0x00fa}, "전쟁에서 인간과"},
		// 때 현자'아스란'이
		{[]uint16{0xa7b6, 0xc720, 0xc0f6, 0x27da, 0xc6be, 0xbabd, 0xf5b6, 0xc027, 0x00cc}, "때 현자'아스란'이"},
		// 인간과 용의 공존, ending exactly on a unit boundary.
		{[]uint16{0xcec0, 0xa3b0, 0xfab0, 0xbf20, 0xc0eb, 0x20c7, 0xf8b0, 0xb8c1}, "인간과 용의 공존"},
		// 비, then a NUL and an older line still in the reused buffer.
		{[]uint16{0xf1ba, 0x4100, 0x4242}, "비"},
		// 론을펼쳤다. 용의: aligned pairs, but ". " shares one unit.
		{[]uint16{0xd0b7, 0xbbc0, 0xeec6, 0xc6c3, 0xd9b4, 0x202e, 0xebbf, 0xc7c0}, "론을펼쳤다. 용의"},
	} {
		if got := decodeBREWAECHAR(test.units); got != test.want {
			t.Fatalf("decodeBREWAECHAR(%04x) = %q, want %q", test.units, got, test.want)
		}
	}
}

// Genuine UTF-16 Hangul whose syllables happen to have an ASCII-range low
// byte must stay UTF-16: as a byte stream it leaves a lead byte unpaired.
func TestDecodeBREWAECHARKeepsUTF16WithASCIILowBytes(t *testing.T) {
	for _, test := range []struct {
		units []uint16
		want  string
	}{
		{[]uint16{0xb420, 0xae4c}, "될까"},
		{[]uint16{0xc548, 0xb155, 0x0020, 0xc138, 0xc694}, "안녕 세요"},
		{[]uint16{0x00a9, 0x0020, 0x0041}, "© A"},
		// Hanja in UTF-16 packs two ASCII-range bytes but pairs no Korean.
		{[]uint16{0x4e2d, 0x6587}, "中文"},
		// Corpus lines the first byte-stream rule misread: the first ASCII
		// character ends the bytes early, the Hangul before it reads as KS
		// pairs, and a later syllable such as 이 (0xc774) looks straddled.
		{utf16Units("헤헤.. 스팅의 이야기는 재밌어"), "헤헤.. 스팅의 이야기는 재밌어"},
		{utf16Units("료가 부과됩니다."), "료가 부과됩니다."},
		{utf16Units("트 일당들에 의해,"), "트 일당들에 의해,"},
		{utf16Units("어느날 무아칸이"), "어느날 무아칸이"},
		// One EUC-KR code per AECHAR with ASCII units between: the NUL
		// byte of "," must not end the text (라그나로크 매지션's tips).
		{[]uint16{0xdfc0, 0xe9b5, 0xeebe, 0xd6b5, 0x002c, 0x0020, 0xa7c0, 0xebb4, 0xd1c7}, "잘들어둬, 위대한"},
	} {
		if got := decodeBREWAECHAR(test.units); got != test.want {
			t.Fatalf("decodeBREWAECHAR(%04x) = %q, want %q", test.units, got, test.want)
		}
	}
}

// The counted form: 드래곤판타지(작화) draws its "OFF" label from a byte
// string with a count of three characters. Read as UTF-16 the label became
// "䙏F", a CJK glyph that overlapped the 튜토리얼 label beside it.
func TestDisplayTextReadsCountedEUCKRByteString(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	textAt := heapBase + 0x200
	for _, test := range []struct {
		data  []byte
		count uint32
		want  string
	}{
		{[]byte("OFF\x00\x00\x00"), 3, "OFF"},
		// 게임 시작 is five characters in nine bytes.
		{[]byte{0xb0, 0xd4, 0xc0, 0xd3, 0x20, 0xbd, 0xc3, 0xc0, 0xdb, 0x00}, 5, "게임 시작"},
		// Genuine counted UTF-16 is untouched.
		{[]byte{'A', 0, 'B', 0}, 2, "AB"},
		{[]byte{0x00, 0xac, 0x98, 0xb0}, 2, "가나"},
		// Counted UTF-16 "헤헤." also ends at a NUL after three byte
		// characters, but no unit before that NUL straddles a boundary.
		{[]byte{0xe4, 0xd5, 0xe4, 0xd5, 0x2e, 0x00}, 3, "헤헤."},
	} {
		if err := runtime.cpu.WriteMemory(textAt, append(test.data, make([]byte, 16)...)); err != nil {
			t.Fatal(err)
		}
		got, err := runtime.displayText(textAt, test.count, false)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("displayText(% x, %d) = %q, want %q", test.data, test.count, got, test.want)
		}
	}
}

func utf16Units(text string) []uint16 {
	return utf16.Encode([]rune(text))
}
