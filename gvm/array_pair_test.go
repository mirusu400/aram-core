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

func pairProgram(words ...uint16) []byte {
	var code []byte
	for _, word := range words {
		code = append(code, 6, byte(word>>8), byte(word))
	}
	return append(code, 0xb5, 0xff)
}

func pairBytes(words []uint16) []byte {
	data := make([]byte, 2*len(words))
	for i, word := range words {
		binary.LittleEndian.PutUint16(data[2*i:], word)
	}
	return data
}

func pairRun(t *testing.T, region gvm.AddressRegion, initial []uint16, dest, source, count, selector uint16, want []uint16, wantErr error) {
	t.Helper()
	d, s := dest, source
	if region == gvm.AddressFile {
		d |= 0x4000
		s |= 0x4000
	}
	code := pairProgram(0x1234, d, s, count, selector)
	pc := len(code) - 2
	fileStart := len(code)
	payload := pairBytes(initial)
	code = append(code, payload...)
	v := addressVM(t, code, gvm.AddressSpace{FileStart: uint32(fileStart), FileLength: uint32(len(payload)), RAM: payload, Symbols: []gvm.AddressSymbol{
		{Region: region, Length: uint32(len(payload))}, {Region: region, Offset: uint32(dest) * 2, Length: 2},
	}})
	if err := v.Run(5); err != gvm.ErrBudget {
		t.Fatal(err)
	}
	stack := v.Stack()
	err := v.Step()
	if wantErr != nil {
		var fault *gvm.ExecutionError
		if !errors.Is(err, wantErr) || !errors.As(err, &fault) || fault.Offset != pc || v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), stack) || v.Halted() {
			t.Fatalf("fault: %v pc%d stack%x", err, v.PC(), v.Stack())
		}
		if v.Step() != err || v.Run(0) != err || v.Run(99) != err {
			t.Fatal("fault not sticky")
		}
	} else {
		if err != nil || v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), []uint16{0x1234}) || v.Halted() {
			t.Fatalf("operation: %v pc%d stack%x", err, v.PC(), v.Stack())
		}
		if err := v.Step(); err != nil || !v.Halted() {
			t.Fatalf("halt: %v", err)
		}
	}
	got, _ := v.Symbol(0)
	if !bytes.Equal(got, pairBytes(want)) {
		t.Fatalf("live words got%x want%x", got, pairBytes(want))
	}
	expectedCode := append([]byte(nil), code...)
	if region == gvm.AddressFile {
		copy(expectedCode[fileStart:], pairBytes(want))
	}
	if !bytes.Equal(v.Program(), expectedCode) {
		t.Fatal("wrong program mutation")
	}
	alias, _ := v.Symbol(1)
	if !bytes.Equal(alias, pairBytes(want[dest:dest+1])) {
		t.Fatal("descriptor alias lost")
	}
}

func TestArrayPairLiveExamples(t *testing.T) {
	cases := []struct {
		name        string
		initial     []uint16
		d, s, n, op uint16
		want        []uint16
		err         error
	}{
		{"forward-copy", []uint16{1, 2, 3, 4}, 1, 0, 3, 0, []uint16{1, 1, 1, 1}, nil},
		{"reverse-copy", []uint16{1, 2, 3, 4}, 0, 1, 3, 0, []uint16{2, 3, 4, 4}, nil},
		{"forward-add", []uint16{1, 2, 3, 4}, 1, 0, 3, 1, []uint16{1, 3, 6, 10}, nil},
		{"same-reference", []uint16{1, 2, 3, 4}, 0, 0, 4, 1, []uint16{2, 4, 6, 8}, nil},
		{"generated-zero-divisor", []uint16{2, 1, 10}, 1, 0, 2, 4, []uint16{2, 1, 10}, gvm.ErrDivideByZero},
		{"generated-zero-remainder", []uint16{2, 2, 10}, 1, 0, 2, 5, []uint16{2, 2, 10}, gvm.ErrDivideByZero},
		{"late-original-zero", []uint16{8, 2, 0}, 0, 1, 2, 4, []uint16{8, 2, 0}, gvm.ErrDivideByZero},
		{"not-live-source", []uint16{0x5555, 1, 2, 3}, 1, 0, 3, 8, []uint16{0x5555, 0xaaaa, 0x5555, 0xaaaa}, nil},
		{"signed-overflow", []uint16{0x8000, 0xffff}, 0, 1, 1, 4, []uint16{0x8000, 0xffff}, nil},
	}
	for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("r%d/%s", region, tc.name), func(t *testing.T) { pairRun(t, region, tc.initial, tc.d, tc.s, tc.n, tc.op, tc.want, tc.err) })
		}
	}
}

