package ktf

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestJavaArrayNewUsesPrimitiveElementWidthForArrayClass(t *testing.T) {
	for _, test := range []struct {
		className   string
		elementSize uint32
	}{
		{"[J", 8},
		{"[D", 8},
		{"[I", 4},
		{"[[J", 4},
	} {
		t.Run(test.className, func(t *testing.T) {
			runtime := newMNTestRuntime(t)
			class := ensureClass(t, runtime, test.className)
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, class))
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 3))
			array, err := ktfJavaArrayNew(context.Background(), runtime)
			check(t, err)
			fields := readU32(t, runtime, array)
			want := uint32(8 + 3*test.elementSize)
			if size := runtime.Heap.Root().Allocations[fields]; size < want {
				t.Fatalf("%s backing allocation = %d bytes, want at least %d", test.className, size, want)
			}
			if body, count, element, _, ok := runtime.ArrayShape(array); !ok ||
				body != fields+8 || count != 3 || element != test.elementSize {
				t.Fatalf("%s shape = body %08x count %d element %d valid %v", test.className, body, count, element, ok)
			}
		})
	}
}

func TestNewJavaArrayRejectsMismatchedElementWidth(t *testing.T) {
	runtime := newMNTestRuntime(t)
	if _, err := runtime.NewJavaArray("[J", 3, 4); err == nil {
		t.Fatal("four-byte backing allocation accepted for a long array")
	}
}
