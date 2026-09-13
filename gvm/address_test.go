package gvm_test

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func addressVM(t *testing.T, program []byte, space gvm.AddressSpace) *gvm.VM {
	t.Helper()
	v, err := gvm.NewWithAddressSpace(program, 0, space)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAddressSpaceEncodingAndResolution(t *testing.T) {
	for _, tc := range []struct {
		name     string
		region   gvm.AddressRegion
		offset   uint32
		want     uint16
		readable bool
		value    uint16
	}{
		{"file_base", gvm.AddressFile, 0, 0x4000, true, 0x2211},
		{"file_odd", gvm.AddressFile, 3, 0x4001, true, 0x4433},
		{"RAM_offset", gvm.AddressRAM, 2, 1, true, 0x8877},
		{"RAM_odd", gvm.AddressRAM, 3, 1, true, 0x8877},
		{"RAM_tag_collision", gvm.AddressRAM, 0x8000, 0x4000, true, 0x2211},
		{"file_tag_collision", gvm.AddressFile, 0x8000, 0x4000, true, 0x2211},
		{"RAM_negative", gvm.AddressRAM, 0x10000, 0x8000, false, 0},
		{"file_negative", gvm.AddressFile, 0x10000, 0xc000, false, 0},
		{"RAM_truncation", gvm.AddressRAM, 0x20000, 0, true, 0x6655},
		{"file_truncation", gvm.AddressFile, 0x20000, 0x4000, true, 0x2211},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := make([]byte, 0x20010)
			copy(p, []byte{0x4d, 0, 0xff})
			copy(p[8:], []byte{0x11, 0x22, 0x33, 0x44})
			ram := make([]byte, 0x20008)
			copy(ram, []byte{0x55, 0x66, 0x77, 0x88})
			v := addressVM(t, p, gvm.AddressSpace{FileStart: 8, FileLength: 0x20008, RAM: ram, Symbols: []gvm.AddressSymbol{{Region: tc.region, Offset: tc.offset}}})
			if err := v.Run(2); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(v.Stack(), []uint16{tc.want}) {
				t.Fatalf("stack %x want %x", v.Stack(), tc.want)
			}
			got, err := v.ReadWord(tc.want)
			if tc.readable {
				if err != nil || got != tc.value {
					t.Fatalf("read %x %v want %x", got, err, tc.value)
				}
			} else if err == nil {
				t.Fatal("negative address accepted")
			}
		})
	}
}

func TestAddressSpaceValidationAndEmptyMembership(t *testing.T) {
	for _, space := range []gvm.AddressSpace{
		{FileStart: 5}, {FileStart: 1, FileLength: ^uint32(0)},
		{Symbols: []gvm.AddressSymbol{{Region: 0}}}, {Symbols: []gvm.AddressSymbol{{Region: 99}}},
		{FileLength: 4, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Offset: 3, Length: 2}}},
		{RAM: []byte{1}, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Offset: ^uint32(0), Length: 2}}},
		{FileLength: 4, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Offset: 5}}},
	} {
		if _, err := gvm.NewWithAddressSpace([]byte{0x4d, 0, 0xff, 0}, 0, space); !errors.Is(err, gvm.ErrInvalidSymbolRegion) {
			t.Fatalf("invalid %+v: %v", space, err)
		}
	}
	if _, err := gvm.NewWithAddressSpace([]byte{0xff}, 1, gvm.AddressSpace{}); !errors.Is(err, gvm.ErrInvalidTarget) {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		size, offset uint32
		ok           bool
	}{
		{"zero_empty", 0, 0, false}, {"zero_at_end", 4, 4, false}, {"zero_inside", 4, 3, true}, {"odd_last", 3, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := addressVM(t, []byte{0x4d, 0, 0xff, 0}, gvm.AddressSpace{FileLength: tc.size, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Offset: tc.offset}}})
			err := v.Step()
			if (err == nil) != tc.ok {
				t.Fatalf("membership %v", err)
			}
			if !tc.ok && !errors.Is(err, gvm.ErrInvalidSymbolRegion) {
				t.Fatal(err)
			}
		})
	}
	for _, n := range []int{0, 1, 2, 3, 4, 5} {
		v := addressVM(t, []byte{0xff, 0, 1, 2, 3, 4, 5}, gvm.AddressSpace{FileStart: 2, FileLength: uint32(n), RAM: make([]byte, n)})
		for _, tag := range []uint16{0, 0x4000} {
			for i := uint16(0); i < 4; i++ {
				_, err := v.ReadWord(tag | i)
				if (err == nil) != (int(i) < n/2) {
					t.Fatalf("length %d address %x err %v", n, tag|i, err)
				}
			}
		}
		for _, a := range []uint16{0x8000, 0xc000, 0xffff} {
			if _, err := v.ReadWord(a); err == nil {
				t.Fatalf("negative %x", a)
			}
		}
	}
}

