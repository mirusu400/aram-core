package application

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"testing"
)

// Characterization tests locking the savestate primitive codec byte layout
// before guest.StateWriter/guest.StateDecoder move from state.go to state_io.go. The
// savestate format is frozen; these bytes must never change.

func TestStateWriterGoldenBytes(t *testing.T) {
	var buffer bytes.Buffer
	writer := guest.NewStateWriter(&buffer)
	writer.U8(0xab)
	writer.U32(0x12345678)
	writer.U64(0x1122334455667788)
	writer.String16("aram")
	if writer.Err != nil {
		t.Fatalf("writer error: %v", writer.Err)
	}
	const wantHex = "ab" + "78563412" + "8877665544332211" + "0400" + "6172616d"
	if got := hex.EncodeToString(buffer.Bytes()); got != wantHex {
		t.Fatalf("guest.StateWriter bytes = %s, want %s", got, wantHex)
	}
	if writer.Offset != int64(buffer.Len()) {
		t.Fatalf("guest.StateWriter offset = %d, want %d", writer.Offset, buffer.Len())
	}
	sum := sha256.Sum256(buffer.Bytes())
	if !bytes.Equal(writer.Digest(), sum[:]) {
		t.Fatalf("guest.StateWriter digest mismatch")
	}
}

func TestStateDecoderGoldenRoundTrip(t *testing.T) {
	raw, err := hex.DecodeString("ab" + "78563412" + "8877665544332211" + "0400" + "6172616d" + "0000")
	if err != nil {
		t.Fatal(err)
	}
	decoder := &guest.StateDecoder{Reader: bytes.NewReader(raw)}
	if got := decoder.U8(); got != 0xab {
		t.Fatalf("u8 = %#x", got)
	}
	if got := decoder.U32(); got != 0x12345678 {
		t.Fatalf("u32 = %#x", got)
	}
	if got := decoder.U64(); got != 0x1122334455667788 {
		t.Fatalf("u64 = %#x", got)
	}
	if got := decoder.String16(); got != "aram" {
		t.Fatalf("string16 = %q", got)
	}
	decoder.Reserved(2)
	if decoder.Err != nil {
		t.Fatalf("decoder error: %v", decoder.Err)
	}
	if decoder.Reader.Len() != 0 {
		t.Fatalf("unread bytes remain: %d", decoder.Reader.Len())
	}
}

func TestStateDecoderRejectsNonzeroReservedAndTruncation(t *testing.T) {
	decoder := &guest.StateDecoder{Reader: bytes.NewReader([]byte{0x01})}
	decoder.Reserved(1)
	if decoder.Err == nil {
		t.Fatal("reserved accepted nonzero byte")
	}
	truncated := &guest.StateDecoder{Reader: bytes.NewReader([]byte{0x01, 0x02})}
	if truncated.U32(); truncated.Err == nil {
		t.Fatal("u32 accepted truncated input")
	}
}
