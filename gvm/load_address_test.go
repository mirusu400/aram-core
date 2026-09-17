package gvm_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func loadAddressPush(code []byte, word uint16) []byte {
	return append(code, 6, byte(word>>8), byte(word))
}

func TestLoadAddressAllRawWords(t *testing.T) {
	// Four windows cover every raw value using legal 14-bit indices. Reuse one
	// VM per window/depth/bank, rather than allocating a VM for every loaded word.
	for _, file := range []bool{false, true} {
		for _, depth := range []int{1, 64, 65} {
			for window := 0; window < 4; window++ {
				t.Run(fmt.Sprintf("file%v/depth%d/window%d", file, depth, window), func(t *testing.T) {
					var code []byte
					prefix := make([]uint16, depth-1)
					for i := range prefix {
						prefix[i] = uint16(0x9100 + i)
						code = loadAddressPush(code, prefix[i])
					}
					start := len(code)
					payload := make([]byte, 32768)
					for index := 0; index < 16384; index++ {
						ref := uint16(index)
						if file {
							ref |= 0x4000
						}
						code = loadAddressPush(code, ref)
						code = append(code, 0x4e, 0x96)
						binary.LittleEndian.PutUint16(payload[index*2:], uint16(window*16384+index))
					}
					code = append(code, 0xff)
					fileStart := len(code)
					code = append(code, payload...)
					// No symbol metadata or services are necessary for a global scalar read.
					v := addressVM(t, code, gvm.AddressSpace{FileStart: uint32(fileStart), FileLength: 32768, RAM: payload})
					if err := v.Run(uint64(depth - 1)); err != gvm.ErrBudget {
						t.Fatal(err)
					}
					for index := 0; index < 16384; index++ {
						if err := v.Step(); err != nil {
							t.Fatal(err)
						}
						if err := v.Step(); err != nil {
							t.Fatalf("index%d: %v", index, err)
						}
						stack := v.Stack()
						want := uint16(window*16384 + index)
						if len(stack) != depth || stack[depth-1] != want || !reflect.DeepEqual(stack[:depth-1], prefix) || v.PC() != start+index*5+4 || v.Halted() {
							t.Fatalf("index%d got%x want%x pc%d", index, stack, want, v.PC())
						}
						if err := v.Step(); err != nil {
							t.Fatal(err)
						}
					}
					if err := v.Step(); err != nil || !v.Halted() {
						t.Fatalf("halt: %v", err)
					}
					if !bytes.Equal(v.Program(), code) {
						t.Fatal("load changed program")
					}
					for index := 0; index < 16384; index++ {
						got, err := v.ReadWord(uint16(index))
						if err != nil || got != uint16(window*16384+index) {
							t.Fatal("load changed RAM")
						}
					}
				})
			}
		}
	}
}

