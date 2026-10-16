package minigame

import (
	"encoding/binary"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"testing"
)

func TestLegacyRandomUsesPackedBoundsAndOutputPointer(t *testing.T) {
	backend := interpreter.New()
	defer backend.Close()
	if err := backend.Map(0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	var output [4]byte
	binary.LittleEndian.PutUint32(output[:], 0x1100)
	if err := backend.WriteMemory(0x1000, output[:]); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterSP, 0x1000); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 2|6<<16); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{cpu: backend, legacy: true, rngState: 1}
	seen := make(map[uint32]bool)
	for range 16 {
		if _, err := runtime.dispatchLegacy(0x40); err != nil {
			t.Fatal(err)
		}
		if err := backend.ReadMemory(0x1100, output[:]); err != nil {
			t.Fatal(err)
		}
		value := binary.LittleEndian.Uint32(output[:])
		if value < 2 || value > 6 {
			t.Fatalf("random result=%d, want 2..6", value)
		}
		seen[value] = true
	}
	if len(seen) < 2 {
		t.Fatal("random service always returned the same value")
	}
}
