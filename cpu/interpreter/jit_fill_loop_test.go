package interpreter

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

type fillLoopTestParameters struct {
	index, limit, step, origin, destination uint32
	lower, upper, stride                    uint32
	color                                   uint16
	lowerSP, upperSP, strideSP, colorSP     uint32
}

func newThumbFillTestBackend(t *testing.T, b *Backend, parameters fillLoopTestParameters, seed int64) *Backend {
	t.Helper()
	t.Cleanup(func() { _ = b.Close() })
	check(t, b.Map(0x1000, 0x104, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	check(t, b.Map(0x2000, 256, cpu.PermissionRead|cpu.PermissionWrite))
	check(t, b.Map(0x3000, 1024, cpu.PermissionRead|cpu.PermissionWrite))
	words := thumbFillLoopWords
	words[2] = 0x9800 | uint16(parameters.lowerSP/4)
	words[5] = 0x9800 | uint16(parameters.upperSP/4)
	words[11] = 0x9800 | uint16(parameters.strideSP/4)
	words[9] = 0x881b | uint16(parameters.colorSP/2)<<6
	code := make([]byte, 0x104)
	for index, word := range words {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	binary.LittleEndian.PutUint16(code[32:], 0xbe00)
	check(t, b.WriteMemory(0x1000, code))
	rng := rand.New(rand.NewSource(seed))
	pixels, stack := make([]byte, 256), make([]byte, 1024)
	_, _ = rng.Read(pixels)
	_, _ = rng.Read(stack)
	binary.LittleEndian.PutUint32(stack[parameters.lowerSP:], parameters.lower)
	binary.LittleEndian.PutUint32(stack[parameters.upperSP:], parameters.upper)
	binary.LittleEndian.PutUint32(stack[parameters.strideSP:], parameters.stride)
	binary.LittleEndian.PutUint16(stack[parameters.colorSP:], parameters.color)
	check(t, b.WriteMemory(0x2000, pixels))
	check(t, b.WriteMemory(0x3000, stack))
	for register := uint32(0); register < cpu.RegisterPC; register++ {
		check(t, b.WriteRegister(register, rng.Uint32()))
	}
	for register, value := range map[uint32]uint32{1: parameters.index, 2: parameters.destination, 6: parameters.limit, 7: parameters.step, 8: parameters.origin, cpu.RegisterSP: 0x3000, cpu.RegisterCPSR: 0xf0000020} {
		check(t, b.WriteRegister(register, value))
	}
	return b
}

func assertThumbFillParity(t *testing.T, precise, translated *Backend, pc uint32, budget uint64) {
	t.Helper()
	want := precise.Run(context.Background(), pc, cpu.ModeThumb, budget)
	got := translated.Run(context.Background(), pc, cpu.ModeThumb, budget)
	if got.Reason != want.Reason || got.Instructions != want.Instructions || got.PC != want.PC || !sameError(got.Err, want.Err) {
		t.Fatalf("result=%+v want=%+v", got, want)
	}
	for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
		actual, err := translated.ReadRegister(register)
		check(t, err)
		expected, err := precise.ReadRegister(register)
		check(t, err)
		if actual != expected {
			t.Fatalf("register %d = %08x want %08x", register, actual, expected)
		}
	}
	for i := range precise.regions {
		if !bytes.Equal(precise.regions[i].data, translated.regions[i].data) {
			t.Fatalf("memory at %#x differs", precise.regions[i].address)
		}
	}
	if !slices.Equal(precise.PCHistory(), translated.PCHistory()) {
		t.Fatal("instruction history differs")
	}
}

func fillLoopTestCases() []fillLoopTestParameters {
	base := fillLoopTestParameters{limit: 24, step: 1, destination: 0x2000, lower: 4, upper: 20, stride: 2, color: 0xa531, lowerSP: 32, upperSP: 28, strideSP: 4, colorSP: 8}
	cases := []fillLoopTestParameters{base}
	left := base
	left.lower = 100
	left.upper = 200
	left.destination = 0x4000
	cases = append(cases, left)
	right := base
	right.origin = 300
	right.lower = 0
	right.upper = 200
	right.destination = 0x4000
	cases = append(cases, right)
	empty := base
	empty.lower = empty.upper
	cases = append(cases, empty)
	wrap := base
	wrap.origin = 0x7fffffff
	wrap.lower = 0x80000000
	wrap.upper = 0x7fffffff
	cases = append(cases, wrap)
	indexWrap := base
	indexWrap.index = 0x7fffffff
	indexWrap.limit = 0x7fffffff
	cases = append(cases, indexWrap)
	zeroStep := base
	zeroStep.step = 0
	zeroStep.index = 10
	cases = append(cases, zeroStep)
	negativeStep := base
	negativeStep.step = ^uint32(0)
	negativeStep.index = 10
	cases = append(cases, negativeStep)
	zeroStride := base
	zeroStride.stride = 0
	cases = append(cases, zeroStride)
	unaligned := base
	unaligned.destination++
	cases = append(cases, unaligned)
	offsets := base
	offsets.lowerSP = 100
	offsets.upperSP = 16
	offsets.strideSP = 144
	offsets.colorSP = 60
	cases = append(cases, offsets)
	return cases
}

func TestThumbFillLoopMatchesPreciseInterpreter(t *testing.T) {
	for index, parameters := range fillLoopTestCases() {
		for budget := uint64(1); budget <= 200; budget++ {
			t.Run(fmt.Sprintf("%d/%d", index, budget), func(t *testing.T) {
				precise := newThumbFillTestBackend(t, New(), parameters, 42)
				translated := newThumbFillTestBackend(t, NewJIT(), parameters, 42)
				assertThumbFillParity(t, precise, translated, 0x1000, budget)
				if budget >= 16 && translated.ExecutionStatistics().AcceleratedLoopIterations == 0 {
					t.Fatal("fill loop was not accelerated")
				}
			})
		}
	}
}

func TestThumbFillLoopRandomRegistersAndWrapping(t *testing.T) {
	rng := rand.New(rand.NewSource(349))
	for index := 0; index < 500; index++ {
		parameters := fillLoopTestCases()[0]
		parameters.index = rng.Uint32()
		parameters.limit = rng.Uint32()
		parameters.step = rng.Uint32()
		parameters.origin = rng.Uint32()
		parameters.lower = rng.Uint32()
		parameters.upper = rng.Uint32()
		parameters.stride = rng.Uint32()
		parameters.color = uint16(rng.Uint32())
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			precise := newThumbFillTestBackend(t, New(), parameters, int64(index))
			translated := newThumbFillTestBackend(t, NewJIT(), parameters, int64(index))
			pc := uint32(0x1000 + (index%2)*2)
			assertThumbFillParity(t, precise, translated, pc, uint64(16+rng.Intn(200)))
		})
	}
}

