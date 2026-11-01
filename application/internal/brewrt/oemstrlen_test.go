package brewrt

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// 컴온베이비2 sizes a STRTOWSTR buffer with AEEStdLib slot 33 (OEMSTRLEN),
// which used to end the session as unimplemented. A Korean character is two
// EUC-KR bytes but one AECHAR, so the count is in characters.
func TestOEMStrLenCountsTheAECHARsStrToWStrWrites(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	// "A한글" in EUC-KR: one ASCII byte and two double-byte characters.
	text := []byte{'A', 0xc7, 0xd1, 0xb1, 0xdb, 0}
	address, err := runtime.allocateGuest(uint32(len(text)))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(address, text); err != nil {
		t.Fatal(err)
	}
	setStaticRegisters(t, runtime, map[uint32]uint32{cpu.RegisterR0: address})
	if err := runtime.returnOEMStringLength(); err != nil {
		t.Fatal(err)
	}
	if got, _ := runtime.cpu.ReadRegister(cpu.RegisterR0); got != 3 {
		t.Fatalf("OEMSTRLEN = %d, want 3 characters", got)
	}

	// A buffer of (OEMSTRLEN + 1) * 2 bytes holds STRTOWSTR's whole output.
	destination, err := runtime.allocateGuest((3 + 1) * 2)
	if err != nil {
		t.Fatal(err)
	}
	setStaticRegisters(t, runtime, map[uint32]uint32{
		cpu.RegisterR0: address,
		cpu.RegisterR1: destination,
		cpu.RegisterR2: (3 + 1) * 2,
	})
	if err := runtime.convertGuestStringToWide(); err != nil {
		t.Fatal(err)
	}
	units, err := runtime.readGuestWideString(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got := string([]rune{rune(units[0]), rune(units[1]), rune(units[2])}); len(units) != 3 || got != "A한글" {
		t.Fatalf("STRTOWSTR into the sized buffer = %q (%d units)", got, len(units))
	}

	setStaticRegisters(t, runtime, map[uint32]uint32{cpu.RegisterR0: 0})
	if err := runtime.returnOEMStringLength(); err != nil {
		t.Fatal(err)
	}
	if got, _ := runtime.cpu.ReadRegister(cpu.RegisterR0); got != 0 {
		t.Fatalf("OEMSTRLEN(NULL) = %d", got)
	}
}
