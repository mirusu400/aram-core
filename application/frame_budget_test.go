package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/mirusu400/aram-core/application/internal/guest"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

func TestGenericStepFrameUsesConfiguredRunBudget(t *testing.T) {
	data := syntheticEADS()
	factory := NewFactory()
	factory.RunBudget = 32
	factory.FrameRunBudget = 32
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     "frame-budget.dat",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	check(t, err)
	t.Cleanup(func() { _ = created.Close() })
	machine := created.(*Machine)

	check(t, machine.Start(context.Background()))
	if err := machine.cpu.WriteMemory(machine.info.TextAddress, []byte{
		0x01, 0x30, // adds r0, #1
		0xfe, 0xe7, // b .
	}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterPC:   machine.info.TextAddress,
		cpu.RegisterCPSR: cpu.StatusThumb,
	} {
		check(t, machine.cpu.WriteRegister(register, value))
	}
	check(t, machine.StepFrame(context.Background()))
	result := machine.LastResult()
	if result.Reason != cpu.StopBudget ||
		result.Instructions != factory.RunBudget ||
		result.PC != 0x02000002 {
		t.Fatalf("frame execution = %+v", result)
	}
}

func TestGenericStepFrameYieldsAtPresentation(t *testing.T) {
	machine := newSyntheticMachine(t)
	machine.runBudget = 32
	stub := machine.wipi.Layout.StubByName["MC_grpFlushLcd"]
	if stub == 0 {
		t.Fatal("MC_grpFlushLcd stub is missing")
	}
	screen := dispatchPublicAPI(
		t,
		machine.wipi,
		"MC_grpGetScreenFrameBuffer",
		0,
	).Low
	if screen == 0 {
		t.Fatal("screen framebuffer is null")
	}
	check(t, machine.cpu.WriteMemory(
		machine.info.TextAddress,
		[]byte{0xfe, 0xe7}, // b .
	))
	stack := DefaultStackBase + DefaultStackSize - 8
	var trailing [8]byte
	binary.LittleEndian.PutUint32(trailing[0:4], 240)
	binary.LittleEndian.PutUint32(trailing[4:8], 320)
	check(t, machine.cpu.WriteMemory(stack, trailing[:]))
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0:   0,
		cpu.RegisterR1:   screen,
		cpu.RegisterR2:   0,
		cpu.RegisterR3:   0,
		cpu.RegisterSP:   stack,
		cpu.RegisterPC:   stub &^ 1,
		cpu.RegisterLR:   machine.info.TextAddress | 1,
		cpu.RegisterCPSR: cpu.StatusThumb,
	} {
		check(t, machine.cpu.WriteRegister(register, value))
	}
	before := machine.wipi.Stats.PresentCount
	check(t, machine.StepFrame(context.Background()))
	result := machine.LastResult()
	if result.Reason != cpu.StopBudget ||
		result.Instructions != 1 ||
		result.PC != machine.info.TextAddress ||
		machine.wipi.Stats.PresentCount != before+1 {
		t.Fatalf(
			"presentation frame = %+v, presents %d -> %d",
			result,
			before,
			machine.wipi.Stats.PresentCount,
		)
	}
}

func BenchmarkGenericHandsetFrame(b *testing.B) {
	data := syntheticEADS()
	factory := NewFactory()
	factory.FrameRunBudget = DefaultHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     "handset-frame.dat",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	check(b, err)
	b.Cleanup(func() { _ = created.Close() })
	machine := created.(*Machine)
	check(b, machine.Start(context.Background()))
	if err := machine.cpu.WriteMemory(machine.info.TextAddress, []byte{
		0x01, 0x30, // adds r0, #1
		0xfe, 0xe7, // b .
	}); err != nil {
		b.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterPC:   machine.info.TextAddress,
		cpu.RegisterCPSR: cpu.StatusThumb,
	} {
		check(b, machine.cpu.WriteRegister(register, value))
	}

	b.ResetTimer()
	for range b.N {
		check(b, machine.StepFrame(context.Background()))
	}
	b.StopTimer()
	result := machine.LastResult()
	if result.Reason != cpu.StopBudget ||
		result.Instructions != DefaultHandsetRunBudget {
		b.Fatalf("frame execution = %+v", result)
	}
	b.ReportMetric(
		float64(b.N)*float64(DefaultHandsetRunBudget)/b.Elapsed().Seconds(),
		"guest-insn/s",
	)
}

func dispatchPublicAPI(t *testing.T, runtime *wipirt.Runtime, name string, args ...uint32) guest.WIPIReturn {
	t.Helper()
	for index := 0; index < 4; index++ {
		value := uint32(0)
		if index < len(args) {
			value = args[index]
		}
		check(t, runtime.CPU.WriteRegister(uint32(index), value))
	}
	sp := guest.DefaultStackBase + guest.DefaultStackSize - 0x100
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, sp))
	for index := 4; index < len(args); index++ {
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], args[index])
		check(t, runtime.CPU.WriteMemory(sp+uint32(index-4)*4, encoded[:]))
	}
	const link = uint32(0x02000001)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterLR, link))
	stub, ok := runtime.Layout.StubByName[name]
	if !ok {
		t.Fatalf("%s has no stub", name)
	}
	handled, err := runtime.DispatchTrap(context.Background(), stub&^1)
	check(t, err)
	if !handled {
		t.Fatalf("%s trap was not handled", name)
	}
	low, err := runtime.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	high, err := runtime.CPU.ReadRegister(cpu.RegisterR1)
	check(t, err)
	if pc, _ := runtime.CPU.ReadRegister(cpu.RegisterPC); pc != link&^1 {
		t.Fatalf("%s returned to PC 0x%08x", name, pc)
	}
	return guest.WIPIReturn{Low: low, High: high}
}
