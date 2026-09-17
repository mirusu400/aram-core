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

func copy35Prefix(depth int) []byte {
	var code []byte
	for i := 0; i < depth; i++ {
		code = append(code, 6, byte(i>>8), byte(i))
	}
	return code
}

func copy35Fault(t *testing.T, v *gvm.VM, want error, symbols int) {
	t.Helper()
	offset, code, stack := v.PC(), v.Program(), v.Stack()
	before := make([][]byte, symbols)
	for i := range before {
		before[i], _ = v.Symbol(uint8(i))
	}
	err := v.Step()
	var execution *gvm.ExecutionError
	if !errors.Is(err, want) || !errors.As(err, &execution) || execution.Offset != offset {
		t.Errorf("fault=%v want=%v offset%d", err, want, offset)
	}
	for _, again := range []error{v.Step(), v.Run(0), v.Run(9)} {
		if again != err {
			t.Error("fault not sticky")
		}
	}
	wantPC := offset + 1
	if offset >= len(code) {
		wantPC = offset
	}
	if v.PC() != wantPC || v.Halted() || !bytes.Equal(code, v.Program()) || !reflect.DeepEqual(stack, v.Stack()) {
		t.Fatal("fault/retry changed public state")
	}
	for i, previous := range before {
		got, _ := v.Symbol(uint8(i))
		if !bytes.Equal(previous, got) {
			t.Fatalf("fault changed symbol%d", i)
		}
	}
}

func TestCopySymbol35LegacyContract(t *testing.T) {
	for _, depth := range []int{0, 64, 65} {
		for _, size := range []int{2, 3, 511, 512} {
			for _, pair := range [][2]byte{{0, 1}, {255, 128}, {128, 255}} {
				for _, value := range []uint16{0, 0x8000, 0xffff, 0x1234} {
					t.Run(fmt.Sprintf("d%d/n%d/%d-%d/v%x", depth, size, pair[0], pair[1], value), func(t *testing.T) {
						code := copy35Prefix(depth)
						offset := len(code)
						code = append(code, 0x35, pair[0], pair[1], 0xff)
						dst, src := bytes.Repeat([]byte{0xa5}, size), bytes.Repeat([]byte{0x5a}, size)
						binary.LittleEndian.PutUint16(src, value)
						bindings := make([]gvm.SymbolRegion, 256)
						bindings[pair[0]] = gvm.SymbolRegion{Length: uint32(size), Initial: dst}
						bindings[pair[1]] = gvm.SymbolRegion{Length: uint32(size), Initial: src}
						v, err := gvm.NewWithSymbols(code, 0, bindings)
						if err != nil {
							t.Fatal(err)
						}
						if err = v.Run(uint64(depth)); err != gvm.ErrBudget {
							t.Fatal(err)
						}
						before := v.Stack()
						if err = v.Run(0); err != gvm.ErrBudget || v.PC() != offset {
							t.Fatal("zero budget moved")
						}
						if err = v.Step(); err != nil {
							t.Fatalf("35 copy: %v", err)
						}
						want := bytes.Repeat([]byte{0xa5}, size)
						binary.LittleEndian.PutUint16(want, value)
						got, _ := v.Symbol(pair[0])
						source, _ := v.Symbol(pair[1])
						if !bytes.Equal(got, want) || !bytes.Equal(source, src) || !reflect.DeepEqual(before, v.Stack()) || v.PC() != offset+3 || v.Halted() {
							t.Fatalf("copy=%x source=%x pc%d", got, source, v.PC())
						}
						if !bytes.Equal(dst, bytes.Repeat([]byte{0xa5}, size)) || !bytes.Equal(code, v.Program()) {
							t.Fatal("caller ownership")
						}
						got[0] ^= 0xff
						source[0] ^= 0xff
						dst[0] ^= 0xff
						src[0] ^= 0xff
						snapshot := v.Program()
						snapshot[0] ^= 0xff
						if s := v.Stack(); len(s) > 0 {
							s[0] ^= 0xffff
						}
						again, _ := v.Symbol(pair[0])
						sourceAgain, _ := v.Symbol(pair[1])
						if !bytes.Equal(again, want) || binary.LittleEndian.Uint16(sourceAgain) != value || !bytes.Equal(v.Program(), code) || !reflect.DeepEqual(before, v.Stack()) {
							t.Fatal("snapshot/input alias escaped")
						}
						if err = v.Step(); err != nil || !v.Halted() || v.PC() != offset+4 {
							t.Fatalf("next fetch: %v", err)
						}
					})
				}
			}
		}
	}
}

