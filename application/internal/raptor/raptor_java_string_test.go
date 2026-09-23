package raptor

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorJavaStringToCharArrayVirtualSlot(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)

	// Give the synthetic host class enough linked-vtable extent to include the
	// handset's fixed 0x8c String slot.
	java.flatVirtual = make([]raptorJavaMethod, 3)
	value, err := runtime.NewRaptorJavaString("연결")
	check(t, err)

	class := java.ClassByName["java/lang/String"]
	if class == nil || class.vtable == 0 {
		t.Fatal("java/lang/String has no Raptor vtable")
	}
	procedure, err := public.ReadU32(class.vtable + 0x8c)
	check(t, err)

	var method raptorJavaMethod
	var methodID uint32
	found := false
	for id, candidate := range java.hostMethods {
		if candidate.className == "java/lang/String" &&
			candidate.Name == "toCharArray" && candidate.descriptor == "()[C" {
			method, methodID, found = candidate, id, true
			break
		}
	}
	if !found {
		t.Fatal("String.toCharArray() was not registered")
	}
	stub, err := runtime.importStub(raptorImportKey{
		Module: raptorJavaHostModule, Ordinal: methodID,
	})
	check(t, err)
	if want := stub | 1; procedure != want {
		t.Fatalf("String vtable+0x8c = 0x%08x, want toCharArray stub 0x%08x", procedure, want)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, value))
	result, err := runtime.callJavaHostMethod(context.Background(), method)
	check(t, err)
	if result.Low == 0 {
		t.Fatal("String.toCharArray() returned null")
	}
	body, err := public.ReadU32(result.Low + 8)
	check(t, err)
	length, err := public.ReadU32(body)
	check(t, err)
	if length != 2 {
		t.Fatalf("char[] length = %d, want 2", length)
	}
	encoded := make([]byte, 4)
	check(t, runtime.CPU.ReadMemory(body+4, encoded))
	if got, want := binary.LittleEndian.Uint32(encoded), uint32(0xacb0c5f0); got != want {
		t.Fatalf("char[] UTF-16 = 0x%08x, want 0x%08x", got, want)
	}
}

func TestRaptorJavaStringIndexOfVirtualSlots(t *testing.T) {
	layout := raptorJavaFixedVirtualMethods["java/lang/String"]
	actual := make(map[uint32]string, len(layout))
	for _, method := range layout {
		actual[method.offset] = method.Name + method.descriptor
	}
	for offset, want := range map[uint32]string{
		0x58: "indexOf(I)I",
		0x5c: "indexOf(II)I",
		0x68: "indexOf(Ljava/lang/String;)I",
		0x6c: "indexOf(Ljava/lang/String;I)I",
	} {
		if got := actual[offset]; got != want {
			t.Errorf("String slot 0x%02x = %q, want %q", offset, got, want)
		}
	}
}
