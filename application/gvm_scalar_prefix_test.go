package application_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/application"
	"github.com/mirusu400/aram-core/gvm"
	"github.com/mirusu400/aram-core/loader/gnex"
)

// Entirely authored normal v2 SGS storage, not executable guest code. Symbol 8
// has ordinary patterned test bytes, never a proposed reserved string value.
// Word-count descriptors cannot represent a one-byte span or overlapping
// nonempty symbols. Zero words exercises every representable short scalar span.
func scalarPrefixFixture(count int, storage string, short int) []byte {
	const ds = 80
	ps := ds + 4*count
	data := make([]byte, ps)
	data[0], data[2], data[5], data[10] = 2, 12, 1, 'S'
	for i := 0; i < count; i++ {
		o := ds + 4*i
		words := 3
		if i == short {
			words = 0
		}
		mutable := storage == "ram" || storage == "zero-ram" || (storage == "mixed" && i%2 == 1)
		initialized := storage != "zero-ram"
		if mutable {
			data[o] = 2
		}
		data[o+1] = byte(words)
		if initialized {
			data[o+2] = 1
		}
		if !mutable || initialized {
			for j := 0; j < 2*words; j++ {
				data = append(data, byte(0x40+i+j))
			}
		}
	}
	// Padding is outside SymbolFileEnd. Both media forms and trailing file
	// storage must survive preparation without being interpreted.
	data = append(data, 0xd1, 0xd2, 0xd3, 0xd4)
	dm := len(data)
	data = append(data, 0, 0x91, 4, 0, 1, 0x92, 4, 0)
	pm := len(data)
	data = append(data, 0x21, 0x22, 0x23, 0x24, 0x31, 0x32, 0x33, 0x34, 0xe1, 0xe2)
	for offset, value := range map[int]int{0x2c: ds, 0x2e: ps, 0x30: dm, 0x32: pm} {
		binary.LittleEndian.PutUint16(data[offset:], uint16(value))
	}
	return data
}

