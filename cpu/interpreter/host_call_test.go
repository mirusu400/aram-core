package interpreter

import (
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestHostCallFrameCapturesAndCommitsInBulk(t *testing.T) {
	backend := New()
	defer backend.Close()
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		t.Fatal(err)
	}
	const (
		stack      = uint32(0x1100)
		parameters = uint32(0x1200)
	)
	writeWords := func(address uint32, values ...uint32) {
		t.Helper()
		encoded := make([]byte, len(values)*4)
		for index, value := range values {
			binary.LittleEndian.PutUint32(encoded[index*4:], value)
		}
		if err := backend.WriteMemory(address, encoded); err != nil {
			t.Fatal(err)
		}
	}
	writeWords(stack, 0x41, 0x42)
	writeWords(parameters, 0x51, 0x52, 0x53)
	for register := uint32(0); register <= cpu.RegisterR12; register++ {
		if err := backend.WriteRegister(register, 0x100+register); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterLR, 0x3344); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterCPSR, cpu.StatusThumb); err != nil {
		t.Fatal(err)
	}

	var frame cpu.HostCallFrame
	if err := cpu.CaptureHostCallFrame(backend, &frame, cpu.HostCallFrameRequest{
		StackWords:       2,
		ParameterAddress: parameters,
		ParameterWords:   3,
	}); err != nil {
		t.Fatal(err)
	}
	if frame.Registers[cpu.RegisterR7] != 0x107 ||
		frame.Registers[cpu.RegisterSP] != stack ||
		frame.Registers[cpu.RegisterLR] != 0x3344 ||
		frame.Stack[0] != 0x41 || frame.Stack[1] != 0x42 ||
		frame.Parameters[0] != 0x51 || frame.Parameters[2] != 0x53 {
		t.Fatalf("captured host-call frame = %+v", frame)
	}

	var commit cpu.RegisterCommit
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0:   0xa0,
		cpu.RegisterR1:   0xa1,
		cpu.RegisterPC:   0x2000,
		cpu.RegisterCPSR: 0,
	} {
		if err := commit.Set(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := cpu.CommitHostCallRegisters(backend, commit); err != nil {
		t.Fatal(err)
	}
	for register, want := range map[uint32]uint32{
		cpu.RegisterR0: 0xa0,
		cpu.RegisterR1: 0xa1,
		cpu.RegisterPC: 0x2000,
	} {
		if got, err := backend.ReadRegister(register); err != nil || got != want {
			t.Fatalf("register %d = %#x, %v; want %#x", register, got, err, want)
		}
	}
	statistics := backend.ExecutionStatistics()
	if statistics.HostFrameCaptures != 1 || statistics.HostRegisterCommits != 1 {
		t.Fatalf("host-call bulk statistics = %+v", statistics)
	}
}

func BenchmarkHostCallFrameCapture(b *testing.B) {
	backend := New()
	b.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		b.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterSP, 0x1100); err != nil {
		b.Fatal(err)
	}
	request := cpu.HostCallFrameRequest{StackWords: 9}
	var frame cpu.HostCallFrame
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := cpu.CaptureHostCallFrame(backend, &frame, request); err != nil {
			b.Fatal(err)
		}
	}
}
