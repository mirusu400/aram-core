package interpreter

import (
	"bytes"
	"fmt"
	"testing"
)

// Build the bytes from the expected word, independently of the load implementation.
// The private-loop helper is a raw little-endian read, including at odd offsets;
// architectural word rotation remains the ordinary memory executor's responsibility.
func TestPrivateLoopWordLoadAllBytesAndOffsets(t *testing.T) {
	values := []uint32{0, 1, 0x7fffffff, 0x80000000, 0xffffffff, 0x01234567, 0x89abcdef}
	for lane := uint32(0); lane < 4; lane++ {
		for value := uint32(0); value < 256; value++ {
			mask := uint32(0xff) << (lane * 8)
			values = append(values, 0x5aa53cc3&^mask|value<<(lane*8))
		}
	}
	for offset := 0; offset < 64; offset++ {
		for _, want := range values {
			backing := bytes.Repeat([]byte{0xd7}, offset+8)
			for lane := 0; lane < 4; lane++ {
				backing[offset+lane] = byte(want >> (uint32(lane) * 8))
			}
			before := append([]byte(nil), backing...)
			// Exactly four available bytes after offset, with spare capacity beyond
			// the logical end: the load must still honor the caller's length proof.
			if got := loadLittle32(backing[:offset+4], offset); got != want {
				t.Fatalf("offset=%d got=%08x want=%08x", offset, got, want)
			}
			if !bytes.Equal(backing, before) {
				t.Fatalf("word read modified memory at offset=%d", offset)
			}
		}
	}
}

func TestPrivateLoopWordLoadRejectsIncompleteSpan(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, length := range []int{0, 1, 2, 3, 4, 7, 8} {
		for _, offset := range []int{-4, -1, length - 3, length - 1, length, maxInt - 2, maxInt} {
			if offset >= 0 && offset <= length-4 {
				continue
			}
			t.Run(fmt.Sprintf("len=%d/offset=%d", length, offset), func(t *testing.T) {
				backing := bytes.Repeat([]byte{0x71}, length+8)
				before := append([]byte(nil), backing...)
				panicked := false
				func() {
					defer func() { panicked = recover() != nil }()
					_ = loadLittle32(backing[:length], offset)
				}()
				if !panicked || !bytes.Equal(backing, before) {
					t.Fatal("incomplete word span did not fail without changing memory")
				}
			})
		}
	}
}