func TestPrepareGVMScalarPrefixStorageAndPublicBinding(t *testing.T) {
	for _, storage := range []string{"file", "ram", "mixed", "zero-ram"} {
		t.Run(storage, func(t *testing.T) {
			input := scalarPrefixFixture(18, storage, -1)
			original := bytes.Clone(input)
			before, err := gnex.DecodeExecutionImage(input)
			if err != nil {
				t.Fatalf("authored fixture: %v", err)
			}
			image, err := application.PrepareGVMScalarPrefix(input, 137, 256)
			if err != nil {
				t.Fatal(err)
			}
			wantBuffer, wantRAM := bytes.Clone(before.Buffer), bytes.Clone(before.SymbolRAM)
			space := gvm.AddressSpace{FileStart: uint32(image.SymbolFileStart), FileLength: uint32(image.SymbolFileEnd - image.SymbolFileStart), RAM: image.SymbolRAM}
			for i, symbol := range image.Symbols {
				want := bytes.Clone(before.Symbols[i].Data)
				switch i {
				case 0, 3, 4, 5, 6, 9, 10:
					want[0], want[1] = 0, 0
				case 1:
					want[0], want[1] = 137, 0
				case 2:
					want[0], want[1] = 0, 1
				case 15:
					want[0], want[1] = 9, 0x30
				}
				if !bytes.Equal(symbol.Data, want) {
					t.Fatalf("symbol %d = %x, want %x", i, symbol.Data, want)
				}
				binding := gvm.AddressSymbol{Region: gvm.AddressFile, Offset: uint32(symbol.BufferOffset - image.SymbolFileStart), Length: uint32(len(symbol.Data))}
				if symbol.Mutable {
					binding.Region, binding.Offset = gvm.AddressRAM, uint32(symbol.RAMOffset)
					copy(wantRAM[symbol.RAMOffset:], want)
					if &symbol.Data[0] != &image.SymbolRAM[symbol.RAMOffset] {
						t.Fatalf("symbol %d lost RAM alias", i)
					}
				} else {
					copy(wantBuffer[symbol.BufferOffset:], want)
					if &symbol.Data[0] != &image.Buffer[symbol.BufferOffset] {
						t.Fatalf("symbol %d lost file alias", i)
					}
				}
				old := before.Symbols[i]
				if symbol.Type != old.Type || symbol.Mutable != old.Mutable || symbol.BufferOffset != old.BufferOffset || symbol.RAMOffset != old.RAMOffset {
					t.Fatalf("symbol %d metadata changed", i)
				}
				space.Symbols = append(space.Symbols, binding)
			}
			if !bytes.Equal(image.Buffer, wantBuffer) || !bytes.Equal(image.SymbolRAM, wantRAM) {
				t.Fatal("storage changed outside first scalar words (including RAM initializers, padding or media)")
			}
			if image.Header != before.Header || image.Entry != before.Entry || image.RAMBytes != before.RAMBytes || image.SymbolFileStart != before.SymbolFileStart || image.SymbolFileEnd != before.SymbolFileEnd || !reflect.DeepEqual(image.Media, before.Media) {
				t.Fatal("decoder metadata or media changed")
			}
			if &image.Media[0].Data[0] != &image.Buffer[image.Media[0].BufferOffset] {
				t.Fatal("file media alias lost")
			}
			// Explicit binding/read only. This does not run instructions, deliver
			// services, or assert a complete runtime has been initialized.
			vm, err := gvm.NewWithAddressSpace(image.Buffer, uint32(image.Entry), space)
			if err != nil {
				t.Fatal(err)
			}
			for i, binding := range space.Symbols {
				address := uint16(binding.Offset / 2)
				if binding.Region == gvm.AddressFile {
					address |= 0x4000
				}
				for word := uint16(0); word < 3; word++ {
					want := binary.LittleEndian.Uint16(image.Symbols[i].Data[2*word:])
					if got, err := vm.ReadWord(address + word); err != nil || got != want {
						t.Fatalf("symbol %d word %d = %x, %v; want %x", i, word, got, err, want)
					}
				}
				got, err := vm.Symbol(uint8(i))
				if err != nil || !bytes.Equal(got, image.Symbols[i].Data) {
					t.Fatalf("bound symbol %d = %x, %v", i, got, err)
				}
			}
			for _, address := range []uint16{uint16(len(image.SymbolRAM) / 2), 0x4000 | uint16((image.SymbolFileEnd-image.SymbolFileStart)/2)} {
				if _, err := vm.ReadWord(address); !errors.Is(err, gvm.ErrInvalidAddress) {
					t.Fatalf("address %x exposed non-symbol storage: %v", address, err)
				}
			}
			if !bytes.Equal(input, original) {
				t.Fatal("preparation modified caller input")
			}
			second, err := application.PrepareGVMScalarPrefix(input, 137, 256)
			if err != nil || !reflect.DeepEqual(second, image) {
				t.Fatalf("repeat preparation differs: %v", err)
			}
			for i := range image.Buffer {
				image.Buffer[i] ^= 0xff
			}
			for i := range image.SymbolRAM {
				image.SymbolRAM[i] ^= 0xff
			}
			image.Media[1].Data[0] ^= 0xff
			if !bytes.Equal(input, original) || !bytes.Equal(second.Buffer, wantBuffer) || !bytes.Equal(second.SymbolRAM, wantRAM) || !reflect.DeepEqual(second.Media, before.Media) {
				t.Fatal("prepared images or caller input share owned storage")
			}
			for i := range second.Symbols {
				got, err := vm.Symbol(uint8(i))
				if err != nil || !bytes.Equal(got, second.Symbols[i].Data) {
					t.Fatalf("binding did not isolate symbol %d: %v", i, err)
				}
			}
			input[10] = 'X'
			if second.Buffer[10] != 'S' {
				t.Fatal("caller mutation leaked into prepared buffer")
			}
		})
	}
}

func scalarPrefixError(t *testing.T, data []byte, width, height int32, want error) {
	t.Helper()
	original := bytes.Clone(data)
	image, err := application.PrepareGVMScalarPrefix(data, width, height)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if !reflect.DeepEqual(image, gnex.ExecutionImage{}) || !bytes.Equal(data, original) {
		t.Fatal("failure returned partial image or mutated caller input")
	}
}

func TestPrepareGVMScalarPrefixDimensionsAndPrecedence(t *testing.T) {
	valid := scalarPrefixFixture(16, "mixed", -1)
	for _, width := range []int32{1, 2, 255, 256} {
		for _, height := range []int32{1, 2, 255, 256} {
			image, err := application.PrepareGVMScalarPrefix(valid, width, height)
			if err != nil {
				t.Fatal(err)
			}
			if binary.LittleEndian.Uint16(image.Symbols[1].Data) != uint16(width) || binary.LittleEndian.Uint16(image.Symbols[2].Data) != uint16(height) {
				t.Fatalf("dimensions %d x %d not preserved", width, height)
			}
		}
	}
	for _, invalid := range []int32{-1 << 31, -65535, -1, 0, 257, 65536, 1<<31 - 1} {
		for _, data := range [][]byte{valid, nil, scalarPrefixFixture(15, "file", -1), scalarPrefixFixture(16, "ram", 15)} {
			// Dimensions have precedence even over decoder and descriptor errors.
			scalarPrefixError(t, data, invalid, 1, application.ErrInvalidGVMScalarDimensions)
			scalarPrefixError(t, data, 1, invalid, application.ErrInvalidGVMScalarDimensions)
			scalarPrefixError(t, data, invalid, invalid, application.ErrInvalidGVMScalarDimensions)
		}
	}
}

