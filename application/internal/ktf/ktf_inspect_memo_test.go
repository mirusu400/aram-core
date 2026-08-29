package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// newInspectableJavaClass lays out the smallest class the inspector accepts: a
// five word class whose third word points at a nine word descriptor naming it.
func newInspectableJavaClass(t *testing.T, runtime *Runtime, name string) uint32 {
	t.Helper()
	nameAddress, err := runtime.allocateBytes(append([]byte(name), 0), true)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := runtime.AllocateWords(9)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(descriptor, []uint32{
		nameAddress, 0, 0, 0, 0, 0, 0, 0, 0,
	}); err != nil {
		t.Fatal(err)
	}
	class, err := runtime.AllocateWords(5)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(class, []uint32{
		0, 0, descriptor, 0, 0,
	}); err != nil {
		t.Fatal(err)
	}
	return class
}

// TestKTFInspectMemoIsClosedOutsideResolution pins the window the class memo
// may live in. Outside a bridge call's receiver resolution it must be shut, so
// a class the guest - or a host handler - relinks in place is re-parsed on the
// next inspection, which is what issue #43 turned on.
func TestKTFInspectMemoIsClosedOutsideResolution(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	class := newInspectableJavaClass(t, runtime, "Original")
	if _, err := runtime.InspectJavaClass(class); err != nil {
		t.Fatal(err)
	}
	if runtime.inspectMemo.open {
		t.Fatal("the class memo is open outside a bridge call")
	}

	// Rewrite the class name in place, the way a relink rewrites a method
	// body, and inspect again. With the memo shut the parse has to see it.
	descriptor, err := runtime.ReadU32(class + 8)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := runtime.allocateBytes(append([]byte("Relinked"), 0), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(descriptor, renamed); err != nil {
		t.Fatal(err)
	}
	again, err := runtime.InspectJavaClass(class)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != "Relinked" {
		t.Fatalf("class name after an in-place relink = %q, want %q", again.Name, "Relinked")
	}
}

// TestKTFInspectMemoServesRepeatsInsideResolution proves the window is worth
// having: while it is open, inspecting the same class again answers from the
// memo instead of re-reading the fourteen guest words.
func TestKTFInspectMemoServesRepeatsInsideResolution(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	class := newInspectableJavaClass(t, runtime, "Original")
	// Warm the inspection cache first: its first use resets the memo, which is
	// how a class-generation bump closes an open window.
	if _, err := runtime.InspectJavaClass(class); err != nil {
		t.Fatal(err)
	}

	runtime.inspectMemo.begin()
	defer runtime.inspectMemo.reset()
	if _, err := runtime.InspectJavaClass(class); err != nil {
		t.Fatal(err)
	}
	// A rewrite the memo must not notice, because nothing that can change a
	// class is allowed to run inside the window.
	descriptor, err := runtime.ReadU32(class + 8)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := runtime.allocateBytes(append([]byte("Unseen"), 0), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(descriptor, renamed); err != nil {
		t.Fatal(err)
	}
	again, err := runtime.InspectJavaClass(class)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != "Original" {
		t.Fatalf("memoised class name = %q, want %q", again.Name, "Original")
	}
	runtime.inspectMemo.reset()
	reparsed, err := runtime.InspectJavaClass(class)
	if err != nil {
		t.Fatal(err)
	}
	if reparsed.Name != "Unseen" {
		t.Fatalf("class name after the window closed = %q, want %q", reparsed.Name, "Unseen")
	}
}
