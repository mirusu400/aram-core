package raptor

import (
	"bytes"
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// TestRaptorNewArraySelectsItsLengthRegisterFromTheComponentOperand pins both
// module-100 ordinal-16 forms. The preceding ordinal 14 leaves an opaque
// reference-array type result in r0, an array caller reloads r1 with a
// component/class value, and its scalar length remains in r2. Primitive and
// null-reference arrays instead use the compact form with their count in r1.
func TestRaptorNewArraySelectsItsLengthRegisterFromTheComponentOperand(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}

	arrayType, err := public.Heap.Allocate(1, true)
	check(t, err)
	component, err := public.Heap.Allocate(1, true)
	check(t, err)
	if arrayType == 0 || component == 0 {
		t.Fatalf("allocate ABI operands = 0x%08x, 0x%08x", arrayType, component)
	}
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, arrayType))
	check(t, public.CPU.WriteRegister(cpu.RegisterR1, component))
	check(t, public.CPU.WriteRegister(cpu.RegisterR2, 3))
	check(t, runtime.dispatchImport(
		context.Background(),
		raptorImportKey{Module: 100, Ordinal: 16},
	))

	array, err := public.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	body, err := public.ReadU32(array + 8)
	check(t, err)
	length, err := public.ReadU32(body)
	check(t, err)
	if length != 3 {
		t.Fatalf("array length = %d, want r2 value 3", length)
	}
	if got := public.Stats.LastAPI; got != "RAPTOR.Java.newArray" {
		t.Fatalf("last API = %q, want RAPTOR.Java.newArray", got)
	}

	// The primitive form must keep using r1: r2 is unrelated caller state, not
	// an allocation count.
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, 'B'))
	check(t, public.CPU.WriteRegister(cpu.RegisterR1, 5))
	check(t, public.CPU.WriteRegister(cpu.RegisterR2, 0x01403108))
	check(t, runtime.dispatchImport(
		context.Background(),
		raptorImportKey{Module: 100, Ordinal: 16},
	))
	primitive, err := public.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	primitiveBody, err := public.ReadU32(primitive + 8)
	check(t, err)
	primitiveLength, err := public.ReadU32(primitiveBody)
	check(t, err)
	if primitiveLength != 5 {
		t.Fatalf("primitive array length = %d, want r1 value 5", primitiveLength)
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	_, count, elementSize, isPrimitive, ok := java.Host.ArrayShape(java.lgtToKTF[primitive])
	if !ok || !isPrimitive || count != 5 || elementSize != 1 {
		t.Fatalf("primitive mirror shape = count %d element %d primitive %t ok %t",
			count, elementSize, isPrimitive, ok)
	}

	// A direct reference array has no ordinal-14 token. Its r2 is unrelated
	// caller state and must not be treated as a count.
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, 0))
	check(t, public.CPU.WriteRegister(cpu.RegisterR1, 8))
	check(t, public.CPU.WriteRegister(cpu.RegisterR2, component))
	check(t, runtime.dispatchImport(
		context.Background(),
		raptorImportKey{Module: 100, Ordinal: 16},
	))
	direct, err := public.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	directBody, err := public.ReadU32(direct + 8)
	check(t, err)
	directLength, err := public.ReadU32(directBody)
	check(t, err)
	if directLength != 8 {
		t.Fatalf("direct reference array length = %d, want r1 value 8", directLength)
	}

	// An ordinal-14 result in r0 is not enough to select the extended form:
	// only a component pointer in r1 makes r2 a count.
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, arrayType))
	check(t, public.CPU.WriteRegister(cpu.RegisterR1, 14))
	check(t, public.CPU.WriteRegister(cpu.RegisterR2, component))
	check(t, runtime.dispatchImport(
		context.Background(),
		raptorImportKey{Module: 100, Ordinal: 16},
	))
	allocatedDirect, err := public.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	allocatedDirectBody, err := public.ReadU32(allocatedDirect + 8)
	check(t, err)
	allocatedDirectLength, err := public.ReadU32(allocatedDirectBody)
	check(t, err)
	if allocatedDirectLength != 14 {
		t.Fatalf("allocated direct array length = %d, want r1 value 14", allocatedDirectLength)
	}
}

