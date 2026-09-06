package raptor

import (
	"bytes"
	"testing"
)

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
