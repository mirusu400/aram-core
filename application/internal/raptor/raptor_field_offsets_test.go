package raptor

import (
	"encoding/binary"
	"testing"
)

// A field reference in the AOT tables carries a name and a descriptor but no
// class, and an obfuscated module reuses one-letter names freely. 배틀몬스터
// declares ("e", "Z") in both its Jlet subclass "a" (index 1) and in class "j"
// (index 2); taking the first class that declared the pair made the Jlet's
// "screen" and "waiting" references resolve to the *same* slot, so the poster
// wrote the screen and then cleared it with the flag in its very next store,
// and the worker woke to a null screen and threw (issue #151). The table is
// laid out class by class, so the neighbours decide which class an entry
// belongs to.
func TestRaptorFieldOffsetsPickTheClassTheNeighboursAgreeOn(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	if err != nil {
		t.Fatal(err)
	}

	// "j" is registered first and shares ("e", "Z") with the Jlet subclass.
	decoy := &raptorJavaClass{
		Name: "j",
		fields: []raptorJavaDeclaredField{
			{Name: "e", descriptor: "Z", index: 2},
		},
	}
	jlet := &raptorJavaClass{
		Name: "a",
		fields: []raptorJavaDeclaredField{
			{Name: "a", descriptor: "Lorg/kwis/msp/lcdui/Display;", index: 0},
			{Name: "e", descriptor: "Z", index: 1},
			{Name: "v", descriptor: "Lo;", index: 2},
			{Name: "w", descriptor: "I", index: 3},
		},
	}
	java.classOrder = append(java.classOrder, decoy, jlet)
	java.ClassByName[decoy.Name] = decoy
	java.ClassByName[jlet.Name] = jlet

	// One run of the Jlet's own references, the shape the module publishes.
	references := [][2]string{
		{"w", "I"},
		{"v", "Lo;"},
		{"a", "Lorg/kwis/msp/lcdui/Display;"},
		{"e", "Z"},
	}
	names, err := public.Heap.Allocate(uint32(len(references))*8, true)
	if err != nil || names == 0 {
		t.Fatalf("allocate field names = 0x%08x, %v", names, err)
	}
	offsets, err := public.Heap.Allocate(uint32(len(references))*2, true)
	if err != nil || offsets == 0 {
		t.Fatalf("allocate field offsets = 0x%08x, %v", offsets, err)
	}
	for index, reference := range references {
		nameAddress, err := runtime.allocateJavaCString(reference[0])
		if err != nil {
			t.Fatal(err)
		}
		typeAddress, err := runtime.allocateJavaCString(reference[1])
		if err != nil {
			t.Fatal(err)
		}
		base := names + uint32(index)*8
		if err := public.WriteU32(base, nameAddress); err != nil {
			t.Fatal(err)
		}
		if err := public.WriteU32(base+4, typeAddress); err != nil {
			t.Fatal(err)
		}
	}
	java.fieldNames = names
	java.fieldOffsets = offsets
	java.fieldCount = uint32(len(references))

	if err := runtime.resolveRaptorJavaFieldOffsets(java); err != nil {
		t.Fatal(err)
	}
	want := []uint16{3, 2, 0, 1}
	for index, expected := range want {
		var encoded [2]byte
		if err := runtime.CPU.ReadMemory(
			offsets+uint32(index)*2,
			encoded[:],
		); err != nil {
			t.Fatal(err)
		}
		if slot := binary.LittleEndian.Uint16(encoded[:]); slot != expected {
			t.Fatalf("reference %d (%q %q) resolved to slot %d, want %d",
				index, references[index][0], references[index][1],
				slot, expected)
		}
	}
}