func TestArrayPairLiveMatrix(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		for f, initial := range [][]uint16{{1, 2, 3, 4, 5, 6, 7, 8}, {0x8000, 0xffff, 0, 1, 31, 32, 0x7fff, 0xaaaa}, {2, 1, 10, 3, 0, 7, 0xfff9, 0xffff}} {
			for op := uint16(0); op < 12; op++ {
				for d := uint16(0); d < 5; d++ {
					for s := uint16(0); s < 5; s++ {
						for _, count := range []uint16{0, 1, 4, 0xffff, 0x8000} {
							t.Run(fmt.Sprintf("r%d/f%d/op%d/d%d/s%d/n%x", region, f, op, d, s, count), func(t *testing.T) {
								// Independent live word-array model. On error, host policy discards its prefix.
								want := append([]uint16(nil), initial...)
								var wantErr error
								for i := 0; i < int(int16(count)); i++ {
									y := want[int(s)+i]
									if (op == 4 || op == 5) && y == 0 {
										wantErr = gvm.ErrDivideByZero
										want = initial
										break
									}
									want[int(d)+i] = scalarExpected(want[int(d)+i], y, op)
								}
								pairRun(t, region, initial, d, s, count, op, want, wantErr)
							})
						}
					}
				}
			}
		}
	}
}

func TestArrayPairDifferentBanks(t *testing.T) {
	for _, destFile := range []bool{false, true} {
		for op := uint16(0); op < 12; op++ {
			for _, source := range [][]uint16{{0xffff, 3, 0xfffd, 31, 32, 33, 0x8000}, {1, 1, 1, 1, 1, 1, 0}} {
				t.Run(fmt.Sprintf("file%v/op%d/source%x", destFile, op, source), func(t *testing.T) {
					initial := []uint16{0x8000, 0xfff9, 7, 0xffff, 1, 0x8000, 0x7fff}
					want := append([]uint16(nil), initial...)
					var wantErr error
					for i, y := range source {
						if (op == 4 || op == 5) && y == 0 {
							wantErr = gvm.ErrDivideByZero
							want = initial
							break
						}
						want[i] = scalarExpected(want[i], y, op)
					}
					destRef, sourceRef := uint16(0), uint16(0x4000)
					file, ram := pairBytes(source), pairBytes(initial)
					if destFile {
						destRef, sourceRef = sourceRef, destRef
						file, ram = ram, file
					}
					code := pairProgram(destRef, sourceRef, uint16(len(source)), op)
					pc := len(code) - 2
					fileStart := len(code)
					code = append(code, file...)
					v := addressVM(t, code, gvm.AddressSpace{FileStart: uint32(fileStart), FileLength: uint32(len(file)), RAM: ram, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Length: uint32(len(file))}, {Region: gvm.AddressRAM, Length: uint32(len(ram))}}})
					if err := v.Run(4); err != gvm.ErrBudget {
						t.Fatal(err)
					}
					stack := v.Stack()
					err := v.Step()
					if wantErr != nil {
						if !errors.Is(err, wantErr) || !reflect.DeepEqual(v.Stack(), stack) || v.Run(0) != err || v.PC() != pc+1 {
							t.Fatalf("late separate-bank failure: %v", err)
						}
					} else if err != nil || len(v.Stack()) != 0 {
						t.Fatalf("operation: %v", err)
					}
					gotFile, _ := v.Symbol(0)
					gotRAM, _ := v.Symbol(1)
					wantFile, wantRAM := pairBytes(source), pairBytes(want)
					if destFile {
						wantFile, wantRAM = wantRAM, wantFile
					}
					if !bytes.Equal(gotFile, wantFile) || !bytes.Equal(gotRAM, wantRAM) {
						t.Fatalf("bank isolation file%x ram%x", gotFile, gotRAM)
					}
				})
			}
		}
	}
}

