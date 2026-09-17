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

func TestAddSymbolArithmetic(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, depth := range []int{0, 65} {
			for _, index := range []int{0, 127, 128, 255} {
				for _, size := range []int{2, 3, 511, 512} {
					for _, old := range []uint16{0, 1, 127, 128, 0x7fff, 0x8000, 0xff80, 0xffff} {
						for _, delta := range []byte{0, 1, 127, 128, 129, 255} {
							t.Run(fmt.Sprintf("configured%v/d%d/i%d/n%d/%x/%x", configured, depth, index, size, old, delta), func(t *testing.T) {
								code := []byte{}
								for i := 0; i < depth; i++ {
									code = append(code, 5, byte(i))
								}
								op := len(code)
								code = append(code, 0x3a, byte(index), delta, 0xff)
								initial := bytes.Repeat([]byte{0xa5}, size)
								binary.LittleEndian.PutUint16(initial, old)
								bindings := make([]gvm.SymbolRegion, index+1)
								bindings[index] = gvm.SymbolRegion{Length: uint32(size), Initial: initial}
								var v *gvm.VM
								var err error
								if configured {
									shared := make([]gvm.AddressSymbol, index+1)
									for i := range shared {
										shared[i] = gvm.AddressSymbol{Region: gvm.AddressRAM}
									}
									shared[index].Length = uint32(size)
									v, err = gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{RAM: initial, Symbols: shared})
								} else {
									v, err = gvm.NewWithSymbols(code, 0, bindings)
								}
								if err != nil {
									t.Fatal(err)
								}
								if v.Run(uint64(depth)) != gvm.ErrBudget {
									t.Fatal("setup")
								}
								stack := v.Stack()
								if v.Run(0) != gvm.ErrBudget || v.PC() != op {
									t.Fatal("zero budget")
								}
								if err = v.Run(1); err != gvm.ErrBudget {
									t.Fatal(err)
								}
								want := append([]byte(nil), initial...)
								binary.LittleEndian.PutUint16(want, uint16(int32(old)+int32(int8(delta))))
								got, err := v.Symbol(uint8(index))
								if err != nil {
									t.Fatal(err)
								}
								if !bytes.Equal(got, want) || v.PC() != op+3 || v.Halted() || !reflect.DeepEqual(stack, v.Stack()) || !bytes.Equal(code, v.Program()) {
									t.Fatalf("state %x pc%d", got, v.PC())
								}
								if binary.LittleEndian.Uint16(initial) != old {
									t.Fatal("caller RAM mutated")
								}
								got[0] ^= 255
								again, _ := v.Symbol(uint8(index))
								if !bytes.Equal(again, want) {
									t.Fatal("snapshot escaped")
								}
								if v.Run(1) != nil || !v.Halted() || v.PC() != op+4 || v.Step() != nil || v.Run(0) != nil || !reflect.DeepEqual(stack, v.Stack()) {
									t.Fatal("halt/resume")
								}
							})
						}
					}
				}
			}
		}
	}
}