func TestCopySymbol35ConfiguredRegions(t *testing.T) {
	for _, dstRegion := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		for _, srcRegion := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
			for _, size := range []int{0, 1, 3, 512} {
				for _, depth := range []int{0, 64, 65} {
					t.Run(fmt.Sprintf("r%d-%d/n%d/d%d", dstRegion, srcRegion, size, depth), func(t *testing.T) {
						// Odd starts and nonzero FileStart. Short descriptor views must not
						// constrain a scalar word which fits the configured backing region.
						extent := 2*size + 9
						dstOffset, srcOffset := 1, size+5
						file, ram := bytes.Repeat([]byte{0xa5}, extent), bytes.Repeat([]byte{0xa5}, extent)
						source := file
						if srcRegion == gvm.AddressRAM {
							source = ram
						}
						source[srcOffset], source[srcOffset+1] = 0x34, 0x80
						originalFile, originalRAM := append([]byte(nil), file...), append([]byte(nil), ram...)
						code := append([]byte{0xcc}, file...)
						entry := len(code)
						code = append(code, copy35Prefix(depth)...)
						offset := len(code)
						code = append(code, 0x35, 255, 128, 0xff)
						symbols := make([]gvm.AddressSymbol, 256)
						for i := range symbols {
							symbols[i].Region = gvm.AddressRAM
						}
						symbols[255] = gvm.AddressSymbol{Region: dstRegion, Offset: uint32(dstOffset), Length: uint32(size)}
						symbols[128] = gvm.AddressSymbol{Region: srcRegion, Offset: uint32(srcOffset), Length: uint32(size)}
						symbols[0] = gvm.AddressSymbol{Region: gvm.AddressFile, Length: uint32(extent)}
						symbols[1] = gvm.AddressSymbol{Region: gvm.AddressRAM, Length: uint32(extent)}
						v, err := gvm.NewWithAddressSpace(code, uint32(entry), gvm.AddressSpace{FileStart: 1, FileLength: uint32(extent), RAM: ram, Symbols: symbols})
						if err != nil {
							t.Fatal(err)
						}
						if err = v.Run(uint64(depth)); err != gvm.ErrBudget {
							t.Fatal(err)
						}
						stack := v.Stack()
						if err = v.Step(); err != nil {
							t.Fatalf("configured copy: %v", err)
						}
						wantFile, wantRAM := append([]byte(nil), file...), append([]byte(nil), ram...)
						target := wantFile
						if dstRegion == gvm.AddressRAM {
							target = wantRAM
						}
						target[dstOffset], target[dstOffset+1] = 0x34, 0x80
						gotFile, _ := v.Symbol(0)
						gotRAM, _ := v.Symbol(1)
						wantCode := append([]byte(nil), code...)
						copy(wantCode[1:1+extent], wantFile)
						if !bytes.Equal(gotFile, wantFile) || !bytes.Equal(gotRAM, wantRAM) || !bytes.Equal(v.Program(), wantCode) || !reflect.DeepEqual(stack, v.Stack()) || v.PC() != offset+3 {
							t.Fatal("wrong configured span/region/width/stack")
						}
						if !bytes.Equal(file, originalFile) || !bytes.Equal(ram, originalRAM) || !bytes.Equal(code[1:1+extent], originalFile) {
							t.Fatal("caller backing changed")
						}
						gotFile[0] ^= 0xff
						gotRAM[0] ^= 0xff
						ram[0] ^= 0xff
						code[1] ^= 0xff
						symbols[255].Offset = uint32(extent)
						againFile, _ := v.Symbol(0)
						againRAM, _ := v.Symbol(1)
						if !bytes.Equal(againFile, wantFile) || !bytes.Equal(againRAM, wantRAM) {
							t.Fatal("snapshot/input escaped")
						}
						if err = v.Step(); err != nil || !v.Halted() {
							t.Fatalf("halt: %v", err)
						}
					})
				}
			}
		}
	}
}

