package gnex

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestGNEX32MediaMemorySeparatesWritableResources(t *testing.T) {
	initial := []byte{1, 2, 3}
	image := GNEX32Image{Media: []GNEX32Media{
		{Flags: 1, Data: initial},
		{Flags: 0, Data: []byte("text\x00")},
		{Flags: 0x100, Data: []byte{0x0a, 0x01}},
		{Flags: 0x101, Data: nil},
		{Flags: 0x200, Data: []byte("MMMD")},
	}}
	state, err := image.NewMediaMemory()
	if err != nil {
		t.Fatal(err)
	}
	view, flags, err := state.Read(0)
	if err != nil || flags != 1 || !bytes.Equal(view, initial) {
		t.Fatalf("mutable read = %x flags=%#x error=%v", view, flags, err)
	}
	view[0] = 9
	if got, _, _ := state.Read(0); got[0] != 1 {
		t.Fatal("media read exposed writable backing storage")
	}
	if err := state.Replace(0, []byte{4, 5}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := state.Read(0); !bytes.Equal(got, []byte{4, 5}) || !bytes.Equal(initial, []byte{1, 2, 3}) {
		t.Fatalf("replacement changed image or missed runtime storage: got=%x image=%x", got, initial)
	}
	if err := state.Replace(3, []byte{6}); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{1, 2, 4} {
		if err := state.Replace(index, []byte{7}); !errors.Is(err, ErrGNEX32ReadOnlyMedia) {
			t.Fatalf("read-only media %d write = %v", index, err)
		}
	}
	second, err := image.NewMediaMemory()
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := second.Read(0); !bytes.Equal(got, initial) {
		t.Fatalf("new state inherited old mutation: %x", got)
	}
}

func TestGNEX32MediaMemoryRejectsBadIndexAndFlags(t *testing.T) {
	image := GNEX32Image{Media: []GNEX32Media{{Flags: 1}}}
	state, err := image.NewMediaMemory()
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{-1, 1} {
		if _, _, err := state.Read(index); err == nil {
			t.Fatalf("Read(%d) accepted", index)
		}
		if err := state.Replace(index, []byte{1}); err == nil {
			t.Fatalf("Replace(%d) accepted", index)
		}
	}
	image.Media[0].Flags = 2
	if _, err := image.NewMediaMemory(); err == nil {
		t.Fatal("accepted unknown media flags")
	}
}

func TestGNEX32MediaMemoryByteAccessAndResize(t *testing.T) {
	image := GNEX32Image{Media: []GNEX32Media{
		{Flags: 1},
		{Flags: 0, Data: []byte{7, 8}},
	}}
	state, err := image.NewMediaMemory()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Resize(0, 4); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMediaByte(0, 3, 65); err != nil {
		t.Fatal(err)
	}
	if got, err := state.MediaByte(0, 3); err != nil || got != 65 {
		t.Fatalf("byte 3 = %d, %v", got, err)
	}
	if got, _, _ := state.Read(0); !bytes.Equal(got, []byte{0, 0, 0, 65}) {
		t.Fatalf("resized data = %x", got)
	}
	if err := state.Resize(0, 2); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := state.Read(0); !bytes.Equal(got, []byte{0, 0}) {
		t.Fatalf("shrunk data = %x", got)
	}
	if got, err := state.MediaByte(1, 1); err != nil || got != 8 {
		t.Fatalf("read-only byte = %d, %v", got, err)
	}
	if err := state.SetMediaByte(1, 0, 9); !errors.Is(err, ErrGNEX32ReadOnlyMedia) {
		t.Fatalf("read-only byte write = %v", err)
	}
	if err := state.Resize(1, 4); !errors.Is(err, ErrGNEX32ReadOnlyMedia) {
		t.Fatalf("read-only resize = %v", err)
	}
	for _, index := range []int{-1, 2} {
		if _, err := state.MediaByte(index, 0); err == nil {
			t.Fatalf("out-of-range media %d read accepted", index)
		}
		if err := state.SetMediaByte(index, 0, 1); err == nil {
			t.Fatalf("out-of-range media %d write accepted", index)
		}
	}
	for _, offset := range []int{-1, 2} {
		if _, err := state.MediaByte(0, offset); !errors.Is(err, ErrGNEX32MediaByteOutOfBounds) {
			t.Fatalf("out-of-range byte %d read = %v", offset, err)
		}
		if err := state.SetMediaByte(0, offset, 1); !errors.Is(err, ErrGNEX32MediaByteOutOfBounds) {
			t.Fatalf("out-of-range byte %d write = %v", offset, err)
		}
	}
	if err := state.Resize(0, int(MaxMemberSize)+1); err == nil {
		t.Fatal("oversized media accepted")
	}
	if got, _, _ := state.Read(0); len(got) != 2 {
		t.Fatalf("failed resize changed data length to %d", len(got))
	}
}

func TestGNEX32MediaPaletteColorRGBOnConstantImage(t *testing.T) {
	original := []byte{0x0a, 1, 1, 0, 0, 0, 4,
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 0x40}
	image := GNEX32Image{Media: []GNEX32Media{
		{Flags: 0x100, Data: original},
		{Flags: 0, Data: []byte("not an image")},
	}}
	state, err := image.NewMediaMemory()
	if err != nil {
		t.Fatal(err)
	}
	if !state.SetPaletteColorRGB(0, 1, 25, 144, 38) {
		t.Fatal("constant image palette update failed")
	}
	modified, _, err := state.Read(0)
	if err != nil || !bytes.Equal(modified[10:13], []byte{25, 144, 38}) {
		t.Fatalf("palette entry = %x, %v", modified, err)
	}
	if !bytes.Equal(original[10:13], []byte{4, 5, 6}) {
		t.Fatal("palette update changed source image")
	}
	for _, input := range [][2]int{{0, -1}, {0, 4}, {1, 0}, {-1, 0}, {2, 0}} {
		if state.SetPaletteColorRGB(input[0], input[1], 0, 0, 0) {
			t.Fatalf("accepted invalid palette update %v", input)
		}
	}
	if err := state.Replace(0, []byte{1}); !errors.Is(err, ErrGNEX32ReadOnlyMedia) {
		t.Fatalf("constant image became generally writable: %v", err)
	}
	second, err := image.NewMediaMemory()
	if err != nil {
		t.Fatal(err)
	}
	untouched, _, err := second.Read(0)
	if err != nil || !bytes.Equal(untouched, original) {
		t.Fatalf("new runtime inherited palette: %x, %v", untouched, err)
	}
}

func TestGNEX32MediaPaletteColorRGBSupportsEveryPaletteImageKind(t *testing.T) {
	images := map[byte][]byte{
		0x02: {0x02, 1, 1, 0, 0, 3, 0x80, 0},
		0x05: {0x05, 1, 1, 0, 0, 2, 3, 0x80},
		0x06: {0x06, 1, 1, 0, 0, 0, 1, 2, 3, 0x40},
		0x07: {0x07, 1, 1, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 0x10},
		0x09: {0x09, 1, 1, 0, 0, 0, 2, 1, 2, 3, 4, 5, 6, 0x80},
		0x0a: {0x0a, 1, 1, 0, 0, 0, 2, 1, 2, 3, 4, 5, 6, 0x40},
		0x0b: {0x0b, 1, 1, 0, 0, 0, 2, 1, 2, 3, 4, 5, 6, 0x10},
	}
	for kind, data := range images {
		t.Run(fmt.Sprintf("kind_%02x", kind), func(t *testing.T) {
			memory, err := (GNEX32Image{Media: []GNEX32Media{{Flags: 0x100, Data: data}}}).NewMediaMemory()
			if err != nil {
				t.Fatal(err)
			}
			if !memory.SetPaletteColorRGB(0, 1, 25, 144, 38) {
				t.Fatal("valid palette update failed")
			}
			payload, overrides, err := memory.ReadImage(0)
			if err != nil {
				t.Fatal(err)
			}
			if kind >= 0x09 {
				if len(overrides) != 0 || !bytes.Equal(payload[10:13], []byte{25, 144, 38}) {
					t.Fatalf("RGB palette update = payload %x overrides %+v", payload, overrides)
				}
			} else {
				want := []GNEX32PaletteRGB{{Index: 1, Red: 25, Green: 144, Blue: 38}}
				if !reflect.DeepEqual(overrides, want) || !bytes.Equal(payload, data) {
					t.Fatalf("selector palette update = payload %x overrides %+v", payload, overrides)
				}
			}
		})
	}
}

func TestGNEX32MediaPaletteOverridesRoundTripAtomically(t *testing.T) {
	data := []byte{0x07, 1, 1, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 0x10}
	image := GNEX32Image{Media: []GNEX32Media{{Flags: 0x100, Data: data}}}
	memory, err := image.NewMediaMemory()
	if err != nil {
		t.Fatal(err)
	}
	memory.SetPaletteColorRGB(0, 3, 30, 31, 32)
	memory.SetPaletteColorRGB(0, 1, 10, 11, 12)
	saved := memory.Snapshot()
	if got := saved.Resources[0].PaletteOverrides; len(got) != 2 || got[0].Index != 1 || got[1].Index != 3 {
		t.Fatalf("snapshot overrides are not canonical: %+v", got)
	}

	restored, err := image.NewMediaMemory()
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Restore(saved); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.Snapshot(), saved) {
		t.Fatal("restored palette overrides differ")
	}
	before := restored.Snapshot()
	bad := restored.Snapshot()
	bad.Resources[0].PaletteOverrides = append(bad.Resources[0].PaletteOverrides,
		GNEX32PaletteRGB{Index: 16, Red: 1, Green: 2, Blue: 3})
	if err := restored.Restore(bad); err == nil || !reflect.DeepEqual(restored.Snapshot(), before) {
		t.Fatalf("invalid override restore = %v", err)
	}
}
