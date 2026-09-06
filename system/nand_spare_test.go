package system

import (
	"bytes"
	"errors"
	"testing"
)

func newTestSparseNANDSpare(t *testing.T, identity string) *SparseNANDSpare {
	t.Helper()
	spare, err := NewSparseNANDSpare(SparseNANDSpareConfig{
		PageSize: 8, PageCount: 16, PagesPerEraseBlock: 4,
		Identity:    identity,
		InitialData: []FlashSeed{{Offset: 3*8 + 2, Data: []byte{0xa5, 0xa5}}},
	})
	check(t, err)
	return spare
}

func TestSparseNANDSparePreservesFactoryDataAndGuestState(t *testing.T) {
	spare := newTestSparseNANDSpare(t, "sparse-spare-test")
	page := make([]byte, spare.SparePageSize())
	check(t, spare.ReadSparePage(page, 3))
	if want := []byte{0xff, 0xff, 0xa5, 0xa5, 0xff, 0xff, 0xff, 0xff}; !bytes.Equal(page, want) {
		t.Fatalf("factory spare page = %x, want %x", page, want)
	}

	programmed := append([]byte(nil), page...)
	programmed[4] = 0x7f
	check(t, spare.ProgramSparePage(programmed, 3))
	state, err := spare.SaveState()
	check(t, err)
	restored := newTestSparseNANDSpare(t, "sparse-spare-test")
	check(t, restored.LoadState(state))
	check(t, restored.ReadSparePage(page, 3))
	if page[2] != 0xa5 || page[4] != 0x7f {
		t.Fatalf("restored spare page = %x", page)
	}

	check(t, restored.EraseSparePages(3, 1))
	check(t, restored.ReadSparePage(page, 3))
	if !bytes.Equal(page, bytes.Repeat([]byte{0xff}, len(page))) {
		t.Fatalf("erased spare page = %x", page)
	}
}

func TestSparseNANDSpareRejectsIncompatibleStateAndGeometry(t *testing.T) {
	if _, err := NewSparseNANDSpare(SparseNANDSpareConfig{}); !errors.Is(err, ErrInvalidNANDSpare) {
		t.Fatalf("empty spare geometry error = %v", err)
	}
	spare := newTestSparseNANDSpare(t, "sparse-spare-a")
	state, err := spare.SaveState()
	check(t, err)
	wrong := newTestSparseNANDSpare(t, "sparse-spare-b")
	if err := wrong.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("identity-mismatched spare state error = %v", err)
	}
	if err := spare.EraseSparePages(16, 1); !errors.Is(err, ErrFlashBounds) {
		t.Fatalf("out-of-range spare erase error = %v", err)
	}
}
