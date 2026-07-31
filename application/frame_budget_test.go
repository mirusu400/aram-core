package application

import (
	"bytes"
	"context"
	"encoding/binary"
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
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	machine := created.(*Machine)

	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
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
		if err := machine.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
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
	stub := machine.wipi.layout.StubByName["MC_grpFlushLcd"]
	if stub == 0 {
		t.Fatal("MC_grpFlushLcd stub is missing")
	}
	screen := dispatchPublicAPI(
		t,
		machine.wipi,
		"MC_grpGetScreenFrameBuffer",
		0,
	).low
	if screen == 0 {
		t.Fatal("screen framebuffer is null")
	}
	if err := machine.cpu.WriteMemory(
		machine.info.TextAddress,
		[]byte{0xfe, 0xe7}, // b .
	); err != nil {
		t.Fatal(err)
	}
	stack := DefaultStackBase + DefaultStackSize - 8
	var trailing [8]byte
	binary.LittleEndian.PutUint32(trailing[0:4], 240)
	binary.LittleEndian.PutUint32(trailing[4:8], 320)
	if err := machine.cpu.WriteMemory(stack, trailing[:]); err != nil {
		t.Fatal(err)
	}
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
		if err := machine.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	before := machine.wipi.stats.PresentCount
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := machine.LastResult()
	if result.Reason != cpu.StopBudget ||
		result.Instructions != 1 ||
		result.PC != machine.info.TextAddress ||
		machine.wipi.stats.PresentCount != before+1 {
		t.Fatalf(
			"presentation frame = %+v, presents %d -> %d",
			result,
			before,
			machine.wipi.stats.PresentCount,
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
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = created.Close() })
	machine := created.(*Machine)
	if err := machine.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
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
		if err := machine.cpu.WriteRegister(register, value); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for range b.N {
		if err := machine.StepFrame(context.Background()); err != nil {
			b.Fatal(err)
		}
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
