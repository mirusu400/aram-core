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
