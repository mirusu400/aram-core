package ktf

import (
	"bytes"
	"context"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// A KTF AOT object header is used through two ABI views. Virtual calls decode
// it relative to JvmContext and load class+12 as a vtable. The inlined type
// helper decodes the same address as a class, then follows
// class+8 -> descriptor+8 -> parent. Keeping a bare vtable index in the header
// satisfies only the first view: class+8 aliases another packed vtable slot.
//
// This test pins the bridge shared by both views and makes collection and a
// state round trip part of the contract. The header is a compact reference,
// so the heap scanner cannot discover the bridge from the object by treating
// its words as ordinary pointers; the persisted vtable-to-class registry is
// therefore also its strong host root and restore marker.
func TestKTFJavaClassBridgeSurvivesGCAndCachedNativeDispatch(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	objectAddress := ensureClass(t, runtime, "java/lang/Object")
	classAddress := ensureClass(t, runtime, "java/lang/StringBuffer")
	methodAddress, err := runtime.resolveJavaMethod(
		classAddress,
		"append",
		"(I)Ljava/lang/StringBuffer;",
	)
	check(t, err)
	class := inspectClass(t, runtime, classAddress)
	method, err := runtime.InspectJavaMethod(methodAddress)
	check(t, err)
	if method.Body == 0 || method.NativeBody != method.Body {
		t.Fatalf("StringBuffer.append(I) bridge slots = %+v", method)
	}

	instance, err := runtime.NewJavaInstanceForClass(class)
	check(t, err)
	fields := readU32(t, runtime, instance)
	header := readU32(t, runtime, fields)
	bridge := runtime.JvmContext + uint32(int32(header)>>5)
	if bridge == runtime.JvmContext+uint32(runtime.javaVTables[classAddress])*4 {
		t.Fatal("object header still decodes to a packed JVM vtable slot")
	}
	if got := runtime.javaClassBridges[classAddress]; got != bridge {
		t.Fatalf("class bridge map = 0x%08x, want 0x%08x", got, bridge)
	}

	nextClass := func(address uint32) uint32 {
		t.Helper()
		descriptor := readU32(t, runtime, address+8)
		return readU32(t, runtime, descriptor+8)
	}
	if got := nextClass(bridge); got != classAddress {
		t.Fatalf("bridge ancestry first hop = 0x%08x, want class 0x%08x", got, classAddress)
	}
	if got := nextClass(classAddress); got != objectAddress {
		t.Fatalf("class ancestry second hop = 0x%08x, want Object 0x%08x", got, objectAddress)
	}
	vtable := readU32(t, runtime, bridge+12)
	if vtable != class.VTable {
		t.Fatalf("bridge vtable = 0x%08x, want 0x%08x", vtable, class.VTable)
	}
	if got := readU32(t, runtime, vtable+uint32(method.VTableIndex)*4); got != methodAddress {
		t.Fatalf("cached virtual method = 0x%08x, want 0x%08x", got, methodAddress)
	}

	// The instance is the only ordinary guest root. Its compact header is not
	// a raw bridge pointer, so this specifically exercises the host root kept
	// in javaVTableClasses.
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR8, instance))
	runtime.stringBuffers[instance] = "score:"
	runtime.CollectJavaHeapForTest()
	if _, ok := runtime.Heap.Root().Allocations[bridge]; !ok {
		t.Fatal("class bridge was collected")
	}
	if got := nextClass(bridge); got != classAddress {
		t.Fatalf("bridge ancestry after collection = 0x%08x", got)
	}

	var state bytes.Buffer
	check(t, WriteState(runtime, runtime.CPU, true, guest.NewStateWriter(&state)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(state.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	check(t, err)
	started := false
	check(t, RestoreState(runtime, runtime.CPU, saved, &started))
	if len(runtime.javaClassBridges) != 0 {
		t.Fatal("derived class bridge cache was serialized directly")
	}
	class = inspectClass(t, runtime, classAddress)
	restoredHeader, err := runtime.javaObjectClassHeader(
		class,
		runtime.javaVTables[classAddress],
	)
	check(t, err)
	if restoredHeader != header || runtime.javaClassBridges[classAddress] != bridge {
		t.Fatalf(
			"restored bridge header/map = %08x/0x%08x, want %08x/0x%08x",
			restoredHeader,
			runtime.javaClassBridges[classAddress],
			header,
			bridge,
		)
	}
	runtime.CollectJavaHeapForTest()
	if _, ok := runtime.Heap.Root().Allocations[bridge]; !ok {
		t.Fatal("restored class bridge was collected")
	}

	// Match issue #172's cached-native path: take +8 from the method reached
	// through the bridge vtable and dispatch it with no tracked method name.
	methodAddress = readU32(t, runtime,
		readU32(t, runtime, bridge+12)+uint32(method.VTableIndex)*4)
	method, err = runtime.InspectJavaMethod(methodAddress)
	check(t, err)
	parameters := allocWords(t, runtime, 4)
	check(t, runtime.writeWords(parameters, []uint32{instance, 5, 0, 0}))
	runtime.LastJavaMethod = ""
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, method.NativeBody))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, parameters))
	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if got := runtime.stringBuffers[instance]; got != "score:5" {
		t.Fatalf("cached native StringBuffer = %q, want %q", got, "score:5")
	}
}