func TestAddressSpaceAliasesOwnershipAndDirtyCode(t *testing.T) {
	p := []byte{0x31, 0, 1, 0x7f, 0x36, 1, 0xfe, 0x4d, 0, 0x4d, 1, 0xff, 0, 0, 0, 0}
	ram := []byte{1, 2, 3, 4, 5, 6}
	syms := []gvm.AddressSymbol{{Region: gvm.AddressRAM, Offset: 2, Length: 4}, {Region: gvm.AddressRAM, Offset: 4, Length: 2}, {Region: gvm.AddressFile, Offset: 0, Length: 4}}
	v := addressVM(t, p, gvm.AddressSpace{FileStart: 12, FileLength: 4, RAM: ram, Symbols: syms})
	p[0] = 0xff
	ram[2] = 99
	syms[0].Offset = 0
	if err := v.Run(5); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Stack(), []uint16{1, 2}) {
		t.Fatal(v.Stack())
	}
	if got, err := v.ReadWord(2); err != nil || got != 0xfffe {
		t.Fatalf("alias read %x %v", got, err)
	}
	a, _ := v.Symbol(0)
	if !reflect.DeepEqual(a, []byte{3, 4, 0xfe, 0xff}) {
		t.Fatal(a)
	}
	a[0] = 99
	s := v.Program()
	s[12] = 99
	if got, _ := v.ReadWord(1); got != 0x0403 {
		t.Fatal("snapshot mutated RAM")
	}
	if got, _ := v.ReadWord(0x4000); got != 0 {
		t.Fatal("snapshot mutated code")
	}
	if ram[4] != 5 || p[14] != 0 {
		t.Fatal("caller memory modified")
	}
	// A file view rewrites the next instruction through the same owned code allocation.
	dirty := addressVM(t, []byte{0x36, 0, 0xff, 0, 0}, gvm.AddressSpace{FileStart: 3, FileLength: 2, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Length: 2}}})
	if err := dirty.Run(2); err != nil || !dirty.Halted() {
		t.Fatalf("dirty code %v", err)
	}
	if word, err := dirty.ReadWord(0x4000); err != nil || word != 0xffff {
		t.Fatalf("dirty read %x %v", word, err)
	}
}

func TestAddressSpaceFaultBudgetAndLegacy(t *testing.T) {
	for _, p := range [][]byte{{0x4d}, {0x4d, 9}, {0x4d, 0}} {
		v := addressVM(t, p, gvm.AddressSpace{Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM}}})
		if err := v.Run(0); !errors.Is(err, gvm.ErrBudget) || v.PC() != 0 {
			t.Fatal("budget", err)
		}
		err := v.Step()
		if err == nil {
			t.Fatal("expected fault")
		}
		pc := v.PC()
		if v.Step() != err || v.Run(0) != err || v.PC() != pc || len(v.Stack()) != 0 {
			t.Fatal("nonsticky fault")
		}
		if pc != 1 {
			t.Fatal("operand committed on fault")
		}
	}
	p := make([]byte, 0, 134)
	for i := 0; i < 65; i++ {
		p = append(p, 0x4d, 0)
	}
	p = append(p, 0x4d, 255)
	v := addressVM(t, p, gvm.AddressSpace{RAM: []byte{0}, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM}}})
	if err := v.Run(65); !errors.Is(err, gvm.ErrBudget) || len(v.Stack()) != 65 {
		t.Fatal("capacity", err, len(v.Stack()))
	}
	if err := v.Step(); !errors.Is(err, gvm.ErrStackOverflow) {
		t.Fatalf("guard order %v", err)
	}
	old, _ := gvm.NewWithSymbols([]byte{0x4d, 0}, 0, []gvm.SymbolRegion{{Length: 2, Initial: []byte{1, 2}}})
	at, _ := gvm.NewAt([]byte{0x4d, 0}, 0)
	for _, v := range []*gvm.VM{gvm.New([]byte{0x4d, 0}), at, old} {
		var unsupported *gvm.UnsupportedOpcodeError
		if !errors.As(v.Step(), &unsupported) || unsupported.Opcode != 0x4d {
			t.Fatal("legacy opcode enabled")
		}
		if _, err := v.ReadWord(0); err == nil {
			t.Fatal("legacy resolver enabled")
		}
	}
	// ReadWord is observational, including on an already faulted VM.
	if word, err := v.ReadWord(0); err == nil || word != 0 {
		t.Fatal("one-byte region resolved")
	}
}

func FuzzAddressSpace(f *testing.F) {
	f.Add([]byte{0x4d, 0, 0xff}, []byte{1, 2}, uint32(0), uint32(2), uint32(0), uint32(2), byte(2), uint16(0))
	f.Fuzz(func(t *testing.T, p, ram []byte, start, length, off, size uint32, region byte, address uint16) {
		if len(p) > 4096 || len(ram) > 4096 {
			return
		}
		v, err := gvm.NewWithAddressSpace(p, 0, gvm.AddressSpace{FileStart: start, FileLength: length, RAM: ram, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRegion(region), Offset: off, Length: size}}})
		if err != nil {
			return
		}
		_ = v.Run(70)
		pc, stack := v.PC(), v.Stack()
		got, e := v.ReadWord(address)
		var bytes []byte
		idx := address
		if idx&0x4000 != 0 {
			idx &^= 0x4000
			current := v.Program()
			bytes = current[int(start):int(uint64(start)+uint64(length))]
		} else {
			bytes = ram /* mutated RAM is checked by bounds only below */
		}
		valid := int16(idx) >= 0 && uint64(idx) < uint64(len(bytes))/2
		if (e == nil) != valid {
			t.Fatalf("resolver validity %x %v", address, e)
		}
		if valid && address&0x4000 != 0 && got != binary.LittleEndian.Uint16(bytes[2*int(idx):]) {
			t.Fatal("resolver value")
		}
		if v.PC() != pc || !reflect.DeepEqual(v.Stack(), stack) {
			t.Fatal("resolver mutated execution")
		}
	})
}
