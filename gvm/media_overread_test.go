package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

// The native opcode81 handler adds the element to the record's data pointer
// without checking it against the record length. File records point into the
// loaded SGS image and writable records are packed in index order inside one
// RAM arena, so a read past the end lands in whatever the native runtime keeps
// next to the record.
func TestMediaByteReadContinuesIntoNativeNeighbour(t *testing.T) {
	file := []byte("..CDEFGH")
	media := []gvm.MediaResource{
		{Data: []byte("CD"), Backing: gvm.MediaBackingFile, FileOffset: 2},
		{Data: []byte("ab"), Backing: gvm.MediaBackingArena},
		{Data: []byte("EF"), Backing: gvm.MediaBackingFile, FileOffset: 4},
		{Data: []byte{}, Backing: gvm.MediaBackingArena},
		{Data: []byte{'c', 0xf0}, Backing: gvm.MediaBackingArena},
		{Data: []byte("zz")},
	}
	for _, tc := range []struct {
		name           string
		index, element int16
		want           uint16
		wantErr        error
	}{
		{"file record reads the next file bytes", 0, 2, 'E', nil},
		{"file record reaches the end of the image", 0, 5, 'H', nil},
		{"file record cannot leave the image", 0, 6, 0, gvm.ErrInvalidElement},
		{"arena record skips file records and empty records", 1, 2, 'c', nil},
		{"arena neighbour byte is sign extended", 1, 3, 0xfff0, nil},
		{"unused arena reads zero", 1, 4, 0, nil},
		{"unused arena is bounded", 1, 0x4000 + 4, 0, gvm.ErrInvalidElement},
		{"detached record keeps failing closed", 5, 2, 0, gvm.ErrInvalidElement},
		{"negative element keeps failing closed", 0, -1, 0, gvm.ErrInvalidElement},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := []byte{0x06, byte(uint16(tc.index) >> 8), byte(tc.index), 0x06, byte(uint16(tc.element) >> 8), byte(tc.element), 0x81}
			vm, err := gvm.NewWithAddressSpaceAndServices(program, 0, gvm.AddressSpace{},
				&gvm.ServiceConfig{Media: media, MediaFile: file})
			if err != nil {
				t.Fatal(err)
			}
			err = vm.Run(3)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Run = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if !errors.Is(err, gvm.ErrBudget) || len(vm.Stack()) != 1 || vm.Stack()[0] != tc.want {
				t.Fatalf("Run=%v stack=%v, want [%#x]", err, vm.Stack(), tc.want)
			}
		})
	}
}

// A resized arena record moves every later arena record, so the neighbour
// seen past its end follows the current lengths rather than the image layout.
func TestMediaByteReadFollowsResizedArena(t *testing.T) {
	media := []gvm.MediaResource{
		{Data: []byte("ab"), Backing: gvm.MediaBackingArena},
		{Data: []byte("cd"), Backing: gvm.MediaBackingArena},
	}
	// Grow record 0 to 4 zeroed bytes with 7b, then read record 0 element 4.
	program := []byte{0x05, 0, 0x05, 4, 0x7b, 0x05, 0, 0x05, 4, 0x81}
	vm, err := gvm.NewWithAddressSpaceAndServices(program, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{Media: media})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(6); !errors.Is(err, gvm.ErrBudget) || len(vm.Stack()) != 1 || vm.Stack()[0] != 'c' {
		t.Fatalf("Run=%v stack=%v, want ['c']", err, vm.Stack())
	}
}

func TestMediaFileBackingMustLieInsideTheImage(t *testing.T) {
	_, err := gvm.NewWithAddressSpaceAndServices([]byte{0xff}, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{
		Media:     []gvm.MediaResource{{Data: []byte("CD"), Backing: gvm.MediaBackingFile, FileOffset: 7}},
		MediaFile: []byte("..CDEFGH"),
	})
	if !errors.Is(err, gvm.ErrInvalidServiceConfig) {
		t.Fatalf("New = %v, want ErrInvalidServiceConfig", err)
	}
}