func TestArrayPairGuards(t *testing.T) {
	cases := []struct {
		name  string
		words []uint16
		want  error
	}{
		{"empty", nil, gvm.ErrStackUnderflow}, {"one", []uint16{0}, gvm.ErrStackUnderflow}, {"two", []uint16{0, 0}, gvm.ErrStackUnderflow}, {"three", []uint16{0, 0, 0}, gvm.ErrStackUnderflow},
		{"dest-negative", []uint16{0x8000, 0, 0, 0}, gvm.ErrInvalidAddress},
		{"source-negative", []uint16{0, 0x8000, 0, 0}, gvm.ErrInvalidAddress},
		{"dest-file-negative", []uint16{0xc000, 0, 1, 0}, gvm.ErrInvalidAddress},
		{"source-file-negative", []uint16{0, 0xc000, 1, 0}, gvm.ErrInvalidAddress},
		{"source-before-selector", []uint16{0, 0x8000, 0, 12}, gvm.ErrInvalidAddress},
		{"missing-dest-file", []uint16{0x4000, 0, 1, 0}, gvm.ErrInvalidAddress},
		{"missing-source-file", []uint16{0, 0x4000, 1, 0}, gvm.ErrInvalidAddress},
		{"selector12", []uint16{0, 0, 0, 12}, gvm.ErrInvalidArraySelector},
		{"selector-negative", []uint16{0, 0, 0, 0xffff}, gvm.ErrInvalidArraySelector},
		{"selector-before-span", []uint16{0, 0, 0x7fff, 12}, gvm.ErrInvalidArraySelector},
		{"dest-span", []uint16{3, 0, 2, 0}, gvm.ErrInvalidAddress},
		{"source-span", []uint16{0, 3, 2, 0}, gvm.ErrInvalidAddress},
		{"span-before-divisor", []uint16{0, 0, 5, 4}, gvm.ErrInvalidAddress},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := pairProgram(tc.words...)
			pc := len(code) - 2
			payload := make([]byte, 8)
			v := addressVM(t, code, gvm.AddressSpace{RAM: payload, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 8}}})
			if err := v.Run(uint64(len(tc.words))); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			stack := v.Stack()
			err := v.Step()
			var fault *gvm.ExecutionError
			if !errors.Is(err, tc.want) || !errors.As(err, &fault) || fault.Offset != pc || v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), stack) || v.Halted() {
				t.Fatalf("fault:%v pc%d", err, v.PC())
			}
			if v.Step() != err || v.Run(0) != err || v.Run(9) != err {
				t.Fatal("not sticky")
			}
			got, _ := v.Symbol(0)
			if !bytes.Equal(got, payload) || !bytes.Equal(v.Program(), code) {
				t.Fatal("fault changed memory")
			}
		})
	}
	for _, op := range []uint16{4, 5} {
		for _, count := range []uint16{0, 0xffff, 0x8000} {
			pairRun(t, gvm.AddressRAM, []uint16{0}, 0, 0, count, op, []uint16{0}, nil)
		}
	}
}

func TestArrayPairLegacyUnsupported(t *testing.T) {
	for _, words := range [][]uint16{nil, {0, 0, 0, 0}} {
		code := pairProgram(words...)
		at, err := gvm.NewAt(code, 0)
		if err != nil {
			t.Fatal(err)
		}
		symbols, err := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: 8, Initial: make([]byte, 8)}})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []*gvm.VM{gvm.New(code), at, symbols} {
			if err := v.Run(uint64(len(words))); err != gvm.ErrBudget {
				t.Fatal(err)
			}
			stack := v.Stack()
			pc := v.PC()
			err := v.Step()
			var unsupported *gvm.UnsupportedOpcodeError
			if !errors.As(err, &unsupported) || unsupported.Opcode != 0xb5 || unsupported.Offset != pc || v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), stack) || v.Run(0) != err {
				t.Fatalf("legacy:%v", err)
			}
		}
	}
}

