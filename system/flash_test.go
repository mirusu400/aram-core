package system

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type byteStorage struct {
	data []byte
}

func (s byteStorage) Size() int64 {
	return int64(len(s.data))
}

func (s byteStorage) ReadAt(destination []byte, offset int64) (int, error) {
	return bytes.NewReader(s.data).ReadAt(destination, offset)
}

func TestCOWFlashProgramsErasesAndFactoryResets(t *testing.T) {
	baseBytes := bytes.Repeat([]byte{0xff}, 0x40)
	baseBytes[0x22] = 0x00
	base := byteStorage{data: baseBytes}
	flash, err := NewCOWFlash(base, 0x10, "firmware-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := flash.ProgramAt([]byte{0xf0, 0x0f, 0xaa, 0x55}, 0x0e); err != nil {
		t.Fatal(err)
	}
	assertStorageBytes(t, flash, 0x0e, []byte{0xf0, 0x0f, 0xaa, 0x55})
	if !bytes.Equal(base.data[0x0e:0x12], bytes.Repeat([]byte{0xff}, 4)) {
		t.Fatal("programming mutated the immutable base")
	}
	before := append([]byte(nil), readStorageBytes(t, flash, 0x0e, 4)...)
	if err := flash.ProgramAt([]byte{0xff}, 0x0e); !errors.Is(err, ErrFlashProgram) {
		t.Fatalf("1-to-0 program error = %v", err)
	}
	if got := readStorageBytes(t, flash, 0x0e, 4); !bytes.Equal(got, before) {
		t.Fatalf("failed program was not atomic: %x", got)
	}
	if err := flash.EraseBlock(2); err != nil {
		t.Fatal(err)
	}
	assertStorageBytes(t, flash, 0x20, bytes.Repeat([]byte{0xff}, 0x10))
	if got := flash.DirtyBlocks(); !equalUint32s(got, []uint32{0, 1, 2}) {
		t.Fatalf("dirty blocks = %v", got)
	}
	flash.FactoryReset()
	if len(flash.DirtyBlocks()) != 0 {
		t.Fatal("factory reset retained dirty blocks")
	}
	assertStorageBytes(t, flash, 0x22, []byte{0x00})
}

func TestCOWFlashStateIsDeterministicAndBoundToFirmware(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0xff}, 0x40)}
	flash, err := NewCOWFlash(base, 0x10, "firmware-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := flash.ProgramAt([]byte{0x7f}, 0x31); err != nil {
		t.Fatal(err)
	}
	if err := flash.ProgramAt([]byte{0x3f}, 0x01); err != nil {
		t.Fatal(err)
	}
	state, err := flash.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	again, err := flash.SaveState()
	if err != nil || !bytes.Equal(state, again) {
		t.Fatal("flash state is not deterministic")
	}
	restored, err := NewCOWFlash(base, 0x10, "firmware-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if got := restored.DirtyBlocks(); !equalUint32s(got, []uint32{0, 3}) {
		t.Fatalf("restored dirty blocks = %v", got)
	}
	assertStorageBytes(t, restored, 0x01, []byte{0x3f})
	assertStorageBytes(t, restored, 0x31, []byte{0x7f})

	wrong, err := NewCOWFlash(base, 0x10, "firmware-b")
	if err != nil {
		t.Fatal(err)
	}
	if err := wrong.LoadState(state); !errors.Is(err, ErrInvalidFlashState) {
		t.Fatalf("wrong-firmware state error = %v", err)
	}
	if err := restored.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidFlashState) {
		t.Fatalf("truncated state error = %v", err)
	}
	assertStorageBytes(t, restored, 0x31, []byte{0x7f})
}

func TestCOWFlashRejectsInvalidGeometryAndBounds(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0xff}, 0x40)}
	if _, err := NewCOWFlash(base, 24, "firmware-a"); !errors.Is(err, ErrInvalidFlash) {
		t.Fatalf("invalid geometry error = %v", err)
	}
	flash, err := NewCOWFlash(base, 0x10, "firmware-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := flash.ProgramAt([]byte{0}, 0x40); !errors.Is(err, ErrFlashBounds) {
		t.Fatalf("out-of-range program error = %v", err)
	}
	if err := flash.EraseBlock(4); !errors.Is(err, ErrFlashBounds) {
		t.Fatalf("out-of-range erase error = %v", err)
	}
	buffer := make([]byte, 2)
	count, err := flash.ReadAt(buffer, 0x3f)
	if count != 1 || !errors.Is(err, io.EOF) {
		t.Fatalf("partial read = count %d error %v", count, err)
	}
}

func readStorageBytes(t *testing.T, storage io.ReaderAt, offset int64, size int) []byte {
	t.Helper()
	result := make([]byte, size)
	if _, err := storage.ReadAt(result, offset); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertStorageBytes(t *testing.T, storage io.ReaderAt, offset int64, want []byte) {
	t.Helper()
	if got := readStorageBytes(t, storage, offset, len(want)); !bytes.Equal(got, want) {
		t.Fatalf("storage bytes at %#x = %x, want %x", offset, got, want)
	}
}

func equalUint32s(left, right []uint32) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
