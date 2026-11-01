package brewrt

import (
	"testing"
	"unicode/utf16"

	"github.com/mirusu400/aram-core/cpu"
)

// 모바일 IQ테스트 duplicates a label through AEEStdLib slot 28 and later
// draws the copy; the slot used to end the session as unimplemented.
func TestWStrDupCopiesOntoTheAppletHeap(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	source := writeStaticAECHAR(t, runtime, "IQ 테스트")
	setStaticRegisters(t, runtime, map[uint32]uint32{cpu.RegisterR0: source})
	if err := runtime.duplicateGuestWideString(); err != nil {
		t.Fatal(err)
	}
	copied, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		t.Fatal(err)
	}
	if copied == 0 || copied == source {
		t.Fatalf("WSTRDUP returned 0x%08x for source 0x%08x", copied, source)
	}
	if _, owned := runtime.heapAllocated[copied]; !owned {
		t.Fatal("WSTRDUP copy is not an applet heap block FREE can release")
	}
	// The copy must survive the source being overwritten.
	if err := runtime.cpu.WriteMemory(source, make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	units, err := runtime.readGuestWideString(copied)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(utf16.Decode(units)); got != "IQ 테스트" {
		t.Fatalf("WSTRDUP copy = %q", got)
	}

	setStaticRegisters(t, runtime, map[uint32]uint32{cpu.RegisterR0: 0})
	if err := runtime.duplicateGuestWideString(); err != nil {
		t.Fatal(err)
	}
	if got, _ := runtime.cpu.ReadRegister(cpu.RegisterR0); got != 0 {
		t.Fatalf("WSTRDUP(NULL) = 0x%08x, want NULL", got)
	}

	// The helper table routes slot 28 to its own trap.
	var encoded [4]byte
	if err := runtime.cpu.ReadMemory(helperTableBase+helperWStrDupSlot*4, encoded[:]); err != nil {
		t.Fatal(err)
	}
	want := (helperMethodTrapBase + helperWStrDupSlot*2) | 1
	if got := uint32(encoded[0]) | uint32(encoded[1])<<8 | uint32(encoded[2])<<16 | uint32(encoded[3])<<24; got != want {
		t.Fatalf("helper slot 28 = 0x%08x, want 0x%08x", got, want)
	}
}
