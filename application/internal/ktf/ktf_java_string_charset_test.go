package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
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

	// "미니엘" in EUC-KR, the encoding the handset holds its text in, with a
	// leading byte the title skips through the offset parameter.
	data := []byte{0x21, 0xb9, 0xcc, 0xb4, 0xcf, 0xbf, 0xa4}
	array, err := runtime.NewJavaArray("[B", uint32(len(data)), 1)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := runtime.ReadU32(array)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(fields+8, data); err != nil {
		t.Fatal(err)
	}
	charset, err := runtime.NewJavaString("KSC5601")
	if err != nil {
		t.Fatal(err)
	}
	instance, err := runtime.newJavaInstance("java/lang/String", 0)
	if err != nil {
		t.Fatal(err)
	}

	stack := guest.DefaultStackBase + 0x100
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
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
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, instance); err != nil {
		t.Fatal(err)
	}
	length, err := runtime.handleStringMethod("length", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if length != 3 {
		t.Fatalf("length() = %d, want 3", length)
	}
}
