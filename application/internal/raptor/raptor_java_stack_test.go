package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorJavaStackPushAndPeekVirtualSlots(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32)}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	java.flatVirtual = make([]raptorJavaMethod, 4)
	class, err := runtime.ensureRaptorHostClass(java, "java/util/Stack")
	check(t, err)
	stack, err := runtime.NewRaptorJavaObject(class.Holder)
	check(t, err)
	for _, text := range []string{"first", "second"} {
		value, err := runtime.NewRaptorJavaString(text)
		check(t, err)
		for _, offset := range []uint32{0x84, 0x88, 0x88} {
			slot, err := public.ReadU32(class.vtable + offset)
			check(t, err)
			if slot == java.noopStub {
				t.Fatalf("Stack slot %02x still resolves to no-op", offset)
			}
			check(t, public.CPU.WriteRegister(cpu.RegisterR0, stack))
			check(t, public.CPU.WriteRegister(cpu.RegisterR1, value))
			key := runtime.importSlots[((slot&^1)-raptorImportStubBase)/4]
			check(t, runtime.dispatchImport(context.Background(), key))
			got, err := public.CPU.ReadRegister(cpu.RegisterR0)
			check(t, err)
			if got != value {
				t.Fatalf("Stack slot %02x returned %08x, want original Raptor reference %08x", offset, got, value)
			}
		}
	}
}