func TestLoadAddressRegionMatrix(t *testing.T) {
	for _, fileLen := range []int{0, 1, 2, 3, 4, 32767, 32768, 32769} {
		for _, ramLen := range []int{0, 1, 2, 3, 4, 32768, 32770} {
			for _, ref := range []uint16{0, 1, 2, 0x3fff, 0x4000, 0x4001, 0x7fff, 0x8000, 0xc000, 0xffff} {
				for _, services := range []bool{false, true} {
					t.Run(fmt.Sprintf("f%d/r%d/ref%x/services%v", fileLen, ramLen, ref, services), func(t *testing.T) {
						file := bytes.Repeat([]byte{0x12, 0x34}, (fileLen+1)/2)[:fileLen]
						ram := bytes.Repeat([]byte{0xcd, 0xfe}, (ramLen+1)/2)[:ramLen]
						code := loadAddressPush(nil, ref)
						code = append(code, 0x4e, 0xff)
						fileStart := len(code)
						code = append(code, file...)
						space := gvm.AddressSpace{FileStart: uint32(fileStart), FileLength: uint32(fileLen), RAM: ram, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Length: uint32(fileLen)}, {Region: gvm.AddressRAM, Length: uint32(ramLen)}}}
						var v *gvm.VM
						if services {
							var err error
							v, err = gvm.NewWithAddressSpaceAndServices(code, 0, space, nil)
							if err != nil {
								t.Fatal(err)
							}
						} else {
							v = addressVM(t, code, space)
						}
						size, want := ramLen, uint16(0xfecd)
						if ref&0x4000 != 0 {
							size, want = fileLen, 0x3412
						}
						valid := ref&0x8000 == 0 && 2*uint64(ref&0x3fff)+2 <= uint64(size)
						// Inspection errors must not make the later opcode failure nonsticky.
						inspected, inspectionErr := v.ReadWord(ref)
						if valid {
							if inspectionErr != nil || inspected != want {
								t.Fatal("inspection mismatch")
							}
						} else if !errors.Is(inspectionErr, gvm.ErrInvalidAddress) {
							t.Fatal("invalid inspection accepted")
						}
						if err := v.Step(); err != nil {
							t.Fatal(err)
						}
						if err := v.Run(0); err != gvm.ErrBudget || v.PC() != 3 {
							t.Fatal("zero budget advanced")
						}
						err := v.Step()
						if valid {
							if err != nil || v.PC() != 4 || !reflect.DeepEqual(v.Stack(), []uint16{want}) || v.Halted() {
								t.Fatalf("load:%v stack%x", err, v.Stack())
							}
							if err := v.Step(); err != nil || !v.Halted() {
								t.Fatalf("halt:%v", err)
							}
						} else {
							var fault *gvm.ExecutionError
							if !errors.Is(err, gvm.ErrInvalidAddress) || !errors.As(err, &fault) || fault.Offset != 3 || v.PC() != 4 || !reflect.DeepEqual(v.Stack(), []uint16{ref}) || v.Halted() {
								t.Fatalf("fault:%v stack%x", err, v.Stack())
							}
							if v.Step() != err || v.Run(0) != err || v.Run(99) != err || v.PC() != 4 {
								t.Fatal("nonsticky opcode address fault")
							}
						}
						gotFile, _ := v.Symbol(0)
						gotRAM, _ := v.Symbol(1)
						if !bytes.Equal(gotFile, file) || !bytes.Equal(gotRAM, ram) || !bytes.Equal(v.Program(), code) {
							t.Fatal("load changed memory")
						}
					})
				}
			}
		}
	}
}

func TestLoadAddressConfigurationAndUnderflow(t *testing.T) {
	for _, depth := range []int{0, 1, 64, 65} {
		var code []byte
		for i := 0; i < depth; i++ {
			code = loadAddressPush(code, 0x8000)
		}
		offset := len(code)
		code = append(code, 0x4e)
		at, err := gvm.NewAt(code, 0)
		if err != nil {
			t.Fatal(err)
		}
		programOffset := uint32(0)
		symbols, err := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{ProgramOffset: &programOffset, Length: uint32(len(code))}, {Length: 2, Initial: []byte{0, 0}}})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []*gvm.VM{gvm.New(code), at, symbols} {
			if err := v.Run(uint64(depth)); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			before := v.Stack()
			err := v.Step()
			var unsupported *gvm.UnsupportedOpcodeError
			if !errors.As(err, &unsupported) || unsupported.Opcode != 0x4e || unsupported.Offset != offset || v.PC() != offset+1 || !reflect.DeepEqual(v.Stack(), before) || v.Step() != err || v.Run(0) != err || v.Halted() || !bytes.Equal(v.Program(), code) {
				t.Fatalf("configuration-first:%v", err)
			}
		}
	}
	for _, services := range []bool{false, true} {
		var v *gvm.VM
		if services {
			var err error
			v, err = gvm.NewWithAddressSpaceAndServices([]byte{0x4e}, 0, gvm.AddressSpace{}, nil)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			v = addressVM(t, []byte{0x4e}, gvm.AddressSpace{})
		}
		err := v.Step()
		var fault *gvm.ExecutionError
		if !errors.Is(err, gvm.ErrStackUnderflow) || !errors.As(err, &fault) || fault.Offset != 0 || v.PC() != 1 || len(v.Stack()) != 0 || v.Halted() || v.Step() != err || v.Run(0) != err {
			t.Fatalf("underflow:%v", err)
		}
	}
}

func TestLoadAddressTerminalByte(t *testing.T) {
	for _, file := range []bool{false, true} {
		code := []byte{0xff, 0xff}
		ref := uint16(0)
		if file {
			ref = 0x4000
		}
		code = loadAddressPush(code, ref)
		code = append(code, 0x4e)
		v, err := gvm.NewWithAddressSpace(code, 2, gvm.AddressSpace{FileLength: 2, RAM: []byte{0, 0x80}})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Run(1); err != gvm.ErrBudget {
			t.Fatal(err)
		}
		if err := v.Run(0); err != gvm.ErrBudget || v.PC() != 5 {
			t.Fatal("budget advanced")
		}
		want := uint16(0x8000)
		if file {
			want = 0xffff
		}
		if err := v.Step(); err != nil || v.PC() != len(code) || !reflect.DeepEqual(v.Stack(), []uint16{want}) || v.Halted() {
			t.Fatalf("terminal load:%v", err)
		}
		err = v.Step()
		var fault *gvm.ExecutionError
		if !errors.Is(err, gvm.ErrTruncated) || !errors.As(err, &fault) || fault.Offset != len(code) || v.PC() != len(code) || !reflect.DeepEqual(v.Stack(), []uint16{want}) || v.Step() != err {
			t.Fatalf("next fetch:%v", err)
		}
	}
}

