package system

import (
	"bytes"
	"errors"
	"io"
	"slices"
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
	check(t, err)
	check(t, flash.ProgramAt([]byte{0xf0, 0x0f, 0xaa, 0x55}, 0x0e))
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
	check(t, flash.EraseBlock(2))
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

func TestCOWFlashRepeatedProgramsReuseDirtyBlock(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0xff}, 0x20)}
	flash, err := NewCOWFlash(base, 0x10, "firmware-a")
	check(t, err)
	check(t, flash.ProgramAt([]byte{0xf0}, 0x01))
	dirtyBlock := flash.blocks[0]
	check(t, flash.ProgramAt([]byte{0x0f}, 0x02))
	if &flash.blocks[0][0] != &dirtyBlock[0] {
		t.Fatal("programming an already-dirty block replaced its backing storage")
	}
	assertStorageBytes(t, flash, 0x01, []byte{0xf0, 0x0f})
}

func TestCOWFlashProgramFailureIsAtomicAcrossBlocks(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0xff}, 0x20)}
	flash, err := NewCOWFlash(base, 0x10, "firmware-a")
	check(t, err)
	check(t, flash.ProgramAt([]byte{0x0f}, 0x0f))
	check(t, flash.ProgramAt([]byte{0x00}, 0x10))
	if err := flash.ProgramAt([]byte{0x00, 0xff}, 0x0f); !errors.Is(err, ErrFlashProgram) {
		t.Fatalf("cross-block program error = %v", err)
	}
	assertStorageBytes(t, flash, 0x0f, []byte{0x0f, 0x00})
}

func TestCOWFlashSparseCapacityTreatsUnrepresentedTailAsErasedAndWritable(t *testing.T) {
	baseBytes := bytes.Repeat([]byte{0xff}, 0x20)
	baseBytes[0x03] = 0x5a
	base := byteStorage{data: baseBytes}
	flash, err := NewCOWFlashWithCapacity(base, 0x40, 0x10, "sparse-firmware")
	check(t, err)
	if flash.Size() != 0x40 {
		t.Fatalf("sparse flash size = %#x", flash.Size())
	}
	assertStorageBytes(t, flash, 0x1e, []byte{0xff, 0xff, 0xff, 0xff})
	assertStorageBytes(t, flash, 0x30, bytes.Repeat([]byte{0xff}, 0x10))
	check(t, flash.ProgramAt([]byte{0xf0, 0x0f, 0xaa, 0x55}, 0x1e))
	assertStorageBytes(t, flash, 0x1e, []byte{0xf0, 0x0f, 0xaa, 0x55})
	if got := flash.DirtyBlocks(); !equalUint32s(got, []uint32{1, 2}) {
		t.Fatalf("sparse dirty blocks = %v", got)
	}
	check(t, flash.EraseBlock(3))
	check(t, flash.ProgramAt([]byte{0x7f}, 0x3f))
	assertStorageBytes(t, flash, 0x3f, []byte{0x7f})
	flash.FactoryReset()
	assertStorageBytes(t, flash, 0x03, []byte{0x5a})
	assertStorageBytes(t, flash, 0x1e, bytes.Repeat([]byte{0xff}, 4))
	assertStorageBytes(t, flash, 0x3f, []byte{0xff})
}

func TestErasedCOWFlashIsSparseWritableMedia(t *testing.T) {
	flash, err := NewErasedCOWFlash(0x40, 0x10, "erased-test")
	check(t, err)
	data := make([]byte, 4)
	if count, err := flash.ReadAt(data, 0x20); count != len(data) || err != nil ||
		!bytes.Equal(data, []byte{0xff, 0xff, 0xff, 0xff}) {
		t.Fatalf("erased read = %x, %d, %v", data, count, err)
	}
	check(t, flash.ProgramAt([]byte{0xf0, 0x0f}, 0x22))
	if count, err := flash.ReadAt(data, 0x20); count != len(data) || err != nil ||
		!bytes.Equal(data, []byte{0xff, 0xff, 0xf0, 0x0f}) {
		t.Fatalf("programmed read = %x, %d, %v", data, count, err)
	}
	if dirty := flash.DirtyBlocks(); !slices.Equal(dirty, []uint32{2}) {
		t.Fatalf("dirty blocks = %v", dirty)
	}
	if count, err := flash.base.ReadAt(nil, flash.Size()+1); count != 0 || err != nil {
		t.Fatalf("empty erased read = %d, %v", count, err)
	}
}

