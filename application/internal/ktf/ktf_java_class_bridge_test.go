package ktf

import (
	"bytes"
	"context"
	"fmt"
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

func TestKTFJavaClassBridgeTracksVTableRebuildAfterStateRestore(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	classAddress := ensureClass(t, runtime, "java/lang/StringBuffer")
	methodAddress, err := runtime.resolveJavaMethod(classAddress, "append", "(I)Ljava/lang/StringBuffer;")
	check(t, err)
	class := inspectClass(t, runtime, classAddress)
	instance, err := runtime.NewJavaInstanceForClass(class)
	check(t, err)
	fields := readU32(t, runtime, instance)
	header := readU32(t, runtime, fields)
	bridge := runtime.JvmContext + uint32(int32(header)>>5)
	oldTable := readU32(t, runtime, bridge+12)
	if oldTable != class.VTable {
		t.Fatalf("initial bridge vtable = 0x%08x, want 0x%08x", oldTable, class.VTable)
	}

	var state bytes.Buffer
	check(t, WriteState(runtime, runtime.CPU, true, guest.NewStateWriter(&state)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(state.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	check(t, err)
	started := false
	check(t, RestoreState(runtime, runtime.CPU, saved, &started))
	if len(runtime.javaClassBridges) != 0 {
		t.Fatal("derived bridge cache unexpectedly survived state restore")
	}

	// A late compatibility method can grow the real vtable before another
	// object allocation has reconstructed the derived bridge cache.
	capacity := runtime.javaVTableCapacity[classAddress]
	if capacity >= uint32(^uint16(0)) {
		t.Fatal("synthetic vtable has no room to grow")
	}
	check(t, runtime.installHostJavaVirtualMethodForClass(classAddress, methodAddress, uint16(capacity)))
	newTable := readU32(t, runtime, classAddress+12)
	if newTable == oldTable {
		t.Fatal("vtable rebuild reused the old table")
	}
	if got := readU32(t, runtime, bridge+12); got != newTable {
		t.Fatalf("restored bridge vtable = 0x%08x, want rebuilt table 0x%08x", got, newTable)
	}
}

// A title's compiled class owns its descriptor and uses the compact vtable
// index directly. Host-created instances of that class must keep the same
// header shape as instances allocated by the title's own AOT code (#206).
func TestKTFGuestClassKeepsCompactVTableHeader(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	parent := ensureClass(t, runtime, "java/lang/Object")
	classAddress := defineGuestSubclass(
		t, runtime, "test/GuestCounter", parent, "counter", "I",
	)
	class := inspectClass(t, runtime, classAddress)
	instance, err := runtime.NewJavaInstanceForClass(class)
	check(t, err)

	fields := readU32(t, runtime, instance)
	header := readU32(t, runtime, fields)
	index := runtime.javaVTables[classAddress]
	want := (index * 4) << 5
	if header != want {
		t.Fatalf("guest class header = 0x%08x, want compact index 0x%08x", header, want)
	}
	if table := readU32(t, runtime, runtime.JvmContext+12+(header>>5)); table != class.VTable {
		t.Fatalf("guest class vtable = 0x%08x, want 0x%08x", table, class.VTable)
	}
	if bridge := runtime.javaClassBridges[classAddress]; bridge != 0 {
		t.Fatalf("guest class acquired host bridge 0x%08x", bridge)
	}
}

// An old save can have all 128 packed JVM slots assigned before a title
// constructs another compiled class. Synthesized host classes already use
// bridge headers, so their packed slots can be transferred to the guest.
func TestKTFGuestClassReclaimsBridgedHostSlotAfterRestore(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	hostAddress := ensureClass(t, runtime, "java/lang/Object")
	host := inspectClass(t, runtime, hostAddress)
	hostInstance, err := runtime.NewJavaInstanceForClass(host)
	check(t, err)
	hostFields := readU32(t, runtime, hostInstance)
	hostHeader := readU32(t, runtime, hostFields)
	bridge := runtime.JvmContext + uint32(int32(hostHeader)>>5)
	hostIndex := runtime.javaVTables[hostAddress]
	if bridge == runtime.JvmContext+hostIndex*4 {
		t.Fatal("host object has no class bridge")
	}

	for len(runtime.javaVTables) < 128 {
		classAddress := defineGuestSubclass(t, runtime,
			fmt.Sprintf("test/Registered%d", len(runtime.javaVTables)),
			hostAddress, "value", "I")
		class := inspectClass(t, runtime, classAddress)
		_, err := runtime.ensureJavaVTableIndex(classAddress, class.VTable)
		check(t, err)
	}
	guestAddress := defineGuestSubclass(t, runtime, "test/NewGuest", hostAddress, "value", "I")
	guestClass := inspectClass(t, runtime, guestAddress)

	var state bytes.Buffer
	check(t, WriteState(runtime, runtime.CPU, true, guest.NewStateWriter(&state)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(state.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	check(t, err)
	started := false
	check(t, RestoreState(runtime, runtime.CPU, saved, &started))
	if len(runtime.javaClassBridges) != 0 {
		t.Fatal("derived bridge cache survived restore")
	}

	instance, err := runtime.NewJavaInstanceForClass(guestClass)
	check(t, err)
	if _, retained := runtime.javaVTables[hostAddress]; retained {
		t.Fatal("bridged host class kept its packed slot")
	}
	if got := runtime.javaVTables[guestAddress]; got != hostIndex {
		t.Fatalf("new guest slot = %d, want reclaimed host slot %d", got, hostIndex)
	}
	fields := readU32(t, runtime, instance)
	header := readU32(t, runtime, fields)
	if want := hostIndex * 4 << 5; header != want {
		t.Fatalf("guest header = 0x%08x, want 0x%08x", header, want)
	}
	if got := readU32(t, runtime, runtime.JvmContext+12+hostIndex*4); got != guestClass.VTable {
		t.Fatalf("guest vtable = 0x%08x, want 0x%08x", got, guestClass.VTable)
	}
	hostInstance, err = runtime.NewJavaInstanceForClass(host)
	check(t, err)
	if got := readU32(t, runtime, readU32(t, runtime, hostInstance)); got != hostHeader {
		t.Fatalf("restored host header = 0x%08x, want 0x%08x", got, hostHeader)
	}
	if got := readU32(t, runtime, bridge+12); got != host.VTable {
		t.Fatalf("host bridge vtable = 0x%08x, want 0x%08x", got, host.VTable)
	}
	if got := runtime.javaVTables[guestAddress]; got != hostIndex {
		t.Fatalf("host object stole guest slot %d", got)
	}
}

func TestKTFReclaimedHostClassReceivesLateVirtualMethod(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	classAddress := ensureClass(t, runtime, "java/lang/StringBuffer")
	methodAddress, err := runtime.resolveJavaMethod(
		classAddress, "append", "(I)Ljava/lang/StringBuffer;")
	check(t, err)
	class := inspectClass(t, runtime, classAddress)
	instance, err := runtime.NewJavaInstanceForClass(class)
	check(t, err)
	fields := readU32(t, runtime, instance)
	bridge := runtime.JvmContext + uint32(int32(readU32(t, runtime, fields))>>5)
	oldTable := readU32(t, runtime, bridge+12)
	delete(runtime.javaVTables, classAddress)

	// This compatibility slot is just beyond the currently reserved table.
	slot := uint16(runtime.javaVTableCapacity[classAddress])
	runtime.hostJavaVirtualSlots[methodAddress] = slot
	check(t, runtime.installHostJavaVirtualMethod(methodAddress))
	newTable := readU32(t, runtime, bridge+12)
	if newTable == oldTable {
		t.Fatal("reclaimed host class did not rebuild its bridge vtable")
	}
	if got := readU32(t, runtime, newTable+uint32(slot)*4); got != methodAddress {
		t.Fatalf("late virtual method = 0x%08x, want 0x%08x", got, methodAddress)
	}
	if _, restored := runtime.javaVTables[classAddress]; restored {
		t.Fatal("late method restored an unnecessary packed host slot")
	}
}
