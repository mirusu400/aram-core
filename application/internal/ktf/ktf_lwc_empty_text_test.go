package ktf

import (
	"context"
	"testing"
)

// A null TextComponent input means empty character data, not a null String.
// Compiled callers immediately invoke String methods on getString's result.
func TestKTFLWCEmptyTextReturnsString(t *testing.T) {
	for _, className := range []string{
		"org/kwis/msp/lwc/TextComponent",
		"org/kwis/msp/lwc/TextFieldComponent",
		"org/kwis/msp/lwc/TextBoxComponent",
	} {
		t.Run(className, func(t *testing.T) {
			r := newTestRuntime(t)
			r.JvmContext = allocWords(t, r, 3+128)
			parent := ensureClass(t, r, className)
			subclass := inspectClass(t, r, defineGuestSubclass(t, r,
				"test/EmptyText", parent, "selected", "I"))
			instance, err := r.NewJavaInstanceForClass(subclass)
			check(t, err)
			call := func(name, descriptor string, args ...uint32) uint32 {
				t.Helper()
				registers := make([]uint32, 13)
				registers[1] = instance
				copy(registers[2:], args)
				value, err := r.handleLWCMethod(context.Background(), className, name, descriptor, registers)
				check(t, err)
				return value
			}
			assertEmpty := func() {
				t.Helper()
				value := call("getString", "()Ljava/lang/String;")
				if value == 0 {
					t.Fatal("empty text returned a null String")
				}
				if got := r.javaStringValue(value); got != "" {
					t.Fatalf("empty text = %q", got)
				}
			}
			assertEmpty()
			if className != "org/kwis/msp/lwc/TextComponent" {
				call("<init>", "(Ljava/lang/String;I)V", 0, 0)
				assertEmpty()
			}
			text, err := r.NewJavaString("name")
			check(t, err)
			call("setString", "(Ljava/lang/String;)V", text)
			if got := call("getString", "()Ljava/lang/String;"); got != text {
				t.Fatal("nonempty text was replaced")
			}
			call("setString", "(Ljava/lang/String;)V", 0)
			assertEmpty()
		})
	}
}
