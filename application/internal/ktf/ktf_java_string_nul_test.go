package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// TestKTFStringByteConstructorStopsAtNUL is the 원더즈-영웅의길 report
// (issue #152). The title keeps its monster records in fixed-width,
// NUL-padded EUC-KR fields and builds one lookup key with
// new String(record, offset, fieldWidth) while the other side of the same
// lookup is the bare name. The handset's native conversion stops at the NUL,
// so both keys hash and compare equal there; decoding the padding as U+0000
// code units made the hashtable miss and the map change threw a
// NullPointerException that left the title on NOW LOADING forever.
func TestKTFStringByteConstructorStopsAtNUL(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	// "로이론 병사" in EUC-KR inside a 32-byte record field.
	name := []byte{0xb7, 0xce, 0xc0, 0xcc, 0xb7, 0xd0, 0x20, 0xba, 0xb4, 0xbb, 0xe7}
	record := make([]byte, 32)
	copy(record, name)
	array, err := runtime.NewJavaArray("[B", uint32(len(record)), 1)
	check(t, err)
	fields := readU32(t, runtime, array)
	check(t, runtime.CPU.WriteMemory(fields+8, record))

	padded, err := runtime.newJavaInstance("java/lang/String", 0)
	check(t, err)
	// The fourth parameter (count) travels on the stack.
	stack := guest.DefaultStackBase + 0x100
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, stack))
	check(t, runtime.writeWords(stack, []uint32{uint32(len(record))}))
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: padded,
		cpu.RegisterR2: array,
		cpu.RegisterR3: 0,
	} {
		check(t, runtime.CPU.WriteRegister(register, value))
	}
	_, err = runtime.handleStringMethod("<init>", "([BII)V")
	check(t, err)

	if got := runtime.javaStringValue(padded); got != "로이론 병사" {
		t.Fatalf("padded field decoded to %q, want %q", got, "로이론 병사")
	}
	bare := newJavaString(t, runtime, "로이론 병사")
	hashes := make([]uint32, 0, 2)
	for _, instance := range []uint32{padded, bare} {
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, instance))
		hash, hashErr := runtime.handleStringMethod("hashCode", "()I")
		check(t, hashErr)
		hashes = append(hashes, hash)
	}
	if hashes[0] != hashes[1] {
		t.Fatalf("hashCode() differs: padded %#x, bare %#x", hashes[0], hashes[1])
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, padded))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, bare))
	equal, err := runtime.handleStringMethod("equals", "(Ljava/lang/Object;)Z")
	check(t, err)
	if equal != 1 {
		t.Fatalf("equals() = %d, want 1", equal)
	}

	// UTF-16 sources carry NUL bytes inside ordinary characters and must keep
	// them.
	utf16Data := []byte{0x00, 0x41, 0x00, 0x42}
	utf16Array, err := runtime.NewJavaArray("[B", uint32(len(utf16Data)), 1)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(readU32(t, runtime, utf16Array)+8, utf16Data))
	charset := newJavaString(t, runtime, "UTF-16BE")
	wide, err := runtime.newJavaInstance("java/lang/String", 0)
	check(t, err)
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: wide,
		cpu.RegisterR2: utf16Array,
		cpu.RegisterR3: charset,
	} {
		check(t, runtime.CPU.WriteRegister(register, value))
	}
	_, err = runtime.handleStringMethod("<init>", "([BLjava/lang/String;)V")
	check(t, err)
	if got := runtime.javaStringValue(wide); got != "AB" {
		t.Fatalf("UTF-16 constructor produced %q, want %q", got, "AB")
	}
}

func TestKTFCharsetStringKeepsNULForScriptLineLength(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	data := []byte{'D', 'R', 'A', 'W', 0, 0}
	array, err := runtime.NewJavaArray("[B", uint32(len(data)), 1)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(readU32(t, runtime, array)+8, data))
	charset := newJavaString(t, runtime, "KSC5601")
	instance, err := runtime.newJavaInstance("java/lang/String", 0)
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, instance))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, array))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, charset))
	_, err = runtime.handleStringMethod("<init>", "([BLjava/lang/String;)V")
	check(t, err)
	if got := runtime.javaStringValue(instance); got != "DRAW\x00\x00" {
		t.Fatalf("charset string = %q, want NUL-padded script line", got)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, instance))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 0))
	end, err := runtime.handleStringMethod("indexOf", "(I)I")
	check(t, err)
	if end != 4 {
		t.Fatalf("script line NUL index = %d, want 4", end)
	}
}

func TestKTFIntegerParseIntAcceptsNULTerminatedToken(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	token := newJavaString(t, runtime, "16\x00")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, token))
	value, err := runtime.handleIntegerMethod("parseInt", "(Ljava/lang/String;)I")
	check(t, err)
	if value != 16 {
		t.Fatalf("parseInt(16\\0) = %d, want 16", value)
	}
}

// TestKTFStringIndexOfNULFindsTerminator is the SBS포트리스2 report
// (issue #591). The title reads its character, weapon and mode names out of
// fixed-width NUL-padded fields with
//
//	name = new String(buffer, offset, width);
//	name = name.substring(0, name.indexOf(0));
//
// which only works when indexOf(0) answers the end of the text. The handset
// stops the conversion at the NUL (issue #152), so the terminator a title
// asks for sits at length(). Answering -1 made substring(0, -1) hand back
// null for all 86 names and the first battle frame threw a
// NullPointerException.
func TestKTFStringIndexOfNULFindsTerminator(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	// "캐롯" in EUC-KR inside a 9-byte name field.
	field := make([]byte, 9)
	copy(field, []byte{0xc4, 0xb3, 0xb7, 0xd4})
	array, err := runtime.NewJavaArray("[B", uint32(len(field)), 1)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(readU32(t, runtime, array)+8, field))
	name, err := runtime.newJavaInstance("java/lang/String", 0)
	check(t, err)
	stack := guest.DefaultStackBase + 0x100
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, stack))
	check(t, runtime.writeWords(stack, []uint32{uint32(len(field))}))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, name))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, array))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 0))
	_, err = runtime.handleStringMethod("<init>", "([BII)V")
	check(t, err)

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, name))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 0))
	end, err := runtime.handleStringMethod("indexOf", "(I)I")
	check(t, err)
	if end != 2 {
		t.Fatalf("indexOf(0) = %d, want the terminator at 2", int32(end))
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, name))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 0))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, end))
	cut, err := runtime.handleStringMethod("substring", "(II)Ljava/lang/String;")
	check(t, err)
	if got := runtime.javaStringValue(cut); cut == 0 || got != "캐롯" {
		t.Fatalf("substring(0, indexOf(0)) = %q at %#x, want %q", got, cut, "캐롯")
	}

	// A search that starts past the terminator, and one for any other
	// character, still misses.
	for _, probe := range []struct {
		descriptor   string
		target, from uint32
	}{
		{"(II)I", 0, 3},
		{"(I)I", 'A', 0},
	} {
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, name))
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, probe.target))
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, probe.from))
		index, indexErr := runtime.handleStringMethod("indexOf", probe.descriptor)
		check(t, indexErr)
		if int32(index) != -1 {
			t.Fatalf(
				"indexOf%s(%d, %d) = %d, want -1",
				probe.descriptor, probe.target, probe.from, int32(index),
			)
		}
	}
}