func TestAddSymbolGlobalViews(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressRAM, gvm.AddressFile} {
		for _, offset := range []uint32{0, 1, 2, 3, 4} {
			for _, length := range []uint32{0, 1, 2, 3, 4} {
				if offset+length > 4 {
					continue
				}
				t.Run(fmt.Sprintf("r%d/o%d/n%d", region, offset, length), func(t *testing.T) {
					code := []byte{0xcc, 0xff, 0xff, 0x12, 0x34, 0xee, 0x3a, 0, 1, 0xff}
					ram := []byte{0xff, 0xff, 0x12, 0x34}
					space := gvm.AddressSpace{FileStart: 1, FileLength: 4, RAM: ram, Symbols: []gvm.AddressSymbol{{Region: region, Offset: offset, Length: length}, {Region: region, Length: 4}, {Region: region, Offset: 1, Length: 3}}}
					v, err := gvm.NewWithAddressSpace(code, 6, space)
					if err != nil {
						t.Fatal(err)
					}
					before, _ := v.Symbol(1)
					err = v.Step()
					if offset+2 > 4 {
						var fault *gvm.ExecutionError
						if !errors.Is(err, gvm.ErrInvalidSymbolRegion) || !errors.As(err, &fault) || fault.Offset != 6 || v.PC() != 7 || v.Step() != err || v.Run(0) != err || v.Run(9) != err {
							t.Fatalf("fault %v", err)
						}
					} else {
						if err != nil || v.PC() != 9 {
							t.Fatalf("success %v pc%d", err, v.PC())
						}
						old := binary.LittleEndian.Uint16(before[offset:])
						binary.LittleEndian.PutUint16(before[offset:], old+1)
					}
					got, _ := v.Symbol(1)
					alias, _ := v.Symbol(2)
					if !bytes.Equal(got, before) || !bytes.Equal(alias, before[1:]) || len(v.Stack()) != 0 || v.Halted() {
						t.Fatalf("memory %x want%x", got, before)
					}
					wantCode := append([]byte(nil), code...)
					if region == gvm.AddressFile {
						copy(wantCode[1:5], before)
					}
					if !bytes.Equal(v.Program(), wantCode) || !bytes.Equal(ram, []byte{255, 255, 0x12, 0x34}) || code[1] != 255 {
						t.Fatal("region bounds/caller isolation")
					}
					for i := 0; i < 2; i++ {
						address := uint16(i)
						if region == gvm.AddressFile {
							address |= 0x4000
						}
						word, e := v.ReadWord(address)
						if e != nil || word != binary.LittleEndian.Uint16(before[2*i:]) {
							t.Fatal("global snapshot")
						}
					}
				})
			}
		}
	}
	// No descriptor shape restriction in configured mode either.
	for _, region := range []gvm.AddressRegion{gvm.AddressRAM, gvm.AddressFile} {
		for _, length := range []uint32{511, 512} {
			data := bytes.Repeat([]byte{255}, 513)
			code := append(append([]byte(nil), data...), 0x3a, 0, 1)
			v, err := gvm.NewWithAddressSpace(code, 513, gvm.AddressSpace{FileLength: 513, RAM: data, Symbols: []gvm.AddressSymbol{{Region: region, Offset: 1, Length: length}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = v.Step(); err != nil {
				t.Fatal(err)
			}
			got, _ := v.Symbol(0)
			if got[0] != 0 || got[1] != 0 || !bytes.Equal(got[2:], data[3:1+length]) {
				t.Fatal("oversized view")
			}
		}
	}
}

func TestAddSymbolFaultOrder(t *testing.T) {
	for _, depth := range []int{0, 65} {
		for _, tc := range []struct {
			name string
			tail []byte
			size int
			want error
		}{
			{"no-operands", []byte{0x3a}, 0, gvm.ErrTruncated},
			{"missing-delta-invalid-index", []byte{0x3a, 255}, 0, gvm.ErrTruncated},
			{"missing-delta-invalid-region", []byte{0x3a, 0}, 1, gvm.ErrTruncated},
			{"invalid-index", []byte{0x3a, 1, 0}, 0, gvm.ErrInvalidSymbol},
			{"empty", []byte{0x3a, 0, 0}, 0, gvm.ErrInvalidSymbolRegion},
			{"one", []byte{0x3a, 0, 127}, 1, gvm.ErrInvalidSymbolRegion},
		} {
			t.Run(fmt.Sprintf("d%d/%s", depth, tc.name), func(t *testing.T) {
				code := []byte{0x44, 0, 4, 0xff}
				for i := 0; i < depth; i++ {
					code = append(code, 5, byte(i))
				}
				op := len(code)
				code = append(code, tc.tail...)
				v, err := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: uint32(tc.size), Initial: bytes.Repeat([]byte{0x55}, tc.size)}})
				if err != nil {
					t.Fatal(err)
				}
				if v.Run(uint64(depth+1)) != gvm.ErrBudget {
					t.Fatal("setup")
				}
				stack := v.Stack()
				before, _ := v.Symbol(0)
				if v.Run(0) != gvm.ErrBudget || v.PC() != op {
					t.Fatal("budget before fault")
				}
				err = v.Step()
				var fault *gvm.ExecutionError
				if !errors.Is(err, tc.want) || !errors.As(err, &fault) || fault.Offset != op || v.PC() != op+1 {
					t.Fatalf("fault %v pc%d", err, v.PC())
				}
				if v.Step() != err || v.Run(0) != err || v.Run(99) != err {
					t.Fatal("sticky identity")
				}
				after, _ := v.Symbol(0)
				if !bytes.Equal(before, after) || !bytes.Equal(v.Program(), code) || !reflect.DeepEqual(stack, v.Stack()) || v.Halted() || v.PC() != op+1 {
					t.Fatal("atomicity")
				}
			})
		}
	}
	for _, configured := range []bool{false, true} {
		var v *gvm.VM
		if configured {
			var err error
			v, err = gvm.NewWithAddressSpace([]byte{0x3a, 0, 0}, 0, gvm.AddressSpace{})
			if err != nil {
				t.Fatal(err)
			}
		} else {
			v = gvm.New([]byte{0x3a, 0, 0})
		}
		if err := v.Step(); !errors.Is(err, gvm.ErrInvalidSymbol) {
			t.Fatalf("missing symbol %v", err)
		}
	}
}

