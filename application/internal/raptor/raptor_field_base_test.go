package raptor

import (
	"encoding/binary"
	"testing"
)

// The AOT compiler numbers a class's own instance fields from zero and records
// the whole chain's word count in the class descriptor, while an object here
// has one flat field block. Publishing a declared index unchanged put a
// subclass's first field on top of its superclass's first field: 스파이더맨3
// declares "l : I" at index 0 in class "d" and
// "a : Lorg/kwis/msp/lcdui/Display;" at index 0 in its superclass "a", so one
// word had to be an int and a Display at once - the Display read came back as
// the int 10 and the module started its main class at PC 0.
func TestRaptorFieldOffsetsSeparateASubclassFromItsParent(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)

	// "a" extends a class this runtime models on the host, so the words its
	// own indices start at are only recoverable from its own descriptor: 16
	// words in all, 3 of them its own, so its index 0 is word 13.
	parent := &raptorJavaClass{
		Name:       "a",
		parentName: "org/kwis/msp/lcdui/Card",
		fieldSize:  16,
		fields: []raptorJavaDeclaredField{
			{Name: "a", descriptor: "Lorg/kwis/msp/lcdui/Display;", index: 0},
			{Name: "b", descriptor: "I", index: 1},
			{Name: "c", descriptor: "I", index: 2},
		},
	}
	// "d" extends a class the module declares itself, whose descriptor already
	// counts the whole chain, so its index 0 is word 16.
	child := &raptorJavaClass{
		Name:       "d",
		parentName: "a",
		fieldSize:  20,
		fields: []raptorJavaDeclaredField{
			{Name: "l", descriptor: "I", index: 0},
			{Name: "m", descriptor: "I", index: 1},
			{Name: "n", descriptor: "I", index: 2},
			{Name: "o", descriptor: "I", index: 3},
		},
	}
	java.classOrder = append(java.classOrder, parent, child)
	java.ClassByName[parent.Name] = parent
	java.ClassByName[child.Name] = child

	// The module publishes its field references class by class.
	references := [][2]string{
		{"a", "Lorg/kwis/msp/lcdui/Display;"},
		{"b", "I"},
		{"c", "I"},
		{"l", "I"},
		{"m", "I"},
		{"n", "I"},
		{"o", "I"},
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
		check(t, err)
		typeAddress, err := runtime.allocateJavaCString(reference[1])
		check(t, err)
		base := names + uint32(index)*8
		check(t, public.WriteU32(base, nameAddress))
		check(t, public.WriteU32(base+4, typeAddress))
	}
	java.fieldNames = names
	java.fieldOffsets = offsets
	java.fieldCount = uint32(len(references))

	check(t, runtime.resolveRaptorJavaFieldOffsets(java))
	published := make([]uint16, len(references))
	for index := range references {
		var encoded [2]byte
		check(t, runtime.CPU.ReadMemory(offsets+uint32(index)*2, encoded[:]))
		published[index] = binary.LittleEndian.Uint16(encoded[:])
	}
	want := []uint16{13, 14, 15, 16, 17, 18, 19}
	for index, expected := range want {
		if published[index] != expected {
			t.Fatalf("reference %d (%q %q) resolved to word %d, want %d",
				index, references[index][0], references[index][1],
				published[index], expected)
		}
	}
	// The point of the layout: the two classes' own index 0 are two words.
	if published[0] == published[3] {
		t.Fatalf("%q index 0 and %q index 0 share word %d",
			parent.Name, child.Name, published[0])
	}
}

// A class contributing only a couple of entries sits inside a much larger
// neighbour's block, and both of 스파이더맨3's launch-class fields are named
// "a". Counting how many entries in a fixed window around the reference a
// candidate class declares handed that reference to the big neighbour, which
// resolved the launch class's Display field to a word its eight-word object
// does not have; the run of entries the reference actually sits in decides.
func TestRaptorFieldOffsetsKeepAShortClassBlockIntact(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)

	card := &raptorJavaClass{
		Name:       "a",
		parentName: "org/kwis/msp/lcdui/Card",
		fieldSize:  4,
		fields: []raptorJavaDeclaredField{
			{Name: "a", descriptor: "Lorg/kwis/msp/lcdui/Display;", index: 0},
			{Name: "b", descriptor: "I", index: 1},
			{Name: "c", descriptor: "I", index: 2},
			{Name: "d", descriptor: "I", index: 3},
		},
	}
	launch := &raptorJavaClass{
		Name:       "app/Launch",
		parentName: "org/kwis/msp/lcdui/Jlet",
		fieldSize:  2,
		fields: []raptorJavaDeclaredField{
			{Name: "a", descriptor: "La;", index: 0},
			{Name: "a", descriptor: "Lorg/kwis/msp/lcdui/Display;", index: 1},
		},
	}
	java.classOrder = append(java.classOrder, card, launch)
	java.ClassByName[card.Name] = card
	java.ClassByName[launch.Name] = launch

	// The launch class's two entries follow the card's four.
	references := [][2]string{
		{"b", "I"},
		{"c", "I"},
		{"d", "I"},
		{"a", "Lorg/kwis/msp/lcdui/Display;"},
		{"a", "La;"},
		{"a", "Lorg/kwis/msp/lcdui/Display;"},
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
		check(t, err)
		typeAddress, err := runtime.allocateJavaCString(reference[1])
		check(t, err)
		base := names + uint32(index)*8
		check(t, public.WriteU32(base, nameAddress))
		check(t, public.WriteU32(base+4, typeAddress))
	}
	java.fieldNames = names
	java.fieldOffsets = offsets
	java.fieldCount = uint32(len(references))

	check(t, runtime.resolveRaptorJavaFieldOffsets(java))
	want := []uint16{1, 2, 3, 0, 0, 1}
	for index, expected := range want {
		var encoded [2]byte
		check(t, runtime.CPU.ReadMemory(offsets+uint32(index)*2, encoded[:]))
		if slot := binary.LittleEndian.Uint16(encoded[:]); slot != expected {
			t.Fatalf("reference %d (%q %q) resolved to word %d, want %d",
				index, references[index][0], references[index][1],
				slot, expected)
		}
	}
}