// TestSyncRaptorArrayCopiesBothWays pins the array bridge. A Raptor array's
// elements live twice - in the body the AOT reads and in the KTF mirror the
// shared Java host writes - and neither side saw the other until a host call
// copied them across. 현영맞고2006 read each sprite atlas with
// InputStream.read(byte[]), parsed a sprite count out of the buffer, and got
// zero every time because only the mirror was ever filled (issue #79).
func TestSyncRaptorArrayCopiesBothWays(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)

	array, err := runtime.newRaptorJavaArray('B', 6)
	check(t, err)
	mirror := java.lgtToKTF[array]
	if mirror == 0 {
		t.Fatal("the array has no KTF mirror")
	}
	body, err := public.ReadU32(array + 8)
	check(t, err)
	mirrorBody, count, element, primitive, ok := java.Host.ArrayShape(mirror)
	if !ok || !primitive || count != 6 || element != 1 {
		t.Fatalf("mirror shape = count %d element %d primitive %t ok %t", count, element, primitive, ok)
	}
	if java.primitiveArrays[mirror] != 1 {
		t.Fatalf("byte array element width = %d, want 1", java.primitiveArrays[mirror])
	}

	// Guest -> mirror: what the AOT stored has to reach a host method that
	// reads the array (new String(byte[]), DataBase.insertRecord).
	written := []byte{1, 2, 3, 4, 5, 6}
	check(t, public.CPU.WriteMemory(body+4, written))
	runtime.syncRaptorArrayArguments(java, []uint32{mirror}, true)
	seen := make([]byte, len(written))
	check(t, public.CPU.ReadMemory(mirrorBody, seen))
	if !bytes.Equal(seen, written) {
		t.Fatalf("mirror = %v, want %v", seen, written)
	}

	// Mirror -> guest: what a host method filled has to reach the AOT.
	filled := []byte{9, 8, 7, 6, 5, 4}
	check(t, public.CPU.WriteMemory(mirrorBody, filled))
	runtime.syncRaptorArrayArguments(java, []uint32{mirror}, false)
	check(t, public.CPU.ReadMemory(body+4, seen))
	if !bytes.Equal(seen, filled) {
		t.Fatalf("guest body = %v, want %v", seen, filled)
	}

	// A reference array is left alone: its elements are heap addresses that
	// name different objects on the two sides, and stores are mirrored one at
	// a time by storeRaptorJavaArray.
	references, err := runtime.newRaptorJavaArray(0, 2)
	check(t, err)
	referenceMirror := java.lgtToKTF[references]
	if width := java.primitiveArrays[referenceMirror]; width != 0 {
		t.Fatalf("a reference array was indexed as primitive (width %d)", width)
	}
	referenceBody, err := public.ReadU32(references + 8)
	check(t, err)
	check(t, public.WriteU32(referenceBody+4, 0xfeedface))
	runtime.syncRaptorArrayArguments(java, []uint32{referenceMirror}, true)
	mirrorWords, err := java.Host.ReadWords(referenceMirror, 2)
	check(t, err)
	if got, _ := public.ReadU32(mirrorWords[0] + 8); got == 0xfeedface {
		t.Fatal("a reference element was copied into the mirror verbatim")
	}

	// A word that is not a mapped array is ignored rather than faulting.
	runtime.syncRaptorArrayArguments(java, []uint32{0, 0x12345678}, true)
}

// TestWrapRaptorJavaObjectGivesArraysABody pins the other direction: an array a
// host method returns (DataBase.selectRecord, String.getBytes) reaches the guest
// as a Raptor array, so it needs its length word and elements, not the one-word
// field block a plain object gets.
func TestWrapRaptorJavaObjectGivesArraysABody(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)

	mirror, err := java.Host.NewJavaArray("[B", 5, 1)
	check(t, err)
	mirrorBody, _, _, _, ok := java.Host.ArrayShape(mirror)
	if !ok {
		t.Fatal("the host array is not reported as an array")
	}
	stored := []byte{0x10, 0x20, 0x30, 0x40, 0x50}
	check(t, public.CPU.WriteMemory(mirrorBody, stored))

	instance, err := runtime.wrapRaptorJavaObject(java, mirror)
	check(t, err)
	body, err := public.ReadU32(instance + 8)
	check(t, err)
	length, err := public.ReadU32(body)
	check(t, err)
	if length != 5 {
		t.Fatalf("wrapped array length = %d, want 5", length)
	}
	seen := make([]byte, len(stored))
	check(t, public.CPU.ReadMemory(body+4, seen))
	if !bytes.Equal(seen, stored) {
		t.Fatalf("wrapped array = %v, want %v", seen, stored)
	}
}