func TestAddSymbolFetchAliases(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			code   []byte
			target uint32
			steps  uint64
		}{
			{"opcode", []byte{0xcc, 0x3a, 0, 1, 0xff}, 1, 1},
			{"index-and-delta", []byte{0xcc, 0x3a, 0, 255, 0xff}, 2, 1},
			{"delta-and-next", []byte{0xcc, 0x3a, 0, 128, 0xff, 0xff}, 3, 1},
			{"future-fetch", []byte{0xcc, 0x3a, 0, 1, 0xfe, 0, 0xff}, 4, 1},
		} {
			t.Run(fmt.Sprintf("%v/%s", configured, tc.name), func(t *testing.T) {
				var v *gvm.VM
				var err error
				if configured {
					v, err = gvm.NewWithAddressSpace(tc.code, 1, gvm.AddressSpace{FileStart: 1, FileLength: uint32(len(tc.code) - 1), Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Offset: tc.target - 1, Length: 0}, {Region: gvm.AddressFile, Offset: tc.target - 1, Length: 2}}})
				} else {
					v, err = gvm.NewWithSymbols(tc.code, 1, []gvm.SymbolRegion{{ProgramOffset: &tc.target, Length: 2}, {ProgramOffset: &tc.target, Length: 2}})
				}
				if err != nil {
					t.Fatal(err)
				}
				want := append([]byte(nil), tc.code...)
				old := binary.LittleEndian.Uint16(want[tc.target:])
				delta := int8(want[3])
				binary.LittleEndian.PutUint16(want[tc.target:], uint16(int32(old)+int32(delta)))
				if err = v.Run(1); err != gvm.ErrBudget || v.PC() != 4 || v.Halted() || len(v.Stack()) != 0 {
					t.Fatalf("step %v", err)
				}
				got, _ := v.Symbol(1)
				if !bytes.Equal(v.Program(), want) || !bytes.Equal(got, want[tc.target:tc.target+2]) {
					t.Fatalf("alias got%x want%x", v.Program(), want)
				}
				if v.Run(tc.steps) != nil || !v.Halted() || v.PC() != 5 {
					t.Fatalf("coherent next fetch pc%d", v.PC())
				}
				snapshot := v.Program()
				snapshot[0] = 0
				got[0] = 0
				if !bytes.Equal(v.Program(), want) {
					t.Fatal("snapshot isolation")
				}
			})
		}
	}
}

func TestAddSymbolReturnAndEndFetch(t *testing.T) {
	// Fill all 17 return slots using real calls, then add at depth zero and unwind.
	code := []byte{0x44, 0, 4, 0xff}
	for i := 0; i < 16; i++ {
		target := len(code) + 4
		code = append(code, 0x44, byte(target>>8), byte(target), 0x45)
	}
	op := len(code)
	code = append(code, 0x3a, 0, 128, 0x45)
	v, err := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: 2, Initial: []byte{0, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Run(17) != gvm.ErrBudget || v.PC() != op {
		t.Fatal("calls")
	}
	if v.Run(1) != gvm.ErrBudget || v.PC() != op+3 || len(v.Stack()) != 0 {
		t.Fatal("add at full return depth")
	}
	if err = v.Run(18); err != nil || !v.Halted() || v.PC() != 4 {
		t.Fatalf("returns %v pc%d", err, v.PC())
	}
	got, _ := v.Symbol(0)
	if !bytes.Equal(got, []byte{128, 255}) {
		t.Fatal("word")
	}
	v, err = gvm.NewWithSymbols([]byte{0x3a, 0, 0}, 0, []gvm.SymbolRegion{{Length: 2, Initial: []byte{0x34, 0x12}}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Run(1) != gvm.ErrBudget || v.PC() != 3 {
		t.Fatal("exact budget")
	}
	err = v.Step()
	var fault *gvm.ExecutionError
	if !errors.Is(err, gvm.ErrTruncated) || !errors.As(err, &fault) || fault.Offset != 3 || v.PC() != 3 || v.Step() != err || v.Run(0) != err {
		t.Fatalf("end fetch %v", err)
	}
	got, _ = v.Symbol(0)
	if !bytes.Equal(got, []byte{0x34, 0x12}) || v.Halted() {
		t.Fatal("end state")
	}
}
