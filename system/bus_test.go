package system

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestBusRoutesRAMROMAndTypedMMIO(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("main-ram", 0x1000, 0x100); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapROM("boot-rom", 0x2000, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	device := &registerDevice{value: 0x11223344}
	if err := bus.MapMMIO("timer", 0x3000, 0x100, device); err != nil {
		t.Fatal(err)
	}

	var value [4]byte
	binary.LittleEndian.PutUint32(value[:], 0xaabbccdd)
	if err := bus.Write(0x1010, value[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	clear(value[:])
	if err := bus.Read(0x1010, value[:], cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(value[:]); got != 0xaabbccdd {
		t.Fatalf("RAM value = %#x", got)
	}
	if err := bus.Write(0x2000, []byte{9}, cpu.PermissionWrite); !errors.Is(err, cpu.ErrPermissionDenied) {
		t.Fatalf("ROM write error = %v", err)
	}
	if err := bus.Read(0x3000, value[:], cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(value[:]); got != 0x11223344 || device.reads != 1 {
		t.Fatalf("MMIO read = %#x, calls %d", got, device.reads)
	}
	binary.LittleEndian.PutUint16(value[:2], 0x7788)
	if err := bus.Write(0x3002, value[:2], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if device.lastOffset != 2 || device.lastWidth != Width16 || device.value != 0x7788 {
		t.Fatalf("MMIO write = offset %#x width %d value %#x", device.lastOffset, device.lastWidth, device.value)
	}
}

func TestBusRejectsOverlapAlignmentAndCrossRegionAccess(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("ram", 0x1000, 0x101); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapRAM("overlap", 0x1080, 0x100); !errors.Is(err, ErrRegionOverlap) {
		t.Fatalf("overlap error = %v", err)
	}
	if err := bus.Read(0x1001, make([]byte, 4), cpu.PermissionRead); !errors.Is(err, ErrUnalignedAccess) {
		t.Fatalf("alignment error = %v", err)
	}
	if err := bus.Read(0x1100, make([]byte, 2), cpu.PermissionRead); !errors.Is(err, ErrRegionBoundary) {
		t.Fatalf("boundary error = %v", err)
	}
	if err := bus.Read(0x4000, make([]byte, 4), cpu.PermissionRead); !errors.Is(err, cpu.ErrInvalidAddress) {
		t.Fatalf("unmapped error = %v", err)
	}
	if err := bus.Read(0x1000, make([]byte, 3), cpu.PermissionRead); !errors.Is(err, ErrInvalidWidth) {
		t.Fatalf("width error = %v", err)
	}
}

func TestBusResetAndStateRoundTripAreDeterministic(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("ram", 0x1000, 0x10); err != nil {
		t.Fatal(err)
	}
	device := &registerDevice{value: 5}
	if err := bus.MapMMIO("device", 0x2000, 4, device); err != nil {
		t.Fatal(err)
	}
	if err := bus.Write(0x1000, []byte{1, 2, 3, 4}, cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	state, err := bus.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Write(0x1000, []byte{9, 9, 9, 9}, cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	device.value = 7
	if err := bus.LoadState(state); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if err := bus.Read(0x1000, got, cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if got[0] != 1 || device.value != 5 {
		t.Fatalf("restored RAM/device = %v/%d", got, device.value)
	}
	if err := bus.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := bus.Read(0x1000, got, cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if got[0] != 0 || device.value != 0 {
		t.Fatalf("reset RAM/device = %v/%d", got, device.value)
	}
}

func TestBusLoadStateIsAtomicWhenDeviceRejectsState(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("ram", 0x1000, 4); err != nil {
		t.Fatal(err)
	}
	device := &registerDevice{value: 5, rejectValue: 9}
	if err := bus.MapMMIO("device", 0x2000, 4, device); err != nil {
		t.Fatal(err)
	}
	if err := bus.Write(0x1000, []byte{1, 1, 1, 1}, cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	state, err := bus.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(state[len(state)-4:], 9)
	if err := bus.Write(0x1000, []byte{7, 7, 7, 7}, cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	device.value = 7
	if err := bus.LoadState(state); err == nil {
		t.Fatal("LoadState accepted rejected device state")
	}
	got := make([]byte, 4)
	if err := bus.Read(0x1000, got, cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if got[0] != 7 || device.value != 7 {
		t.Fatalf("failed load changed RAM/device = %v/%d", got, device.value)
	}
}

type registerDevice struct {
	value       uint32
	rejectValue uint32
	reads       int
	lastOffset  uint32
	lastWidth   Width
}

func (d *registerDevice) Reset() error {
	d.value = 0
	return nil
}

func (d *registerDevice) Read(offset uint32, width Width) (uint32, error) {
	d.reads++
	d.lastOffset = offset
	d.lastWidth = width
	return d.value, nil
}

func (d *registerDevice) Write(offset uint32, width Width, value uint32) error {
	d.lastOffset = offset
	d.lastWidth = width
	d.value = value
	return nil
}

func (d *registerDevice) SaveState() ([]byte, error) {
	state := make([]byte, 4)
	binary.LittleEndian.PutUint32(state, d.value)
	return state, nil
}

func (d *registerDevice) LoadState(state []byte) error {
	if len(state) != 4 {
		return errors.New("invalid register-device state")
	}
	value := binary.LittleEndian.Uint32(state)
	if d.rejectValue != 0 && value == d.rejectValue {
		return errors.New("rejected register-device state")
	}
	d.value = value
	return nil
}