func TestLoadAddressCrossDescriptorsAndOwnership(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		ref := uint16(0)
		if region == gvm.AddressFile {
			ref = 0x4000
		}
		code := loadAddressPush(nil, ref)
		code = append(code, 0x4e, 0xff, 0, 0x80, 0xa7)
		original := append([]byte(nil), code...)
		ram := []byte{0, 0x80, 0xa7}
		space := gvm.AddressSpace{FileStart: 5, FileLength: 3, RAM: ram, Symbols: []gvm.AddressSymbol{
			{Region: region, Length: 1}, {Region: region, Offset: 1, Length: 1}, {Region: region, Offset: 3, Length: 0}, {Region: region, Length: 3},
		}}
		v := addressVM(t, code, space)
		code[5] = 0xff
		ram[1] = 0
		space.Symbols[0].Offset = 2
		before, _ := v.Symbol(3)
		before[0] = 0xff
		if err := v.Run(2); err != gvm.ErrBudget || !reflect.DeepEqual(v.Stack(), []uint16{0x8000}) {
			t.Fatalf("cross-descriptor raw load:%v", err)
		}
		stack := v.Stack()
		stack[0] = 0
		if !reflect.DeepEqual(v.Stack(), []uint16{0x8000}) {
			t.Fatal("stack inspection aliases VM")
		}
		got, _ := v.Symbol(3)
		if !bytes.Equal(got, []byte{0, 0x80, 0xa7}) || !bytes.Equal(v.Program(), original) {
			t.Fatal("caller/snapshot alias or load store")
		}
	}
}

func TestLoadAddressCodeAndNestedReturns(t *testing.T) {
	for _, file := range []bool{false, true} {
		// Nested call saved PCs9 and5 must survive. The loaded source word includes
		// the load instruction itself, and its raw4e06 value must not be re-resolved.
		ref := uint16(6)
		if file {
			ref |= 0x4000
		}
		code := []byte{0xf0, 0xf1, 0x44, 0, 6, 0xff, 0x44, 0, 10, 0x45}
		code = loadAddressPush(code, ref)
		code = append(code, 0x4e, 0x45)
		v, err := gvm.NewWithAddressSpace(code, 2, gvm.AddressSpace{FileLength: uint32(len(code)), RAM: code})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Run(3); err != gvm.ErrBudget || v.PC() != 13 {
			t.Fatal(err)
		}
		if err := v.Step(); err != nil || v.PC() != 14 || !reflect.DeepEqual(v.Stack(), []uint16{0x4e06}) {
			t.Fatalf("code scalar:%v stack%x", err, v.Stack())
		}
		if err := v.Run(3); err != nil || !v.Halted() || v.PC() != 6 || !reflect.DeepEqual(v.Stack(), []uint16{0x4e06}) || !bytes.Equal(v.Program(), code) {
			t.Fatalf("nested returns:%v pc%d", err, v.PC())
		}
	}
}

func TestLoadAddressStickyPrefix(t *testing.T) {
	for _, depth := range []int{1, 64, 65} {
		var code []byte
		for i := 0; i < depth-1; i++ {
			code = loadAddressPush(code, uint16(0x9200+i))
		}
		code = loadAddressPush(code, 0xc000)
		offset := len(code)
		code = append(code, 0x4e)
		v := addressVM(t, code, gvm.AddressSpace{FileLength: uint32(len(code))})
		if err := v.Run(uint64(depth)); err != gvm.ErrBudget {
			t.Fatal(err)
		}
		before := v.Stack()
		err := v.Step()
		if !errors.Is(err, gvm.ErrInvalidAddress) || v.PC() != offset+1 || v.Halted() || !reflect.DeepEqual(v.Stack(), before) {
			t.Fatalf("depth%d invalid address: %v", depth, err)
		}
		if v.Step() != err || v.Run(0) != err || v.Run(100) != err || !reflect.DeepEqual(v.Stack(), before) || !bytes.Equal(v.Program(), code) {
			t.Fatal("sticky fault changed full stack or program")
		}
	}
}
