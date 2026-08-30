package raptor

import (
	"encoding/binary"
	"testing"
)

// TestRaptorFlatVirtualSlotsClearEveryFixedSlot pins the invariant that made
// 현영맞고2006 a black screen: a linked (flat) virtual method must never land on
// a slot the fixed pass writes afterwards. Publishing offset = index*2 put flat
// entry 1 on java/lang/Object's hashCode slot at byte 0x0c, so every call to
// that entry - Graphics.drawString, which the title draws its whole screen
// with - dispatched to hashCode and drew nothing (issue #79).
func TestRaptorFlatVirtualSlotsClearEveryFixedSlot(t *testing.T) {
	highest := uint32(0)
	for class, methods := range raptorJavaFixedVirtualMethods {
		for _, method := range methods {
			if method.offset > highest {
				highest = method.offset
			}
			for index := uint32(0); index < 64; index++ {
				if slot := raptorJavaFlatVirtualSlot(index); slot == method.offset {
					t.Fatalf(
						"flat entry %d occupies %s's fixed slot 0x%02x",
						index,
						class,
						method.offset,
					)
				}
			}
		}
	}
	if first := raptorJavaFlatVirtualSlot(0); first <= highest {
		t.Fatalf(
			"first flat slot 0x%02x is not past the highest fixed slot 0x%02x",
			first,
			highest,
		)
	}
}

// TestResolveRaptorJavaFieldOffsetsSeesLateClasses is the other half of the
// same report. 현영맞고2006 registers nine field-less helper classes, links, and
// only then loads the Card subclass that owns its fields, so resolving the
// field-offset table once at link time published a zero for every field and
// every getfield in the module read slot zero.
func TestResolveRaptorJavaFieldOffsetsSeesLateClasses(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java := &JavaRuntime{}

	name, err := runtime.allocateJavaCString("g")
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := runtime.allocateJavaCString("Lorg/kwis/msp/lcdui/Graphics;")
	if err != nil {
		t.Fatal(err)
	}
	names, err := public.Heap.Allocate(8, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := public.WriteU32(names, name); err != nil {
		t.Fatal(err)
	}
	if err := public.WriteU32(names+4, descriptor); err != nil {
		t.Fatal(err)
	}
	offsets, err := public.Heap.Allocate(2, true)
	if err != nil {
		t.Fatal(err)
	}
	java.fieldNames = names
	java.fieldOffsets = offsets
	java.fieldCount = 1

	// Resolved with no class registered: the field has no known index yet.
	if err := runtime.resolveRaptorJavaFieldOffsets(java); err != nil {
		t.Fatal(err)
	}
	if got := readOffset(t, runtime, offsets); got != 0 {
		t.Fatalf("field offset before the owning class = %d, want 0", got)
	}

	// The owning class arrives afterwards, the way a module that links before
	// it loads its main class does.
	java.classOrder = []*raptorJavaClass{{
		Name: "Hcvs",
		fields: []raptorJavaDeclaredField{{
			Name:       "g",
			descriptor: "Lorg/kwis/msp/lcdui/Graphics;",
			index:      142,
		}},
	}}
	if err := runtime.resolveRaptorJavaFieldOffsets(java); err != nil {
		t.Fatal(err)
	}
	if got := readOffset(t, runtime, offsets); got != 142 {
		t.Fatalf("field offset after the owning class = %d, want 142", got)
	}
}

func readOffset(t *testing.T, runtime *Runtime, address uint32) uint16 {
	t.Helper()
	var encoded [2]byte
	if err := runtime.CPU.ReadMemory(address, encoded[:]); err != nil {
		t.Fatal(err)
	}
	return binary.LittleEndian.Uint16(encoded[:])
}
