package ktf

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// A char range a Java API is handed is the title's to get wrong, and the
// handset answers with a catchable exception. 무한의통통 builds a String from
// six chars of a five-char array; ARAM failed the host call instead, which
// stopped the whole title (fuzz finding d3bb4391ad183f8e).
func TestKTFCharArrayArgumentsThrowJavaExceptions(t *testing.T) {
	r := newTestRuntime(t)
	r.JvmContext = allocWords(t, r, 3+128)
	array, err := r.newJavaCharArray("ABCDE")
	check(t, err)
	stack := guest.DefaultStackBase + 0x100
	check(t, r.CPU.WriteRegister(cpu.RegisterSP, stack))
	expect := func(what, want string, err error) {
		t.Helper()
		var exception *ktfUnhandledJavaException
		if !errors.As(err, &exception) || exception.name != want {
			t.Fatalf("%s error = %v, want %s", what, err, want)
		}
	}
	set := func(r1, r2, r3, stacked uint32) {
		t.Helper()
		check(t, r.CPU.WriteRegister(cpu.RegisterR1, r1))
		check(t, r.CPU.WriteRegister(cpu.RegisterR2, r2))
		check(t, r.CPU.WriteRegister(cpu.RegisterR3, r3))
		check(t, r.WriteU32(stack, stacked))
	}

	result := newHostObject(t, r, "java/lang/String")
	set(result, array, 0, 6)
	_, err = r.handleStringMethod("<init>", "([CII)V")
	expect("String(char[5], 0, 6)", "java/lang/StringIndexOutOfBoundsException", err)
	set(result, 0, 0, 1)
	_, err = r.handleStringMethod("<init>", "([CII)V")
	expect("String(null, 0, 1)", "java/lang/NullPointerException", err)
	set(result, array, ^uint32(0), 2)
	_, err = r.handleStringMethod("<init>", "([CII)V")
	expect("String(char[5], -1, 2)", "java/lang/StringIndexOutOfBoundsException", err)
	set(array, 4, 2, 0)
	_, err = r.handleStringMethod("valueOf", "([CII)Ljava/lang/String;")
	expect("String.valueOf(char[5], 4, 2)", "java/lang/StringIndexOutOfBoundsException", err)

	buffer := newHostObject(t, r, "java/lang/StringBuffer")
	r.stringBuffers[buffer] = "x"
	set(buffer, array, 1, 5)
	_, err = r.handleStringBufferMethod("append", "([CII)Ljava/lang/StringBuffer;")
	expect("StringBuffer.append(char[5], 1, 5)", "java/lang/ArrayIndexOutOfBoundsException", err)
	if got := r.stringBuffers[buffer]; got != "x" {
		t.Fatalf("rejected append changed the buffer to %q", got)
	}

	// An in-range call is unchanged.
	set(result, array, 1, 3)
	_, err = r.handleStringMethod("<init>", "([CII)V")
	check(t, err)
	if got := r.javaStringValue(result); got != "BCD" {
		t.Fatalf("String(char[5], 1, 3) = %q", got)
	}
}
