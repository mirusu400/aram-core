package system

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func TestClockedRunnerAdvancesDevicesInDeterministicQuanta(t *testing.T) {
	backend := interpreter.New()
	if err := backend.Map(
		0x1000, 0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	writeClockedRunnerARM(t, backend, 0x1000, []uint32{
		0xe1a00000, // MOV r0, r0
		0xeafffffd, // B 0x1000
	})
	clock := &recordingClock{}
	runner, err := NewClockedRunner(backend, backend, 4, clock)
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), 0x1000, cpu.ModeARM, 10)
	if result.Err != nil || result.Reason != cpu.StopBudget ||
		result.Instructions != 10 || result.PC != 0x1000 {
		t.Fatalf("clocked result = %+v", result)
	}
	if want := []uint64{4, 4, 2}; !reflect.DeepEqual(clock.advances, want) {
		t.Fatalf("device advances = %v, want %v", clock.advances, want)
	}
}

func TestClockedRunnerPreservesModeAcrossSlicesAndStops(t *testing.T) {
	backend := interpreter.New()
	if err := backend.Map(
		0x1000, 0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	writeClockedRunnerARM(t, backend, 0x1000, []uint32{
		0xe12fff10, // BX r0
	})
	if err := backend.WriteMemory(0x1100, []byte{
		0x01, 0x21, // MOVS r1, #1
		0x00, 0xbe, // BKPT
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 0x1101); err != nil {
		t.Fatal(err)
	}
	clock := &recordingClock{}
	runner, err := NewClockedRunner(backend, backend, 1, clock)
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
		result.Instructions != 3 || result.PC != 0x1104 {
		t.Fatalf("ARM-to-Thumb clocked result = %+v", result)
	}
	if want := []uint64{1, 1, 1}; !reflect.DeepEqual(clock.advances, want) {
		t.Fatalf("device advances = %v, want %v", clock.advances, want)
	}
	if value, err := backend.ReadRegister(cpu.RegisterR1); err != nil || value != 1 {
		t.Fatalf("Thumb result r1 = %d, error %v", value, err)
	}
}

func TestClockedRunnerReportsDeviceAdvanceFault(t *testing.T) {
	backend := interpreter.New()
	if err := backend.Map(
		0x1000, 0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	writeClockedRunnerARM(t, backend, 0x1000, []uint32{0xeafffffe})
	advanceFailure := errors.New("synthetic clock failure")
	clock := &recordingClock{err: advanceFailure}
	runner, err := NewClockedRunner(backend, backend, 4, clock)
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Reason != cpu.StopFault || result.Instructions != 4 ||
		!errors.Is(result.Err, advanceFailure) {
		t.Fatalf("device fault result = %+v", result)
	}
}

func TestClockedRunnerDrivesSyntheticQualcommTimeTickIRQ(t *testing.T) {
	backend := interpreter.New()
	controller := NewQualcommInterruptController(backend)
	bootControl, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5680, ClockModeStatus: 1,
		NANDReady: NewStatusSignal(), InterruptController: controller,
		TimeTickClock: &QualcommTimeTickClockConfig{
			InstructionsPerSecond: 1, TimeTickHz: 1, InterruptSource: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := NewBus()
	if err := bus.MapRAM("vectors-and-code", 0, 0x2000); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO(
		"qualcomm-control", 0x80000000, QualcommBootControlWindowSize, bootControl,
	); err != nil {
		t.Fatal(err)
	}
	writeBusWord := func(address, value uint32) {
		t.Helper()
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], value)
		if err := bus.Write(address, encoded[:], cpu.PermissionWrite); err != nil {
			t.Fatal(err)
		}
	}
	writeBusWord(0x18, 0xe3a00004) // MOV r0, #4 (source mask)
	writeBusWord(0x1c, 0xe59f1004) // LDR r1, [pc, #4]
	writeBusWord(0x20, 0xe5810000) // STR r0, [r1] (INT_CLEAR_0)
	writeBusWord(0x24, 0xe25ef004) // SUBS pc, lr, #4
	writeBusWord(0x28, 0x80000900)
	writeBusWord(0x1000, 0xe2822001) // ADD r2, r2, #1
	writeBusWord(0x1004, 0xeafffffd) // B 0x1000
	writeBusWord(0x80000914, 1<<2)
	writeBusWord(0x800054c4, 2)
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterCPSR, 0x13); err != nil {
		t.Fatal(err)
	}
	runner, err := NewClockedRunner(backend, backend, 1, bootControl)
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopBudget ||
		result.Instructions != 8 || result.PC != 0x1000 {
		t.Fatalf("timer IRQ result = %+v", result)
	}
	if value, err := backend.ReadRegister(cpu.RegisterR2); err != nil || value != 2 {
		t.Fatalf("post-IRQ r2 = %d, error %v", value, err)
	}
	if status, _ := controller.Read(qualcommInterruptStatus0Offset, Width32); status != 0 {
		t.Fatalf("acknowledged interrupt status = %#x", status)
	}
}

type recordingClock struct {
	advances []uint64
	err      error
}

func (clock *recordingClock) Advance(cycles uint64) error {
	clock.advances = append(clock.advances, cycles)
	return clock.err
}

func writeClockedRunnerARM(
	t *testing.T,
	backend cpu.Backend,
	address uint32,
	instructions []uint32,
) {
	t.Helper()
	code := make([]byte, len(instructions)*4)
	for index, instruction := range instructions {
		binary.LittleEndian.PutUint32(code[index*4:], instruction)
	}
	if err := backend.WriteMemory(address, code); err != nil {
		t.Fatal(err)
	}
}