func TestCopySymbol35FaultOrder(t *testing.T) {
	cases := []struct {
		name     string
		ops      []byte
		dst, src int
		want     error
	}{
		{"missing-both", nil, 0, 0, gvm.ErrTruncated},
		{"missing-source-before-destination", []byte{255}, 0, 0, gvm.ErrTruncated},
		{"destination-index-first", []byte{255, 255}, 0, 0, gvm.ErrInvalidSymbol},
		{"destination-empty-before-source-index", []byte{0, 255}, 0, 0, gvm.ErrInvalidSymbolRegion},
		{"destination-short-before-source-index", []byte{0, 255}, 1, 0, gvm.ErrInvalidSymbolRegion},
		{"source-index-after-valid-destination", []byte{0, 255}, 2, 0, gvm.ErrInvalidSymbol},
		{"source-empty", []byte{0, 1}, 2, 0, gvm.ErrInvalidSymbolRegion},
		{"source-short", []byte{0, 1}, 3, 1, gvm.ErrInvalidSymbolRegion},
	}
	for _, mode := range []string{"legacy", "file", "ram"} {
		for _, depth := range []int{0, 65} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("%s/d%d/%s", mode, depth, tc.name), func(t *testing.T) {
					// Keep a real saved return pending. Sticky failures expose no API for
					// resuming it, so success preservation is tested by nested returns below.
					code := []byte{0x44, 0, 4, 0xff}
					code = append(code, copy35Prefix(depth)...)
					code = append(code, 0x35)
					code = append(code, tc.ops...)
					var v *gvm.VM
					var err error
					if mode == "legacy" {
						v, err = gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: uint32(tc.dst), Initial: bytes.Repeat([]byte{0xa5}, tc.dst)}, {Length: uint32(tc.src), Initial: bytes.Repeat([]byte{0x5a}, tc.src)}})
					} else {
						// Full global span, not descriptor shape, supplies these available sizes.
						// Both descriptors are empty so resolving via view length would be wrong.
						backing := bytes.Repeat([]byte{0xa5}, 4)
						region := gvm.AddressRAM
						if mode == "file" {
							region = gvm.AddressFile
						}
						start := len(code)
						code = append(code, backing...)
						// Appended backing would mask inline truncation. Truncation cases use
						// the RAM mode's empty file span, leaving program end at the opcode.
						if len(tc.ops) < 2 {
							code = code[:start]
							region = gvm.AddressRAM
						}
						v, err = gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{FileStart: uint32(start), FileLength: uint32(len(code) - start), RAM: backing, Symbols: []gvm.AddressSymbol{{Region: region, Offset: uint32(4 - tc.dst)}, {Region: region, Offset: uint32(4 - tc.src)}, {Region: gvm.AddressRAM, Length: 4}}})
					}
					if err != nil {
						t.Fatal(err)
					}
					if err = v.Run(uint64(depth + 1)); err != gvm.ErrBudget {
						t.Fatal(err)
					}
					n := 2
					if mode != "legacy" {
						n = 3
					}
					copy35Fault(t, v, tc.want, n)
				})
			}
		}
	}
}

func TestCopySymbol35GlobalLastWordAndEnd(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		for _, sourceBad := range []bool{false, true} {
			for _, offset := range []uint32{2, 3, 4} {
				t.Run(fmt.Sprintf("r%d/source%v/off%d", region, sourceBad, offset), func(t *testing.T) {
					code := []byte{0xa5, 0xa5, 0x34, 0x12, 0x35, 0, 1, 0xff}
					dst, src := offset, uint32(0)
					if sourceBad {
						dst, src = 0, offset
					}
					v, err := gvm.NewWithAddressSpace(code, 4, gvm.AddressSpace{FileLength: 4, RAM: []byte{0xa5, 0xa5, 0x34, 0x12}, Symbols: []gvm.AddressSymbol{{Region: region, Offset: dst}, {Region: region, Offset: src}, {Region: region, Length: 4}}})
					if err != nil {
						t.Fatal(err)
					}
					if offset != 2 {
						copy35Fault(t, v, gvm.ErrInvalidSymbolRegion, 3)
						return
					}
					if err = v.Step(); err != nil {
						t.Fatal(err)
					}
					got, _ := v.Symbol(2)
					want := []byte{0xa5, 0xa5, 0xa5, 0xa5}
					if sourceBad {
						want = []byte{0x34, 0x12, 0x34, 0x12}
					}
					if !bytes.Equal(got, want) || v.PC() != 7 {
						t.Fatalf("last full word: %x pc%d", got, v.PC())
					}
				})
			}
		}
	}
}

