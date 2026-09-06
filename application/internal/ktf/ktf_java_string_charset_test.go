package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// TestKTFStringCharsetConstructorMaterializesText is the 다크슬레이어2 report.
// The title decodes every line it shows through
// String(byte[], int, int, String) and then draws it as
//
//	int n = line.length(); line.getChars(0, n, buffer, 0);
//	g.drawChars(buffer, 0, n, x, y, anchor);
//
// The charset-named constructors were not implemented, so the switch fell to
// its silent default, the String was never materialised, length() answered 0
// and drawChars was asked for no characters at all. Every menu label, name
// plate and line of dialogue in the game was blank.
func TestKTFStringCharsetConstructorMaterializesText(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	// "미니엘" in EUC-KR, the encoding the handset holds its text in, with a
	// leading byte the title skips through the offset parameter.
	data := []byte{0x21, 0xb9, 0xcc, 0xb4, 0xcf, 0xbf, 0xa4}
	array, err := runtime.NewJavaArray("[B", uint32(len(data)), 1)
	check(t, err)
	fields := readU32(t, runtime, array)
	check(t, runtime.CPU.WriteMemory(fields+8, data))
	charset := newJavaString(t, runtime, "KSC5601")
	instance, err := runtime.newJavaInstance("java/lang/String", 0)
	check(t, err)

	stack := guest.DefaultStackBase + 0x100
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, stack))
	if err := runtime.writeWords(stack, []uint32{
		uint32(len(data) - 1),
		charset,
	}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: instance,
		cpu.RegisterR2: array,
		cpu.RegisterR3: 1,
	} {
		check(t, runtime.CPU.WriteRegister(register, value))
	}
	if _, err := runtime.handleStringMethod(
		"<init>",
		"([BIILjava/lang/String;)V",
	); err != nil {
		t.Fatal(err)
	}

	if value := runtime.javaStringValue(instance); value != "미니엘" {
		t.Fatalf("charset constructor produced %q, want %q", value, "미니엘")
	}
	// length() is what the title asks before it copies the characters out, so
	// a materialised string that still measures zero would draw nothing.
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, instance))
	length, err := runtime.handleStringMethod("length", "()I")
	check(t, err)
	if length != 3 {
		t.Fatalf("length() = %d, want 3", length)
	}
}
