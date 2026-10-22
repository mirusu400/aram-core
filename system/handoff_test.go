package system

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func TestBootHandoffSeedsMappedMemoryAndRegisters(t *testing.T) {
	bus := NewBus()
	check(t, bus.MapRAM("boot-iram", 0x1000, 0x100))
	backend := interpreter.New()
	check(t, backend.AttachSystemBus(bus))
	handoff := BootHandoff{
		ID: "test.pbl-hle", Entry: 0x2000, Mode: cpu.ModeARM,
		Registers: []RegisterSeed{{Register: cpu.RegisterR7, Value: 0xa1b2c3d4}},
		Memory:    []MemorySeed{{Address: 0x1003, Bytes: []byte{1, 2, 3, 4, 5, 6, 7}}},
	}
	check(t, handoff.Apply(bus, backend))
	data := make([]byte, 7)
	for index := range data {
		check(t, bus.Read(0x1003+uint32(index), data[index:index+1], cpu.PermissionRead))
	}
	if string(data) != string(handoff.Memory[0].Bytes) {
		t.Fatalf("handoff memory = %x", data)
	}
	value, err := backend.ReadRegister(cpu.RegisterR7)
	if err != nil || value != 0xa1b2c3d4 {
		t.Fatalf("handoff r7 = %#x error %v", value, err)
	}
	if value, err := backend.ReadRegister(cpu.RegisterPC); err != nil || value != handoff.Entry {
		t.Fatalf("handoff PC = %#x error %v", value, err)
	}
	if value, err := backend.ReadRegister(cpu.RegisterCPSR); err != nil || value&cpu.StatusThumb != 0 {
		t.Fatalf("ARM handoff CPSR = %#x error %v", value, err)
	}
}

func TestBootHandoffSynchronizesThumbEntryContext(t *testing.T) {
	bus := NewBus()
	backend := interpreter.New()
	check(t, backend.AttachSystemBus(bus))
	check(t, backend.WriteRegister(cpu.RegisterCPSR, 0x13))
	handoff := BootHandoff{ID: "test.thumb", Entry: 0x2000, Mode: cpu.ModeThumb}
	check(t, handoff.Apply(bus, backend))
	if value, err := backend.ReadRegister(cpu.RegisterPC); err != nil || value != handoff.Entry {
		t.Fatalf("Thumb handoff PC = %#x error %v", value, err)
	}
	if value, err := backend.ReadRegister(cpu.RegisterCPSR); err != nil || value&cpu.StatusThumb == 0 {
		t.Fatalf("Thumb handoff CPSR = %#x error %v", value, err)
	}
}

func TestBootHandoffRejectsOverlapsAndPCSeed(t *testing.T) {
	handoff := BootHandoff{
		ID: "test", Entry: 0x2000, Mode: cpu.ModeARM,
		Registers: []RegisterSeed{{Register: cpu.RegisterPC}},
		Memory: []MemorySeed{
			{Address: 0x1000, Bytes: make([]byte, 8)},
			{Address: 0x1004, Bytes: make([]byte, 8)},
		},
	}
	if err := handoff.Validate(); err == nil {
		t.Fatal("invalid handoff was accepted")
	}
}
