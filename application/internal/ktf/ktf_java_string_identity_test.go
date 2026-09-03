package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// java.lang.String is immutable, and the JDK returns the receiver from every
// method that would answer an identical string: trim() with nothing to trim,
// substring(0), toLowerCase() on a string that is already lower case. KTF Java
// allocated a fresh guest String for each of them instead, and the KTF heap has
// no generational collector to make that cheap: 리얼사커2007 calls trim() in its
// own loop and allocated 2.6 million tiny blocks before the 32 MiB heap was
// gone (issue #131).
func TestKTFStringMethodsReturnTheReceiverWhenUnchanged(t *testing.T) {
	runtime := newCardOriginRuntime(t)
	instance, err := runtime.NewJavaString("abc")
	if err != nil {
		t.Fatal(err)
	}
	call := func(name, descriptor string, args ...uint32) uint32 {
		t.Helper()
		if err := runtime.CPU.WriteRegister(cpu.RegisterR1, instance); err != nil {
			t.Fatal(err)
		}
		for index, value := range args {
			if err := runtime.CPU.WriteRegister(
				cpu.RegisterR2+uint32(index),
				value,
			); err != nil {
				t.Fatal(err)
			}
		}
		result, err := runtime.handleStringMethod(name, descriptor)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	empty, err := runtime.NewJavaString("")
	if err != nil {
		t.Fatal(err)
	}
	for _, unchanged := range []struct {
		name       string
		descriptor string
		args       []uint32
	}{
		{"trim", "()Ljava/lang/String;", nil},
		{"substring", "(I)Ljava/lang/String;", []uint32{0}},
		{"substring", "(II)Ljava/lang/String;", []uint32{0, 3}},
		{"toLowerCase", "()Ljava/lang/String;", nil},
		{"concat", "(Ljava/lang/String;)Ljava/lang/String;", []uint32{empty}},
		{"replace", "(CC)Ljava/lang/String;", []uint32{'z', 'y'}},
	} {
		if got := call(unchanged.name, unchanged.descriptor, unchanged.args...); got != instance {
			t.Fatalf(
				"%s%s = 0x%08x, want the receiver 0x%08x",
				unchanged.name,
				unchanged.descriptor,
				got,
				instance,
			)
		}
	}
	// A method that really does change the string still answers a new one.
	if got := call("toUpperCase", "()Ljava/lang/String;"); got == instance {
		t.Fatal("toUpperCase returned the receiver for a lower-case string")
	} else if value := runtime.javaStringValue(got); value != "ABC" {
		t.Fatalf("toUpperCase = %q, want %q", value, "ABC")
	}
	if got := call("substring", "(I)Ljava/lang/String;", 1); got == instance {
		t.Fatal("substring(1) returned the receiver")
	}
	padded, err := runtime.NewJavaString("  abc  ")
	if err != nil {
		t.Fatal(err)
	}
	instance = padded
	if got := call("trim", "()Ljava/lang/String;"); got == instance {
		t.Fatal("trim returned the receiver for a padded string")
	} else if value := runtime.javaStringValue(got); value != "abc" {
		t.Fatalf("trim = %q, want %q", value, "abc")
	}
}
