package ktf

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestKTFMNArrayClassAddsDimensionToArrayComponents(t *testing.T) {
	for _, test := range []struct{ component, want string }{
		{"java/lang/String", "[Ljava/lang/String;"},
		{"[B", "[[B"},
		{"[[I", "[[[I"},
	} {
		t.Run(test.component, func(t *testing.T) {
			runtime := newMNTestRuntime(t)
			component := ensureClass(t, runtime, test.component)
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, component))
			class, err := ktfMNResolveArrayClass(context.Background(), runtime)
			check(t, err)
			if got := inspectClass(t, runtime, class).Name; got != test.want {
				t.Fatalf("MN array class = %q, want %q", got, test.want)
			}
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, class))
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 15))
			array, err := ktfMNArrayNew(context.Background(), runtime)
			check(t, err)
			if got := readU32(t, runtime, array+4); got != class {
				t.Fatalf("allocated array class = %08x, want %08x", got, class)
			}
			fields := readU32(t, runtime, array)
			if got := readU32(t, runtime, fields+4); got != 15 {
				t.Fatalf("array length = %d, want 15", got)
			}
			// All slots contain references, even when the component is byte[].
			for index := uint32(0); index < 15; index++ {
				check(t, runtime.WriteU32(fields+8+index*4, 0x12340000+index))
			}
			if got := readU32(t, runtime, fields+8+14*4); got != 0x1234000e {
				t.Fatalf("last reference slot = %08x", got)
			}
		})
	}
}
