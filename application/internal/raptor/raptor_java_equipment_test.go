package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Legend of Master stores the original inventory index in an Integer, then
// calls these virtual slots while equipping and swapping weapons (issue #371).
func TestRaptorJavaEquipmentVirtualSlots(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	java.flatVirtual = make([]raptorJavaMethod, 3)

	integerClass, err := runtime.ensureRaptorHostClass(java, "java/lang/Integer")
	check(t, err)
	boxedIndex, err := runtime.NewRaptorJavaObject(integerClass.Holder)
	check(t, err)
	assertRaptorJavaVirtualMethod(t, runtime, java, integerClass, 0x34, "intValue", "()I")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, boxedIndex))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 2))
	_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className: "java/lang/Integer", Name: "<init>", descriptor: "(I)V",
	})
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, boxedIndex))
	index, err := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className: "java/lang/Integer", Name: "intValue", descriptor: "()I",
	})
	check(t, err)
	if index.Low != 2 {
		t.Fatalf("Integer.intValue = %d, want inventory index 2", index.Low)
	}

	vectorClass, err := runtime.ensureRaptorHostClass(java, "java/util/Vector")
	check(t, err)
	vector, err := runtime.NewRaptorJavaObject(vectorClass.Holder)
	check(t, err)
	assertRaptorJavaVirtualMethod(t, runtime, java, vectorClass, 0x6c,
		"setElementAt", "(Ljava/lang/Object;I)V")
	items := make([]uint32, 4)
	for i, name := range []string{"potion A", "potion B", "sword A", "sword B"} {
		items[i], err = runtime.NewRaptorJavaString(name)
		check(t, err)
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, vector))
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, items[i]))
		_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
			className: "java/util/Vector", Name: "addElement", descriptor: "(Ljava/lang/Object;)V",
		})
		check(t, err)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, vector))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, index.Low))
	_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className: "java/util/Vector", Name: "removeElementAt", descriptor: "(I)V",
	})
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, vector))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, items[2]))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, index.Low))
	_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className: "java/util/Vector", Name: "setElementAt", descriptor: "(Ljava/lang/Object;I)V",
	})
	check(t, err)
	entries := java.Host.Vectors[java.lgtToKTF[vector]]
	if len(entries) != 3 || entries[0] != java.lgtToKTF[items[0]] ||
		entries[1] != java.lgtToKTF[items[1]] || entries[2] != java.lgtToKTF[items[2]] {
		t.Fatalf("inventory after swap = %v; want two potions and the old sword", entries)
	}
}
