package gnex32

import (
	"errors"
	"reflect"
	"testing"
)

func TestArrayToVarOperations(t *testing.T) {
	tests := []struct {
		name      string
		old       uint32
		value     uint32
		operation uint32
		want      uint32
	}{
		{"set", 12, 3, 0, 3},
		{"add", 12, 3, 1, 15},
		{"subtract", 12, 3, 2, 9},
		{"multiply", 12, 3, 3, 36},
		{"divide", 12, 3, 4, 4},
		{"modulo", 12, 5, 5, 2},
		{"and", 12, 3, 6, 0},
		{"or", 12, 3, 7, 15},
		{"not zero scalar", 12, 0, 8, 1},
		{"not nonzero scalar", 12, 3, 8, 0},
		{"xor", 12, 3, 9, 15},
		{"signed right shift", ^uint32(7), 1, 10, ^uint32(3)},
		{"left shift", 12, 3, 11, 96},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := newNVRuntime(t, 1)
			if err := runtime.vm.memory.WriteWord(0, 0, test.old); err != nil {
				t.Fatal(err)
			}
			runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}
			result, err := runtime.service(0xdd, []uint32{test.old, test.value, 1, test.operation})
			if err != nil {
				t.Fatal(err)
			}
			if result.Pop != 4 || len(result.Push) != 0 {
				t.Fatalf("ArrayToVar stack result = %+v", result)
			}
			if got := gnex32TestSymbolWords(t, runtime, 0, 1)[0]; got != test.want {
				t.Fatalf("ArrayToVar result = %#x, want %#x", got, test.want)
			}
		})
	}
}

func TestArrayToVarFailuresAreAtomic(t *testing.T) {
	for _, operation := range []uint32{4, 5} {
		runtime := newNVRuntime(t, 3)
		want := []uint32{12, 18, 24}
		for index, value := range want {
			if err := runtime.vm.memory.WriteWord(0, index, value); err != nil {
				t.Fatal(err)
			}
		}
		runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}
		if _, err := runtime.service(0xdd, []uint32{want[0], 0, 3, operation}); !errors.Is(err, ErrDivisionByZero) {
			t.Fatalf("operation %d zero-divisor error = %v", operation, err)
		}
		if got := gnex32TestSymbolWords(t, runtime, 0, 3); !reflect.DeepEqual(got, want) {
			t.Fatalf("operation %d changed array to %v", operation, got)
		}
	}

	runtime := newNVRuntime(t, 3)
	want := []uint32{1, 2, 3}
	for index, value := range want {
		if err := runtime.vm.memory.WriteWord(0, index, value); err != nil {
			t.Fatal(err)
		}
	}
	runtime.vm.refs[0] = gnex32SymbolRef{symbol: 0}
	if _, err := runtime.service(0xdd, []uint32{want[0], 7, 4, 1}); err == nil {
		t.Fatal("ArrayToVar accepted an out-of-bounds range")
	}
	if got := gnex32TestSymbolWords(t, runtime, 0, 3); !reflect.DeepEqual(got, want) {
		t.Fatalf("out-of-bounds ArrayToVar changed array to %v", got)
	}
	if _, err := runtime.service(0xdd, []uint32{want[0], 7, 3, 12}); err == nil {
		t.Fatal("ArrayToVar accepted operation 12")
	}
	if got := gnex32TestSymbolWords(t, runtime, 0, 3); !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid-operation ArrayToVar changed array to %v", got)
	}
}
