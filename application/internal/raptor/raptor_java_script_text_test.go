package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Legend of Master calls these fixed CLDC slots while loading its item and
// dialogue text. A zero-returning fallback turns the parsed strings into null
// and drops the Vector entries altogether (issue #341).
func TestRaptorJavaScriptTextVirtualSlots(t *testing.T) {
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

	value, err := runtime.NewRaptorJavaString("  가  ")
	check(t, err)
	stringClass := java.ClassByName["java/lang/String"]
	assertRaptorJavaVirtualMethod(t, runtime, java, stringClass, 0x88,
		"trim", "()Ljava/lang/String;")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, value))
	trimmed, err := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className: "java/lang/String", Name: "trim", descriptor: "()Ljava/lang/String;",
	})
	check(t, err)
	if trimmed.Low == 0 {
		t.Fatal("String.trim returned null")
	}
	if got := java.Host.JavaStrings[java.lgtToKTF[trimmed.Low]]; got != "가" {
		t.Fatalf("trimmed script line = %q, want 가", got)
	}

	vectorClass, err := runtime.ensureRaptorHostClass(java, "java/util/Vector")
	check(t, err)
	vector, err := runtime.NewRaptorJavaObject(vectorClass.Holder)
	check(t, err)
	assertRaptorJavaVirtualMethod(t, runtime, java, vectorClass, 0x40,
		"size", "()I")
	assertRaptorJavaVirtualMethod(t, runtime, java, vectorClass, 0x50,
		"indexOf", "(Ljava/lang/Object;)I")
	assertRaptorJavaVirtualMethod(t, runtime, java, vectorClass, 0x60,
		"elementAt", "(I)Ljava/lang/Object;")
	assertRaptorJavaVirtualMethod(t, runtime, java, vectorClass, 0x64,
		"firstElement", "()Ljava/lang/Object;")
	assertRaptorJavaVirtualMethod(t, runtime, java, vectorClass, 0x70,
		"removeElementAt", "(I)V")
	assertRaptorJavaVirtualMethod(t, runtime, java, vectorClass, 0x78,
		"addElement", "(Ljava/lang/Object;)V")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, vector))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, value))
	_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className: "java/util/Vector", Name: "addElement", descriptor: "(Ljava/lang/Object;)V",
	})
	check(t, err)
	entries := java.Host.Vectors[java.lgtToKTF[vector]]
	if len(entries) != 1 || entries[0] != java.lgtToKTF[value] {
		t.Fatalf("Vector script entries = %v, want [%08x]", entries, java.lgtToKTF[value])
	}

	second, err := runtime.NewRaptorJavaString("second")
	check(t, err)
	third, err := runtime.NewRaptorJavaString("third")
	check(t, err)
	for _, element := range []uint32{second, third} {
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, vector))
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, element))
		_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
			className: "java/util/Vector", Name: "addElement", descriptor: "(Ljava/lang/Object;)V",
		})
		check(t, err)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, vector))
	size, err := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className: "java/util/Vector", Name: "size", descriptor: "()I",
	})
	check(t, err)
	if size.Low != 3 {
		t.Fatalf("Vector.size = %d, want 3", size.Low)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, vector))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, third))
	index, err := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className: "java/util/Vector", Name: "indexOf", descriptor: "(Ljava/lang/Object;)I",
	})
	check(t, err)
	if index.Low != 2 {
		t.Fatalf("Vector.indexOf(third) = %d, want 2", index.Low)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, vector))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 1))
	element, err := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className: "java/util/Vector", Name: "elementAt", descriptor: "(I)Ljava/lang/Object;",
	})
	check(t, err)
	if element.Low != second {
		t.Fatalf("Vector.elementAt(1) = 0x%x, want 0x%x", element.Low, second)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, vector))
	first, err := runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className: "java/util/Vector", Name: "firstElement", descriptor: "()Ljava/lang/Object;",
	})
	check(t, err)
	if first.Low != value {
		t.Fatalf("Vector.firstElement = 0x%x, want 0x%x", first.Low, value)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, vector))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 0))
	_, err = runtime.callJavaHostMethod(context.Background(), raptorJavaMethod{
		className: "java/util/Vector", Name: "removeElementAt", descriptor: "(I)V",
	})
	check(t, err)
	entries = java.Host.Vectors[java.lgtToKTF[vector]]
	if len(entries) != 2 || entries[0] != java.lgtToKTF[second] {
		t.Fatalf("Vector entries after removeElementAt(0) = %v, want second then third", entries)
	}
}

func assertRaptorJavaVirtualMethod(
	t *testing.T,
	runtime *Runtime,
	java *JavaRuntime,
	class *raptorJavaClass,
	offset uint32,
	name, descriptor string,
) {
	t.Helper()
	if class == nil || class.vtable == 0 {
		t.Fatal("Raptor Java class has no vtable")
	}
	procedure, err := runtime.Public.ReadU32(class.vtable + offset)
	check(t, err)
	found := false
	for id, method := range java.hostMethods {
		if method.className != class.Name || method.Name != name || method.descriptor != descriptor {
			continue
		}
		found = true
		stub, err := runtime.importStub(raptorImportKey{
			Module: raptorJavaHostModule, Ordinal: id,
		})
		check(t, err)
		if procedure == stub|1 {
			return
		}
	}
	if !found {
		t.Fatalf("%s.%s%s was not registered", class.Name, name, descriptor)
	}
	t.Fatalf("%s slot 0x%02x = 0x%08x, want %s%s", class.Name, offset, procedure, name, descriptor)
}
