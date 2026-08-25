package system

import (
	"bytes"
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
	if err := bus.Read(0x4000, value[:], cpu.PermissionRead); err == nil {
		t.Fatal("unmapped physical read succeeded")
	} else {
		var external cpu.ExternalAbortError
		if !errors.As(err, &external) || !external.ExternalAbort() {
			t.Fatalf("unmapped physical read is not an external abort: %v", err)
		}
	}
}

func TestBusMMIOObserverReceivesInstructionContext(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("ram", 0x1000, 4); err != nil {
		t.Fatal(err)
	}
	device := &registerDevice{value: 0x11223344}
	if err := bus.MapMMIO("timer", 0x3000, 4, device); err != nil {
		t.Fatal(err)
	}
	var accesses []MMIOAccess
	bus.SetMMIOObserver(func(access MMIOAccess) {
		accesses = append(accesses, access)
	})
	context := cpu.MemoryAccessContext{
		InstructionAddress: 0x8004, Mode: cpu.ModeThumb, Attributed: true,
	}
	var value [4]byte
	if err := bus.ReadContext(context, 0x3000, value[:], cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(value[:], 0xaabbccdd)
	if err := bus.WriteContext(context, 0x3000, value[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := bus.ReadContext(context, 0x1000, value[:], cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if len(accesses) != 2 {
		t.Fatalf("observed %d MMIO accesses, want 2", len(accesses))
	}
	if got := accesses[0]; got.Context != context || got.Region != "timer" ||
		got.Address != 0x3000 || got.Offset != 0 || got.Width != Width32 ||
		got.Permission != cpu.PermissionRead || got.Value != 0x11223344 || got.Write || got.Err != nil {
		t.Fatalf("observed read = %+v", got)
	}
	if got := accesses[1]; got.Context != context || got.Permission != cpu.PermissionWrite ||
		got.Value != 0xaabbccdd || !got.Write || got.Err != nil {
		t.Fatalf("observed write = %+v", got)
	}
}

func TestBusMemoryObserverIsBoundedToConfiguredPhysicalRange(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("ram", 0x1000, 0x100); err != nil {
		t.Fatal(err)
	}
	var accesses []MemoryAccess
	if err := bus.SetMemoryObserver(0x1010, 4, func(access MemoryAccess) {
		accesses = append(accesses, access)
	}); err != nil {
		t.Fatal(err)
	}
	context := cpu.MemoryAccessContext{
		InstructionAddress: 0x8000, Mode: cpu.ModeARM, Attributed: true,
	}
	var value [4]byte
	binary.LittleEndian.PutUint32(value[:], 0xaabbccdd)
	if err := bus.WriteContext(context, 0x1010, value[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	clear(value[:])
	if err := bus.ReadContext(context, 0x1010, value[:], cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if err := bus.ReadContext(context, 0x1020, value[:], cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if len(accesses) != 2 {
		t.Fatalf("observed %d bounded memory accesses, want 2", len(accesses))
	}
	if got := accesses[0]; got.Context != context || got.Region != "ram" ||
		got.Address != 0x1010 || got.Offset != 0x10 || got.Width != Width32 ||
		got.Permission != cpu.PermissionWrite || got.Value != 0xaabbccdd ||
		!got.Write || got.MMIO || got.Err != nil {
		t.Fatalf("observed RAM write = %+v", got)
	}
	if got := accesses[1]; got.Permission != cpu.PermissionRead || got.Write ||
		got.Value != 0xaabbccdd {
		t.Fatalf("observed RAM read = %+v", got)
	}
	if err := bus.SetMemoryObserver(^uint32(0), 2, func(MemoryAccess) {}); !errors.Is(err, ErrInvalidRegion) {
		t.Fatalf("wrapping observer range error = %v", err)
	}
	if err := bus.SetMemoryObserver(0, 0, nil); err != nil {
		t.Fatalf("disable observer: %v", err)
	}
}

func TestBusInstructionMemoryObserverFiltersByAttributedGuestPC(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("ram", 0x1000, 0x100); err != nil {
		t.Fatal(err)
	}
	var accesses []MemoryAccess
	if err := bus.SetInstructionMemoryObserver(0x8000, 4, func(access MemoryAccess) {
		accesses = append(accesses, access)
	}); err != nil {
		t.Fatal(err)
	}
	matching := cpu.MemoryAccessContext{
		InstructionAddress: 0x8002, Mode: cpu.ModeThumb, Attributed: true,
	}
	outside := matching
	outside.InstructionAddress = 0x8004
	var value [4]byte
	binary.LittleEndian.PutUint32(value[:], 0x11223344)
	if err := bus.WriteContext(matching, 0x1010, value[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := bus.ReadContext(outside, 0x1010, value[:], cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if err := bus.Read(0x1010, value[:], cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if len(accesses) != 1 || accesses[0].Context != matching || !accesses[0].Write ||
		accesses[0].Address != 0x1010 || accesses[0].Value != 0x11223344 {
		t.Fatalf("instruction-attributed accesses = %+v", accesses)
	}
	if err := bus.SetInstructionMemoryObserver(^uint32(0), 2, func(MemoryAccess) {}); !errors.Is(err, ErrInvalidRegion) {
		t.Fatalf("wrapping instruction observer range error = %v", err)
	}
	if err := bus.SetInstructionMemoryObserver(0, 0, nil); err != nil {
		t.Fatalf("disable instruction observer: %v", err)
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

func TestBusSparseRAMRoundTripsOnlyTouchedPages(t *testing.T) {
	bus := NewBus()
	if err := bus.MapSparseRAM("adsp", 0x70000000, 0x08000000); err != nil {
		t.Fatal(err)
	}
	var value [4]byte
	for _, address := range []uint32{0x70000000, 0x70001338, 0x77fffffc} {
		for index := range value {
			value[index] = 0xff
		}
		if err := bus.Read(address, value[:], cpu.PermissionRead); err != nil {
			t.Fatal(err)
		}
		if value != [4]byte{} {
			t.Fatalf("untouched sparse RAM at 0x%08x = %x", address, value)
		}
	}
	low := [4]byte{1, 2, 3, 4}
	high := [4]byte{5, 6, 7, 8}
	if err := bus.Write(0x70001338, low[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := bus.Write(0x77fffffc, high[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	state, err := bus.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state) >= 3*int(sparseRAMPageSize) {
		t.Fatalf("two touched sparse pages produced %d-byte state", len(state))
	}
	if err := bus.Write(0x70001338, make([]byte, 4), cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := bus.Write(0x77fffffc, make([]byte, 4), cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := bus.LoadState(state); err != nil {
		t.Fatal(err)
	}
	for address, want := range map[uint32][4]byte{
		0x70001338: low,
		0x77fffffc: high,
	} {
		clear(value[:])
		if err := bus.Read(address, value[:], cpu.PermissionExecute); err != nil {
			t.Fatal(err)
		}
		if value != want {
			t.Fatalf("restored sparse RAM at 0x%08x = %x, want %x", address, value, want)
		}
	}
	restoredState, err := bus.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restoredState, state) {
		t.Fatal("sparse RAM state is not deterministic after round trip")
	}
	if err := bus.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := bus.Read(0x70001338, value[:], cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if value != [4]byte{} {
		t.Fatalf("reset sparse RAM = %x", value)
	}
}

func TestSparseRAMStateRejectsNonCanonicalZeroPage(t *testing.T) {
	memory := newSparseRAM()
	memory.write(0x1000, []byte{1})
	state, err := memory.saveState(0x3000)
	if err != nil {
		t.Fatal(err)
	}
	clear(state[20:])
	if _, err := decodeSparseRAMState(0x3000, state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("zero sparse page state error = %v", err)
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

func TestBusLoadStateSubsetLeavesNewRegionsUntouched(t *testing.T) {
	source := NewBus()
	if err := source.MapRAM("ram", 0x1000, 4); err != nil {
		t.Fatal(err)
	}
	if err := source.Write(0x1000, []byte{1, 2, 3, 4}, cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	state, err := source.SaveState()
	if err != nil {
		t.Fatal(err)
	}

	destination := NewBus()
	if err := destination.MapRAM("ram", 0x1000, 4); err != nil {
		t.Fatal(err)
	}
	if err := destination.MapRAMImage("new", 0x2000, 4, []byte{9, 8, 7, 6}); err != nil {
		t.Fatal(err)
	}
	if err := destination.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("exact load with added region error = %v", err)
	}
	if err := destination.LoadStateSubset(state); err != nil {
		t.Fatal(err)
	}
	var data [4]byte
	if err := destination.Read(0x1000, data[:], cpu.PermissionRead); err != nil ||
		!bytes.Equal(data[:], []byte{1, 2, 3, 4}) {
		t.Fatalf("restored existing RAM = %v error %v", data, err)
	}
	if err := destination.Read(0x2000, data[:], cpu.PermissionRead); err != nil ||
		!bytes.Equal(data[:], []byte{9, 8, 7, 6}) {
		t.Fatalf("new RAM after subset restore = %v error %v", data, err)
	}

	missing := NewBus()
	if err := missing.MapRAM("different", 0x1000, 4); err != nil {
		t.Fatal(err)
	}
	if err := missing.LoadStateSubset(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("subset load with missing serialized region error = %v", err)
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