func TestCopySymbol35Overlaps(t *testing.T) {
	for _, mode := range []string{"legacy", "file", "ram"} {
		for _, pair := range [][2]uint32{{1, 1}, {1, 2}, {2, 1}} {
			t.Run(fmt.Sprintf("%s/%d-%d", mode, pair[0], pair[1]), func(t *testing.T) {
				data := []byte{0x11, 0x22, 0x33, 0x44, 0x55}
				code := append(append([]byte(nil), data...), 0x35, 0, 1, 0xff)
				var v *gvm.VM
				var err error
				if mode == "legacy" {
					a, b := pair[0], pair[1]
					v, err = gvm.NewWithSymbols(code, 5, []gvm.SymbolRegion{{ProgramOffset: &a, Length: 2}, {ProgramOffset: &b, Length: 2}})
				} else {
					region := gvm.AddressFile
					if mode == "ram" {
						region = gvm.AddressRAM
					}
					v, err = gvm.NewWithAddressSpace(code, 5, gvm.AddressSpace{FileLength: 5, RAM: data, Symbols: []gvm.AddressSymbol{{Region: region, Offset: pair[0], Length: 2}, {Region: region, Offset: pair[1], Length: 2}, {Region: region, Length: 5}}})
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = v.Step(); err != nil {
					t.Fatal(err)
				}
				want := append([]byte(nil), data...)
				copy(want[pair[0]:pair[0]+2], data[pair[1]:pair[1]+2])
				got := v.Program()[:5]
				if mode == "ram" {
					got, _ = v.Symbol(2)
				}
				if !bytes.Equal(got, want) || v.PC() != 8 || len(v.Stack()) != 0 {
					t.Fatalf("overlap=%x want%x", got, want)
				}
			})
		}
	}
}

func TestCopySymbol35OperandAndFutureFetchAliases(t *testing.T) {
	for _, address := range []bool{false, true} {
		for _, self := range []bool{false, true} {
			t.Run(fmt.Sprintf("address%v/self%v", address, self), func(t *testing.T) {
				// Source first word ff00 becomes a future halt, or destroys both operand
				// bytes. Nonzero entry/FileStart prevents accidental whole-buffer offsets.
				code := []byte{0xcc, 0x35, 0, 1, 0xff, 0xff, 0xff, 0}
				dst, src := uint32(2), uint32(6)
				if !self {
					dst = 4
					code[4] = 0xfe
				}
				var v *gvm.VM
				var err error
				if address {
					v, err = gvm.NewWithAddressSpace(code, 1, gvm.AddressSpace{FileStart: 1, FileLength: 7, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Offset: dst - 1, Length: 2}, {Region: gvm.AddressFile, Offset: src - 1, Length: 2}}})
				} else {
					v, err = gvm.NewWithSymbols(code, 1, []gvm.SymbolRegion{{ProgramOffset: &dst, Length: 2}, {ProgramOffset: &src, Length: 2}})
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = v.Step(); err != nil || v.PC() != 4 {
					t.Fatalf("alias: %v pc%d", err, v.PC())
				}
				want := append([]byte(nil), code...)
				want[dst], want[dst+1] = 0xff, 0
				if !bytes.Equal(want, v.Program()) {
					t.Fatalf("program=%x want%x", v.Program(), want)
				}
				if err = v.Step(); err != nil || !v.Halted() || v.PC() != 5 {
					t.Fatalf("future: %v pc%d", err, v.PC())
				}
			})
		}
	}
}

func TestCopySymbol35SavedReturns(t *testing.T) {
	for _, address := range []bool{false, true} {
		t.Run(fmt.Sprintf("address%v", address), func(t *testing.T) {
			code := []byte{0x44, 0, 4, 0xff, 5, 7, 0x44, 0, 10, 0x45, 0x35, 0, 1, 0x45}
			var v *gvm.VM
			var err error
			if address {
				v, err = gvm.NewWithAddressSpace(code, 0, gvm.AddressSpace{RAM: []byte{0xa5, 0xa5, 0x34, 0x80}, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 2}, {Region: gvm.AddressRAM, Offset: 2, Length: 2}}})
			} else {
				v, err = gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: 2, Initial: []byte{0xa5, 0xa5}}, {Length: 2, Initial: []byte{0x34, 0x80}}})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = v.Run(3); err != gvm.ErrBudget || v.PC() != 10 {
				t.Fatalf("setup: %v", err)
			}
			for _, wantPC := range []int{13, 9, 3} {
				if err = v.Step(); err != nil || v.PC() != wantPC || !reflect.DeepEqual(v.Stack(), []uint16{7}) {
					t.Fatalf("copy/return: %v pc%d want%d", err, v.PC(), wantPC)
				}
			}
			got, _ := v.Symbol(0)
			if !bytes.Equal(got, []byte{0x34, 0x80}) {
				t.Fatalf("copy=%x", got)
			}
			if err = v.Step(); err != nil || !v.Halted() {
				t.Fatal(err)
			}
		})
	}
}

func TestCopySymbol35ExhaustiveWords(t *testing.T) {
	for raw := 0; raw < 65536; raw++ {
		// Copy at program end proves no third inline byte is required. Next fetch
		// truncates only after this instruction has completed successfully.
		v, err := gvm.NewWithSymbols([]byte{0x35, 0, 1}, 0, []gvm.SymbolRegion{{Length: 3, Initial: []byte{0xa5, 0xa5, 0x55}}, {Length: 2, Initial: []byte{byte(raw), byte(raw >> 8)}}})
		if err != nil {
			t.Fatal(err)
		}
		if err = v.Step(); err != nil {
			t.Fatalf("raw%x: %v", raw, err)
		}
		got, _ := v.Symbol(0)
		if !bytes.Equal(got, []byte{byte(raw), byte(raw >> 8), 0x55}) || v.PC() != 3 || len(v.Stack()) != 0 {
			t.Fatalf("raw%x: %x pc%d", raw, got, v.PC())
		}
		if raw == 0 {
			copy35Fault(t, v, gvm.ErrTruncated, 2)
		}
	}
}
