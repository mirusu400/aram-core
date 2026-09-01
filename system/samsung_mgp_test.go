package system

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestSamsungMGPControlReleasesCompanionAndPublishesReady(t *testing.T) {
	bus := NewBus()
	if err := bus.MapSparseRAM("mgp-shared", 0x1000, 0x100); err != nil {
		t.Fatal(err)
	}
	control, err := NewSamsungMGPControl(bus, SamsungMGPControlConfig{
		Size: 0x20, ReleaseOffset: 0x0c,
		ReadyAddress: 0x1010, ReadyValue: 1,
		ResponseDelayInstructions: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO("mgp-control", 0x2000, 0x20, control); err != nil {
		t.Fatal(err)
	}

	writeSamsungMGPRegister(t, bus, 0x200c, 0)
	if err := control.Advance(4); err != nil {
		t.Fatal(err)
	}
	if got := readSamsungMGPByte(t, bus, 0x1010); got != 0 {
		t.Fatalf("ready after an idle zero write = %#x", got)
	}

	writeSamsungMGPRegister(t, bus, 0x200c, 1)
	writeSamsungMGPRegister(t, bus, 0x200c, 0)
	if got := readSamsungMGPByte(t, bus, 0x1010); got != 0 {
		t.Fatalf("ready before delayed response = %#x", got)
	}
	if err := control.Advance(3); err != nil {
		t.Fatal(err)
	}
	if got := readSamsungMGPByte(t, bus, 0x1010); got != 0 {
		t.Fatalf("ready before final instruction = %#x", got)
	}
	if err := control.Advance(1); err != nil {
		t.Fatal(err)
	}
	if got := readSamsungMGPByte(t, bus, 0x1010); got != 1 {
		t.Fatalf("ready after companion release = %#x", got)
	}
}

func TestSamsungMGPControlStateRoundTripPreservesPendingResponse(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("mgp-shared", 0x1000, 0x100); err != nil {
		t.Fatal(err)
	}
	config := SamsungMGPControlConfig{
		Size: 0x20, ReleaseOffset: 0x0c,
		ReadyAddress: 0x1010, ReadyValue: 0x5a,
		ResponseDelayInstructions: 8,
	}
	control, err := NewSamsungMGPControl(bus, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Write(0x0c, Width16, 1); err != nil {
		t.Fatal(err)
	}
	if err := control.Write(0x0c, Width16, 0); err != nil {
		t.Fatal(err)
	}
	if err := control.Advance(3); err != nil {
		t.Fatal(err)
	}
	state, err := control.SaveState()
	if err != nil {
		t.Fatal(err)
	}

	restored, err := NewSamsungMGPControl(bus, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if err := restored.Advance(4); err != nil {
		t.Fatal(err)
	}
	if got := readSamsungMGPByte(t, bus, 0x1010); got != 0 {
		t.Fatalf("restored response completed early = %#x", got)
	}
	if err := restored.Advance(1); err != nil {
		t.Fatal(err)
	}
	if got := readSamsungMGPByte(t, bus, 0x1010); got != 0x5a {
		t.Fatalf("restored response = %#x", got)
	}

	wrongConfig := config
	wrongConfig.ReadyAddress++
	wrong, _ := NewSamsungMGPControl(bus, wrongConfig)
	if err := wrong.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("state loaded into a mismatched profile: %v", err)
	}
}

func TestSamsungMGPControlMigratesPassiveRegisterWindowSubset(t *testing.T) {
	legacy, err := NewLatchedRegisterWindow(0x20, Width16)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Write(2, Width16, 0x1234); err != nil {
		t.Fatal(err)
	}
	state, err := legacy.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	bus := NewBus()
	if err := bus.MapRAM("mgp-shared", 0x1000, 0x100); err != nil {
		t.Fatal(err)
	}
	control, _ := NewSamsungMGPControl(bus, SamsungMGPControlConfig{
		Size: 0x20, ReleaseOffset: 0x0c,
		ReadyAddress: 0x1010, ReadyValue: 1,
		ResponseDelayInstructions: 1,
	})
	if err := control.LoadStateSubset(state); err != nil {
		t.Fatal(err)
	}
	if got, err := control.Read(2, Width16); err != nil || got != 0x1234 {
		t.Fatalf("migrated register = %#x, %v", got, err)
	}
}

func TestSamsungMGPControlRejectsInvalidConfigurationAndAccess(t *testing.T) {
	bus := NewBus()
	valid := SamsungMGPControlConfig{
		Size: 0x20, ReleaseOffset: 0x0c,
		ReadyAddress: 0x1010, ReadyValue: 1,
		ResponseDelayInstructions: 1,
	}
	for _, mutate := range []func(*SamsungMGPControlConfig){
		func(config *SamsungMGPControlConfig) { config.Size = 0 },
		func(config *SamsungMGPControlConfig) { config.Size = 3 },
		func(config *SamsungMGPControlConfig) { config.ReleaseOffset = 1 },
		func(config *SamsungMGPControlConfig) { config.ReleaseOffset = config.Size },
		func(config *SamsungMGPControlConfig) { config.ReadyValue = 0 },
		func(config *SamsungMGPControlConfig) { config.ResponseDelayInstructions = 0 },
	} {
		config := valid
		mutate(&config)
		if _, err := NewSamsungMGPControl(bus, config); err == nil {
			t.Fatalf("accepted invalid config %+v", config)
		}
	}
	control, err := NewSamsungMGPControl(bus, valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Read(0, Width8); !errors.Is(err, ErrSamsungMGPMMIO) {
		t.Fatalf("byte register read error = %v", err)
	}
	if err := control.Write(1, Width16, 0); !errors.Is(err, ErrSamsungMGPMMIO) {
		t.Fatalf("unaligned register write error = %v", err)
	}
	if err := control.Write(0, Width16, 0x10000); !errors.Is(err, ErrSamsungMGPMMIO) {
		t.Fatalf("oversized register write error = %v", err)
	}
}

func writeSamsungMGPRegister(t *testing.T, bus *Bus, address uint32, value uint16) {
	t.Helper()
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	if err := bus.Write(address, encoded[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
}

func readSamsungMGPByte(t *testing.T, bus *Bus, address uint32) byte {
	t.Helper()
	var value [1]byte
	if err := bus.Read(address, value[:], cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	return value[0]
}
