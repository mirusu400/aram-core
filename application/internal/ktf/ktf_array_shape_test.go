package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// TestArrayShapeDescribesArrays pins the accessor the Raptor bridge copies
// array elements through. Its second body is the only thing an AOT-compiled
// title ever reads, so a wrong element size or count silently corrupts a
// resource the title decodes by hand (issue #79).
func TestArrayShapeDescribesArrays(t *testing.T) {
	runtime, err := NewRuntimeForProfile(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	}, nil, ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	for _, want := range []struct {
		className string
		count     uint32
		element   uint32
		primitive bool
	}{
		{"[B", 7, 1, true},
		{"[C", 5, 2, true},
		{"[I", 3, 4, true},
		{"[Ljava/lang/Object;", 4, 4, false},
	} {
		instance, err := runtime.NewJavaArray(want.className, want.count, want.element)
		if err != nil {
			t.Fatalf("%s: %v", want.className, err)
		}
		body, count, element, primitive, ok := runtime.ArrayShape(instance)
		if !ok {
			t.Fatalf("%s is not reported as an array", want.className)
		}
		if count != want.count || element != want.element || primitive != want.primitive {
			t.Fatalf(
				"%s shape = count %d element %d primitive %t, want %d/%d/%t",
				want.className, count, element, primitive,
				want.count, want.element, want.primitive,
			)
		}
		// Elements start after the array header, and writing the last one must
		// stay inside the allocation the array was made with.
		words, err := runtime.ReadWords(instance, 2)
		if err != nil {
			t.Fatal(err)
		}
		if body != words[0]+8 {
			t.Fatalf("%s body = 0x%08x, want 0x%08x", want.className, body, words[0]+8)
		}
	}

	if _, _, _, _, ok := runtime.ArrayShape(0); ok {
		t.Fatal("a null instance was reported as an array")
	}
	instance, err := runtime.newJavaInstance("java/lang/Object", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, ok := runtime.ArrayShape(instance); ok {
		t.Fatal("a plain object was reported as an array")
	}
}