func TestPrepareGVMScalarPrefixMinimumWords(t *testing.T) {
	for _, storage := range []string{"file", "ram", "zero-ram"} {
		data := scalarPrefixFixture(16, storage, -1)
		// Reduce every descriptor to one word. The remaining authored bytes
		// are valid padding before the media table, not additional symbols.
		for i := 0; i < 16; i++ {
			data[80+4*i+1] = 1
		}
		before, err := gnex.DecodeExecutionImage(data)
		if err != nil {
			t.Fatalf("%s minimum fixture: %v", storage, err)
		}
		image, err := application.PrepareGVMScalarPrefix(data, 1, 256)
		if err != nil {
			t.Fatalf("%s minimum spans: %v", storage, err)
		}
		for i, symbol := range image.Symbols {
			if len(symbol.Data) != 2 {
				t.Fatalf("%s symbol %d span = %d", storage, i, len(symbol.Data))
			}
			want := binary.LittleEndian.Uint16(before.Symbols[i].Data)
			switch i {
			case 0, 3, 4, 5, 6, 9, 10:
				want = 0
			case 1:
				want = 1
			case 2:
				want = 256
			case 15:
				want = 0x3009
			}
			if got := binary.LittleEndian.Uint16(symbol.Data); got != want {
				t.Fatalf("%s symbol %d = %x, want %x", storage, i, got, want)
			}
		}
	}
}

func TestPrepareGVMScalarPrefixShortSymbols(t *testing.T) {
	for _, storage := range []string{"file", "ram", "zero-ram"} {
		for _, slot := range []int{0, 9, 10, 15, 1, 2, 3, 4, 5, 6} {
			t.Run(fmt.Sprintf("%s/%d", storage, slot), func(t *testing.T) {
				data := scalarPrefixFixture(16, storage, slot)
				decoded, err := gnex.DecodeExecutionImage(data)
				if err != nil || len(decoded.Symbols[slot].Data) != 0 {
					t.Fatalf("invalid short-span fixture: %v", err)
				}
				scalarPrefixError(t, data, 1, 1, application.ErrInvalidGVMScalarSymbols)
			})
		}
	}
	for count := 0; count < 16; count++ {
		data := scalarPrefixFixture(count, "mixed", -1)
		if _, err := gnex.DecodeExecutionImage(data); err != nil {
			t.Fatalf("count %d fixture: %v", count, err)
		}
		scalarPrefixError(t, data, 1, 1, application.ErrInvalidGVMScalarSymbols)
	}
	for _, slot := range []int{7, 8, 11, 12, 13, 14} {
		if _, err := application.PrepareGVMScalarPrefix(scalarPrefixFixture(16, "mixed", slot), 1, 1); err != nil {
			t.Fatalf("non-scalar slot %d must not need storage: %v", slot, err)
		}
	}
}

func TestPrepareGVMScalarPrefixDecoderErrors(t *testing.T) {
	cases := map[string][]byte{"nil": nil, "truncated": {2, 0, 12}, "oversized": make([]byte, (128<<10)+1)}
	for _, name := range []string{"variant", "header", "ordering", "descriptor", "symbol-overread", "media-overread", "entry", "decoder-before-short"} {
		data := scalarPrefixFixture(16, "mixed", -1)
		switch name {
		case "variant":
			data[2] = 11
		case "header":
			data[10] = 0
		case "ordering":
			binary.LittleEndian.PutUint16(data[0x2c:], 0)
		case "descriptor":
			binary.LittleEndian.PutUint16(data[0x2e:], 145)
		case "symbol-overread":
			data[81] = 255
		case "media-overread":
			dm := int(binary.LittleEndian.Uint16(data[0x30:]))
			binary.LittleEndian.PutUint16(data[dm+2:], 65535)
		case "entry":
			binary.LittleEndian.PutUint16(data[0x1c:], 65535)
		case "decoder-before-short":
			data = scalarPrefixFixture(15, "ram", 0)
			dm := int(binary.LittleEndian.Uint16(data[0x30:]))
			binary.LittleEndian.PutUint16(data[dm+2:], 65535)
		}
		cases[name] = data
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, want := gnex.DecodeExecutionImage(data)
			if want == nil {
				t.Fatal("malformed fixture unexpectedly decoded")
			}
			original := bytes.Clone(data)
			image, got := application.PrepareGVMScalarPrefix(data, 1, 1)
			// Structured decoder errors must retain their exact type and fields,
			// not become a scalar-symbol error or a wrapping adapter error.
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("decoder error = %#v, want %#v", got, want)
			}
			if !reflect.DeepEqual(image, gnex.ExecutionImage{}) || !bytes.Equal(data, original) {
				t.Fatal("decoder failure leaked partial state or mutated input")
			}
		})
	}
}