func TestCOWFlashFactorySeedsAreImmutableResetBaselineAndBindStateIdentity(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0xff}, 0x20)}
	seeds := []FlashSeed{
		{Offset: 0x22, Data: []byte{0xff, 0xfe, 0xaf, 0xbe}},
		{Offset: 0x03, Data: []byte{0x7f}},
	}
	flash, err := NewCOWFlashWithCapacityAndSeeds(base, 0x40, 0x10, "firmware-a", seeds)
	check(t, err)
	if len(flash.DirtyBlocks()) != 0 {
		t.Fatalf("factory seeds are guest-dirty blocks: %v", flash.DirtyBlocks())
	}
	assertStorageBytes(t, flash, 0x03, []byte{0x7f})
	assertStorageBytes(t, flash, 0x22, []byte{0xff, 0xfe, 0xaf, 0xbe})
	check(t, flash.ProgramAt([]byte{0x7e}, 0x03))
	check(t, flash.EraseBlock(2))
	assertStorageBytes(t, flash, 0x22, []byte{0xff, 0xff, 0xff, 0xff})
	flash.FactoryReset()
	assertStorageBytes(t, flash, 0x03, []byte{0x7f})
	assertStorageBytes(t, flash, 0x22, []byte{0xff, 0xfe, 0xaf, 0xbe})
	check(t, flash.ProgramAt([]byte{0x0f}, 0x30))

	reordered, err := NewCOWFlashWithCapacityAndSeeds(
		base, 0x40, 0x10, "firmware-a", []FlashSeed{seeds[1], seeds[0]},
	)
	check(t, err)
	if reordered.Identity() != flash.Identity() {
		t.Fatal("factory seed order changed flash identity")
	}
	different, err := NewCOWFlashWithCapacityAndSeeds(
		base, 0x40, 0x10, "firmware-a", []FlashSeed{{Offset: 0x22, Data: []byte{0xfe}}},
	)
	check(t, err)
	if different.Identity() == flash.Identity() {
		t.Fatal("different factory seeds share a flash identity")
	}
	state, err := flash.SaveState()
	check(t, err)
	restored, err := NewCOWFlashWithCapacityAndSeeds(base, 0x40, 0x10, "firmware-a", seeds)
	check(t, err)
	check(t, restored.LoadState(state))
	assertStorageBytes(t, restored, 0x22, []byte{0xff, 0xfe, 0xaf, 0xbe})
	assertStorageBytes(t, restored, 0x30, []byte{0x0f})
	if err := different.LoadState(state); !errors.Is(err, ErrInvalidFlashState) {
		t.Fatalf("different-seed state error = %v", err)
	}
}

func TestCOWFlashRejectsInvalidFactorySeeds(t *testing.T) {
	baseBytes := bytes.Repeat([]byte{0xff}, 0x20)
	baseBytes[3] = 0
	base := byteStorage{data: baseBytes}
	for _, seeds := range [][]FlashSeed{
		{{Offset: 0x40, Data: []byte{0}}},
		{{Offset: 0x3f, Data: []byte{0, 0}}},
		{{Offset: 1, Data: nil}},
		{{Offset: 2, Data: []byte{0, 0}}, {Offset: 3, Data: []byte{0}}},
		{{Offset: 3, Data: []byte{0xff}}},
	} {
		if _, err := NewCOWFlashWithCapacityAndSeeds(
			base, 0x40, 0x10, "firmware-a", seeds,
		); !errors.Is(err, ErrInvalidFlash) {
			t.Fatalf("invalid factory seeds %#v error = %v", seeds, err)
		}
	}
}

func TestCOWFlashStateIsDeterministicAndBoundToFirmware(t *testing.T) {
	base := byteStorage{data: bytes.Repeat([]byte{0xff}, 0x40)}
	flash, err := NewCOWFlash(base, 0x10, "firmware-a")
	check(t, err)
	check(t, flash.ProgramAt([]byte{0x7f}, 0x31))
	check(t, flash.ProgramAt([]byte{0x3f}, 0x01))
	state, err := flash.SaveState()
	check(t, err)
	again, err := flash.SaveState()
	if err != nil || !bytes.Equal(state, again) {
		t.Fatal("flash state is not deterministic")
	}
	restored, err := NewCOWFlash(base, 0x10, "firmware-a")
	check(t, err)
	check(t, restored.LoadState(state))
	if got := restored.DirtyBlocks(); !equalUint32s(got, []uint32{0, 3}) {
		t.Fatalf("restored dirty blocks = %v", got)
	}
	assertStorageBytes(t, restored, 0x01, []byte{0x3f})
	assertStorageBytes(t, restored, 0x31, []byte{0x7f})

	wrong, err := NewCOWFlash(base, 0x10, "firmware-b")
	check(t, err)
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
	check(t, err)
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
