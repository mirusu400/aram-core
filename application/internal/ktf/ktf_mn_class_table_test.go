package ktf

import (
	"context"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestKTFRegisterMNClassesUsesSparsePointerTable(t *testing.T) {
	runtime := newMNTestRuntime(t)
	const (
		moduleTable = ImageBase + 0x100
		slots       = ImageBase + 0x200
		child       = ImageBase + 0x300
		parent      = ImageBase + 0x500
		imports     = ImageBase + 0x700
	)
	// The table is sparse, not a contiguous class array or a linked list.
	// Put the child before its parent and the parent beyond the live count.
	childField, _ := defineMNLayoutClass(t, runtime, child, ImageBase+0x380,
		"test/Child", 1, 8, nil)
	defineMNLayoutClass(t, runtime, parent, ImageBase+0x580,
		"test/Parent", 0, 4, nil)
	check(t, runtime.writeWords(moduleTable, []uint32{slots, 2, 8}))
	check(t, runtime.writeWords(slots, []uint32{0, child, 0, 0, 0, 0, 0, parent}))
	check(t, runtime.WriteU32(imports, readU32(t, runtime, ImageBase+0x580)))
	check(t, runtime.registerMNClasses(moduleTable, imports))
	for name, address := range map[string]uint32{"test/Child": child, "test/Parent": parent} {
		if got := runtime.JavaClasses[name]; got != address {
			t.Fatalf("registered %s = 0x%08x, want 0x%08x", name, got, address)
		}
	}
	if got := inspectClass(t, runtime, child).Parent; got != parent {
		t.Fatalf("linked parent = 0x%08x, want module class 0x%08x", got, parent)
	}
	if got := inspectClass(t, runtime, child).FieldSize; got != 12 {
		t.Fatalf("linked child field size = %d, want 12", got)
	}
	if got := readU32(t, runtime, childField+12); got != 4 {
		t.Fatalf("linked child field offset = %d, want 4", got)
	}
}

func TestKTFRegisterMNClassesValidatesSparseTable(t *testing.T) {
	const (
		moduleTable = ImageBase + 0x100
		slots       = ImageBase + 0x200
		object      = ImageBase + 0x300
		imageEnd    = ImageBase + 0x4000
	)
	for _, test := range []struct {
		name   string
		module uint32
		header [3]uint32
		slots  []uint32
		want   string
	}{
		{"empty", moduleTable, [3]uint32{}, nil, "registered no classes"},
		{"short header", imageEnd - 8, [3]uint32{}, nil, "module table"},
		{"count above capacity", moduleTable, [3]uint32{slots, 2, 1}, nil, "invalid KTF module class table"},
		{"class limit", moduleTable, [3]uint32{slots, mnMaxClasses + 1, mnMaxClassSlots}, nil, "invalid KTF module class table"},
		{"slot limit", moduleTable, [3]uint32{slots, 1, mnMaxClassSlots + 1}, nil, "invalid KTF module class table"},
		{"short slot array", moduleTable, [3]uint32{imageEnd - 4, 1, 2}, nil, "class slots"},
		{"null slots with live count", moduleTable, [3]uint32{slots, 1, 2}, []uint32{0, 0}, "does not match"},
		{"too few entries", moduleTable, [3]uint32{slots, 2, 2}, []uint32{object, 0}, "does not match"},
		{"too many entries", moduleTable, [3]uint32{slots, 1, 2}, []uint32{object, object + 0x100}, "does not match"},
		{"duplicate", moduleTable, [3]uint32{slots, 2, 2}, []uint32{object, object}, "duplicate KTF module class object"},
		{"short class object", moduleTable, [3]uint32{slots, 1, 2}, []uint32{0, imageEnd - 16}, "outside client image"},
		{"unreadable descriptor", moduleTable, [3]uint32{slots, 1, 2}, []uint32{0, ImageBase + 0x600}, "inspect KTF module class slot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := newMNTestRuntime(t)
			defineMNLayoutClass(t, runtime, object, ImageBase+0x380, "test/One", 0, 0, nil)
			defineMNLayoutClass(t, runtime, object+0x100, ImageBase+0x480, "test/Two", 0, 0, nil)
			check(t, runtime.writeWords(moduleTable, test.header[:]))
			check(t, runtime.writeWords(slots, test.slots))
			err := runtime.registerMNClasses(test.module, 0)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("registerMNClasses error = %v, want %q", err, test.want)
			}
			if runtime.JavaClasses["test/One"] != 0 || runtime.JavaClasses["test/Two"] != 0 {
				t.Fatal("invalid table published a partial module registry")
			}
		})
	}
}

func TestKTFRegisterMNClassObjectCanEndAtImageBoundary(t *testing.T) {
	runtime := newMNTestRuntime(t)
	const (
		moduleTable = ImageBase + 0x100
		slots       = ImageBase + 0x200
		object      = ImageBase + 0x4000 - 20
	)
	defineMNLayoutClass(t, runtime, object, ImageBase+0x300, "test/Last", 0, 0, nil)
	check(t, runtime.writeWords(moduleTable, []uint32{slots, 1, 1}))
	check(t, runtime.WriteU32(slots, object))
	check(t, runtime.registerMNClasses(moduleTable, 0))
	if got := runtime.JavaClasses["test/Last"]; got != object {
		t.Fatalf("registered last object = 0x%08x, want 0x%08x", got, object)
	}
}

func TestKTFMNFieldSlotReturnsMetadataNotClassOrValue(t *testing.T) {
	runtime := newMNTestRuntime(t)
	class := ensureClass(t, runtime, "org/kwis/msp/lcdui/Font")
	iface, err := runtime.ensureMNInterface()
	check(t, err)
	slot := readU32(t, runtime, iface+21*4)
	host, ok := runtime.hostCalls[slot&^1]
	if !ok || host.name != "mn.resolve_field" {
		t.Fatalf("MN slot 21 = 0x%08x/%q", slot, host.name)
	}
	contextWord := allocWords(t, runtime, 1)
	check(t, runtime.WriteU32(contextWord, 0x1234))
	for _, test := range []struct {
		name string
		want uint32
	}{
		{"FACE_SYSTEM", 0},
		{"STYLE_PLAIN", 0},
		{"SIZE_SMALL", JavaFontSizeSmall},
	} {
		member, err := runtime.allocateBytes(append([]byte{0x55}, []byte("I+"+test.name)...), true)
		check(t, err)
		var previous uint32
		for attempt := 0; attempt < 2; attempt++ {
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, class))
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, member))
			check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, contextWord))
			field, err := host.handler(context.Background(), runtime)
			check(t, err)
			if field == 0 || field == class || (previous != 0 && field != previous) {
				t.Fatalf("%s field = 0x%08x, class = 0x%08x, previous = 0x%08x", test.name, field, class, previous)
			}
			words := readWords(t, runtime, field, 4)
			if words[0]&8 == 0 || words[1] != class || words[3] != test.want {
				t.Fatalf("%s field = %08x, want static declaration and value %d", test.name, words, test.want)
			}
			previous = field
		}
	}
	if got := readU32(t, runtime, contextWord); got != 0x1234 {
		t.Fatalf("field lookup modified its caller context: 0x%08x", got)
	}
}