func TestArrayPairCodeAliases(t *testing.T) {
	for _, sameBank := range []bool{false, true} {
		for _, divide := range []bool{false, true} {
			t.Run(fmt.Sprintf("same%v/divide%v", sameBank, divide), func(t *testing.T) {
				sourceRef := uint16(0)
				if sameBank {
					sourceRef = 0x4007
				}
				count, selector := uint16(7), uint16(0)
				source := []uint16{0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff}
				if divide {
					count, selector = 2, 4
					source = []uint16{2, 0}
				}
				code := pairProgram(0x4000, sourceRef, count, selector)
				code = append(code, pairBytes(source)...)
				v := addressVM(t, code, gvm.AddressSpace{FileLength: uint32(len(code)), RAM: pairBytes(source), Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Length: uint32(len(code))}}})
				if err := v.Run(4); err != gvm.ErrBudget {
					t.Fatal(err)
				}
				before := v.Stack()
				err := v.Step()
				if divide {
					if !errors.Is(err, gvm.ErrDivideByZero) || !reflect.DeepEqual(v.Stack(), before) || v.Step() != err || v.PC() != 13 || !bytes.Equal(v.Program(), code) {
						t.Fatalf("late code-alias failure: %v", err)
					}
					return
				}
				want := append([]byte(nil), code...)
				copy(want[:14], bytes.Repeat([]byte{0xff}, 14))
				if err != nil || v.PC() != 13 || len(v.Stack()) != 0 || !bytes.Equal(v.Program(), want) {
					t.Fatalf("code alias: %v code%x", err, v.Program())
				}
				if err := v.Step(); err != nil || !v.Halted() || v.PC() != 14 {
					t.Fatalf("changed future fetch: %v", err)
				}
			})
		}
	}
}

func TestArrayPairMaximumSpan(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		// Maximum initial tagged index and positive signed count bound the union to98300 bytes.
		initial := make([]uint16, 0x3fff+0x7fff)
		for i := range initial {
			initial[i] = 0xa7a7
		}
		pairRun(t, region, initial, 0x3fff, 0, 0x7fff, 0, initial, nil)
	}
}

func TestArrayPairStackBudgetAndReturn(t *testing.T) {
	code := []byte{0x44, 0, 4, 0xff}
	prefix := make([]uint16, 61)
	for i := range prefix {
		prefix[i] = uint16(0x8000 + i)
		code = append(code, 6, byte(prefix[i]>>8), byte(prefix[i]))
	}
	callee := pairProgram(1, 0, 3, 1)
	callee[len(callee)-1] = 0x45
	code = append(code, callee...)
	v := addressVM(t, code, gvm.AddressSpace{RAM: pairBytes([]uint16{1, 2, 3, 4}), Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 8}}})
	if err := v.Run(66); err != gvm.ErrBudget || len(v.Stack()) != 65 {
		t.Fatal(err)
	}
	pc := v.PC()
	before, _ := v.Symbol(0)
	if err := v.Run(0); err != gvm.ErrBudget || v.PC() != pc {
		t.Fatal("zero budget advanced")
	}
	after, _ := v.Symbol(0)
	if !bytes.Equal(before, after) {
		t.Fatal("zero budget wrote")
	}
	if err := v.Step(); err != nil || v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), prefix) {
		t.Fatalf("depth65: %v", err)
	}
	got, _ := v.Symbol(0)
	if !bytes.Equal(got, pairBytes([]uint16{1, 3, 6, 10})) {
		t.Fatalf("live result%x", got)
	}
	if err := v.Run(2); err != nil || !v.Halted() || v.PC() != 4 || !reflect.DeepEqual(v.Stack(), prefix) {
		t.Fatalf("return: %v", err)
	}
}