func TestThumbFillLoopGuardsAndPreciseFaults(t *testing.T) {
	for _, test := range []struct {
		name    string
		address uint32
		trace   bool
	}{
		{"stack color alias", 0x3008, false},
		{"stack stride alias", 0x3004, false},
		{"stack clip alias", 0x3020, false},
		{"executable pixel", 0x1000, false},
		{"tracing", 0x2000, true},
		{"fault after one pixel", 0x20fe, false},
		{"unmapped pixel", 0x4000, false},
		{"address-space edge", 0xffffffff, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			parameters := fillLoopTestCases()[0]
			parameters.index = 10
			parameters.destination = test.address
			precise := newThumbFillTestBackend(t, New(), parameters, 42)
			translated := newThumbFillTestBackend(t, NewJIT(), parameters, 42)
			if test.trace {
				check(t, precise.SetPCHistoryLimit(512))
				check(t, translated.SetPCHistoryLimit(512))
			}
			if test.name != "fault after one pixel" {
				loop := translated.classifyThumbFillLoop(0x1000)
				if retired := translated.accelerateThumbFillLoop(loop, 96, false, false, test.trace); retired != 0 {
					t.Fatal("guarded first iteration accelerated")
				}
			}
			assertThumbFillParity(t, precise, translated, 0x1000, 96)
		})
	}
}

func TestThumbFillLoopRequiresExactSequence(t *testing.T) {
	for index := range thumbFillLoopWords {
		b := newThumbFillTestBackend(t, NewJIT(), fillLoopTestCases()[0], 0)
		var data [2]byte
		address := 0x1000 + uint32(index)*2
		check(t, b.ReadMemory(address, data[:]))
		mask := uint16(1)
		if index == 2 || index == 5 || index == 11 {
			mask = 0x100
		}
		binary.LittleEndian.PutUint16(data[:], binary.LittleEndian.Uint16(data[:])^mask)
		check(t, b.WriteMemory(address, data[:]))
		if b.classifyThumbFillLoop(0x1000) != nil {
			t.Fatalf("changed instruction %d classified", index)
		}
		if index > 0 && b.classifyThumbFillLoopTail(0x1002) != nil {
			t.Fatalf("changed suffix instruction %d classified", index)
		}
	}
}

func TestThumbFillLoopShortResumeParity(t *testing.T) {
	parameters := fillLoopTestCases()[0]
	precise := newThumbFillTestBackend(t, New(), parameters, 123)
	translated := newThumbFillTestBackend(t, NewJIT(), parameters, 123)
	pc := uint32(0x1000)
	for _, budget := range []uint64{1, 15, 1, 16, 13, 10, 15, 1, 17, 32, 60, 3, 80} {
		assertThumbFillParity(t, precise, translated, pc, budget)
		var err error
		pc, err = precise.ReadRegister(cpu.RegisterPC)
		check(t, err)
	}
}

