package system

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func TestHLERunnerDispatchesProfileCallAndReturnsThroughLinkRegister(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("code", 0x1000, 0x200); err != nil {
		t.Fatal(err)
	}
	writeARMInstructions(t, bus, 0x1000,
		0xeb00003e, // BL 0x1100
		0xe2800001, // ADD r0, r0, #1
		0xe1200070, // BKPT
	)
	backend := interpreter.New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	call := HLECallProfile{
		ID: "fixture-call", Contract: "fixture.return-41",
		Address: 0x1100, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	}
	runner, err := NewHLERunner(bus, backend, []HLECallProfile{call}, map[string]HLECallHandler{
		call.Contract: HLECallHandlerFunc(func(context HLECallContext) error {
			return context.CPU.WriteRegister(cpu.RegisterR0, 41)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
		result.Instructions != 3 || result.PC != 0x100c {
		t.Fatalf("HLE run result = %+v", result)
	}
	if value, _ := backend.ReadRegister(cpu.RegisterR0); value != 42 {
		t.Fatalf("r0 after HLE return = %d", value)
	}
	invocations := runner.Invocations()
	if len(invocations) != 1 || invocations[0].ID != call.ID ||
		invocations[0].ReturnAddress != 0x1004 {
		t.Fatalf("HLE invocation trace = %+v", invocations)
	}
	var trapped [4]byte
	if err := bus.Read(0x1100, trapped[:], cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if trapped != [4]byte{} {
		t.Fatalf("HLE runner patched guest bytes: %x", trapped)
	}
}

func TestHLERunnerRequiresExplicitHandlerAndPropagatesHandlerFault(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("code", 0x1000, 0x200); err != nil {
		t.Fatal(err)
	}
	backend := interpreter.New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	call := HLECallProfile{
		ID: "fixture-call", Contract: "fixture.failure",
		Address: 0x1100, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	}
	if _, err := NewHLERunner(bus, backend, []HLECallProfile{call}, nil); err == nil {
		t.Fatal("HLE runner accepted a call without a handler")
	}
	want := errors.New("fixture handler failed")
	runner, err := NewHLERunner(bus, backend, []HLECallProfile{call}, map[string]HLECallHandler{
		call.Contract: HLECallHandlerFunc(func(HLECallContext) error { return want }),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), call.Address, call.Mode, 1)
	if result.Reason != cpu.StopFault || result.Instructions != 0 ||
		result.PC != call.Address || !errors.Is(result.Err, want) {
		t.Fatalf("handler fault result = %+v", result)
	}
}

func TestHLERunnerPreservesUnownedExecutionTrap(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("code", 0x1000, 0x200); err != nil {
		t.Fatal(err)
	}
	writeARMInstructions(t, bus, 0x1000, 0xe1a00000, 0xe1a00000)
	backend := interpreter.New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	call := HLECallProfile{
		ID: "fixture-call", Contract: "fixture.return",
		Address: 0x1100, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
	}
	runner, err := NewHLERunner(bus, backend, []HLECallProfile{call}, map[string]HLECallHandler{
		call.Contract: HLECallHandlerFunc(func(HLECallContext) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.SetExecutionTraps([]cpu.ExecutionTrap{
		{Address: call.Address, Mode: call.Mode},
		{Address: 0x1004, Mode: cpu.ModeARM},
	}); err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopExecutionTrap ||
		result.Instructions != 1 || result.PC != 0x1004 {
		t.Fatalf("unowned HLE-adjacent trap result = %+v", result)
	}
}

func writeARMInstructions(t *testing.T, bus *Bus, address uint32, instructions ...uint32) {
	t.Helper()
	var encoded [4]byte
	for index, instruction := range instructions {
		binary.LittleEndian.PutUint32(encoded[:], instruction)
		if err := bus.Write(address+uint32(index*4), encoded[:], cpu.PermissionWrite); err != nil {
			t.Fatal(err)
		}
	}
}
