package ktf

import (
	"context"
	"testing"
)

// Compiled text-field subclasses read TextComponent.m_td themselves. Exercise
// that guest-memory view after the host constructor, editing, and keypad input.
func TestKTFLWCTextDataTracksEdits(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	parent := ensureClass(t, runtime, "org/kwis/msp/lwc/TextFieldComponent")
	subclass := inspectClass(t, runtime, defineGuestSubclass(t, runtime,
		"test/EditableName", parent, "selected", "I"))
	instance, err := runtime.NewJavaInstanceForClass(subclass)
	check(t, err)
	check(t, runtime.WriteJavaFieldWord(instance, 0, 3))
	other, err := runtime.NewJavaInstanceForClass(subclass)
	check(t, err)
	state := runtime.lwcComponent(instance)
	offset := ktfHostInstanceFieldOffsets["org/kwis/msp/lwc/TextComponent.m_td[C"]
	buffer := func(receiver uint32) uint32 {
		t.Helper()
		fields := readU32(t, runtime, receiver)
		return readU32(t, runtime, fields+4+offset)
	}
	assertText := func(want string) {
		t.Helper()
		array := buffer(instance)
		length, err := runtime.javaArrayLength(array)
		check(t, err)
		got, err := runtime.readJavaCharArrayRange(array, 0, length)
		check(t, err)
		if got != want {
			t.Fatalf("compiled m_td read = %q, want %q", got, want)
		}
		if own := readU32(t, runtime, readU32(t, runtime, instance)+4); own != 3 {
			t.Fatalf("subclass field overwritten with %d", own)
		}
	}
	call := func(name, descriptor string, args ...uint32) {
		t.Helper()
		registers := make([]uint32, 13)
		registers[1] = instance
		copy(registers[2:], args)
		_, err := runtime.handleLWCMethod(context.Background(),
			"org/kwis/msp/lwc/TextFieldComponent", name, descriptor, registers)
		check(t, err)
	}
	assertText("")
	if buffer(instance) == buffer(other) {
		t.Fatal("text fields share a mutable backing array")
	}
	text, err := runtime.NewJavaString("시장🙂")
	check(t, err)
	call("<init>", "(Ljava/lang/String;I)V", text, 0)
	assertText("시장🙂")
	text, err = runtime.NewJavaString("서울")
	check(t, err)
	call("setString", "(Ljava/lang/String;)V", text)
	assertText("서울")
	inserted, err := runtime.newJavaCharArray("시")
	check(t, err)
	call("insert", "([CIII)V", inserted, 0, 1, 2)
	assertText("서울시")
	call("delete", "(II)V", 2, 1)
	assertText("서울")
	call("setString", "(Ljava/lang/String;)V", 0)
	assertText("")
	for _, key := range []int32{'5', '1'} {
		_, err := runtime.editLWCText(instance, state, ktfLWCKeyPressed, key)
		check(t, err)
	}
	assertText("니")
	length, err := runtime.javaArrayLength(buffer(other))
	check(t, err)
	if length != 0 {
		t.Fatalf("editing one field changed the other's length to %d", length)
	}
}