func TestThumbFillLoopStackOffsetsAndFaults(t *testing.T) {
	for _, offset := range []uint32{0, 4, 28, 32, 256, 1020} {
		for _, colorOffset := range []uint32{0, 2, 8, 60, 62} {
			t.Run(fmt.Sprintf("%d/%d", offset, colorOffset), func(t *testing.T) {
				parameters := fillLoopTestCases()[0]
				parameters.lowerSP, parameters.upperSP, parameters.strideSP = offset, offset, offset
				parameters.colorSP = colorOffset
				precise := newThumbFillTestBackend(t, New(), parameters, 42)
				translated := newThumbFillTestBackend(t, NewJIT(), parameters, 42)
				assertThumbFillParity(t, precise, translated, 0x1000, 160)
			})
		}
	}
	for _, stack := range []uint32{0x33f4, 0x4000, 0xfffffffc} {
		t.Run(fmt.Sprintf("fault/%x", stack), func(t *testing.T) {
			parameters := fillLoopTestCases()[0]
			precise := newThumbFillTestBackend(t, New(), parameters, 42)
			translated := newThumbFillTestBackend(t, NewJIT(), parameters, 42)
			check(t, precise.WriteRegister(cpu.RegisterSP, stack))
			check(t, translated.WriteRegister(cpu.RegisterSP, stack))
			assertThumbFillParity(t, precise, translated, 0x1000, 160)
			if translated.ExecutionStatistics().AcceleratedLoopIterations != 0 {
				t.Fatal("faulting stack accelerated")
			}
		})
	}
}

func TestThumbFillLoopDisablesForObservedAndSystemExecution(t *testing.T) {
	for _, mode := range []string{"system", "trap", "trace", "physical", "tlb"} {
		t.Run(mode, func(t *testing.T) {
			b := newThumbFillTestBackend(t, NewJIT(), fillLoopTestCases()[0], 42)
			loop := b.classifyThumbFillLoop(0x1000)
			before := b.regs
			if mode == "physical" {
				b.physicalAccess = true
			}
			if mode == "tlb" {
				b.tlb = make([]tlbEntry, 1)
			}
			if retired := b.accelerateThumbFillLoop(loop, 160, mode == "system", mode == "trap", mode == "trace"); retired != 0 || b.regs != before {
				t.Fatal("observed/system execution accelerated")
			}
		})
	}
}

func TestThumbFillLoopTailUsesActualOriginRegister(t *testing.T) {
	for _, origin := range []uint32{0, 10, 0x7fffffff, 0x80000000} {
		for budget := uint64(1); budget <= 160; budget++ {
			t.Run(fmt.Sprintf("%x/%d", origin, budget), func(t *testing.T) {
				parameters := fillLoopTestCases()[0]
				precise := newThumbFillTestBackend(t, New(), parameters, 42)
				translated := newThumbFillTestBackend(t, NewJIT(), parameters, 42)
				check(t, precise.WriteRegister(0, origin))
				check(t, translated.WriteRegister(0, origin))
				assertThumbFillParity(t, precise, translated, 0x1002, budget)
				if budget >= 15 && translated.ExecutionStatistics().AcceleratedLoopIterations == 0 {
					t.Fatal("fill suffix did not accelerate")
				}
			})
		}
	}
}

func TestThumbFillLoopTailDoesNotAssumeSkippedInstruction(t *testing.T) {
	parameters := fillLoopTestCases()[0]
	precise := newThumbFillTestBackend(t, New(), parameters, 42)
	translated := newThumbFillTestBackend(t, NewJIT(), parameters, 42)
	assertThumbFillParity(t, precise, translated, 0x1002, 16)
	pc, err := precise.ReadRegister(cpu.RegisterPC)
	check(t, err)
	for _, b := range []*Backend{precise, translated} {
		// Change the back-branch target to MOV r0,r9 after the suffix is cached.
		check(t, b.WriteMemory(0x1000, thumbTestCode(0x4648)))
		check(t, b.WriteRegister(9, 10))
	}
	if translated.classifyThumbFillLoop(0x1000) != nil || translated.classifyThumbFillLoopTail(0x1002) == nil {
		t.Fatal("suffix classification depends on its unexecuted prefix")
	}
	assertThumbFillParity(t, precise, translated, pc, 200)
}

func TestThumbFillLoopRejectsWrappingDecodedCodeSpan(t *testing.T) {
	for _, start := range []uint32{0xffffffe0, 0xfffffff0} {
		t.Run(fmt.Sprintf("%x", start), func(t *testing.T) {
			b := NewJIT()
			defer b.Close()
			code := thumbTestCode(thumbFillLoopWords[:]...)
			firstSize := uint32(1<<32 - uint64(start))
			check(t, b.Map(start, firstSize, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
			check(t, b.WriteMemory(start, code[:firstSize]))
			if firstSize < uint32(len(code)) {
				check(t, b.Map(0, uint32(len(code))-firstSize, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
				check(t, b.WriteMemory(0, code[firstSize:]))
			}
			if b.classifyThumbFillLoop(start) != nil || b.classifyThumbFillLoopTail(start+2) != nil {
				t.Fatal("wrapping decoded code span classified")
			}
		})
	}
}
