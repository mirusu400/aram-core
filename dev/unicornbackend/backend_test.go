package unicornbackend

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
)

func TestMissingLibraryReturnsUnavailable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "definitely-missing-unicorn-library")
	backend, err := NewWithOptions(Options{LibraryPath: missing})
	if backend != nil {
		_ = backend.Close()
		t.Fatal("missing library returned a backend")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing library error = %v, want ErrUnavailable", err)
	}
}

func TestARMConditionEvaluation(t *testing.T) {
	status := statusN | statusC
	want := []bool{
		false, true, true, false,
		true, false, false, true,
		true, false, false, true,
		false, true, true, false,
	}
	for condition, expected := range want {
		if got := armConditionPassed(uint8(condition), status); got != expected {
			t.Fatalf("condition %#x = %v, want %v", condition, got, expected)
		}
	}
}

func openInstalledBackend(t *testing.T) *Backend {
	t.Helper()
	backend, err := New()
	if errors.Is(err, ErrUnavailable) {
		t.Skipf("Unicorn 2.x shared library unavailable: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return backend
}

func TestInstalledBackendContractAndContext(t *testing.T) {
	backend := openInstalledBackend(t)
	if err := backend.Identity().Validate(); err != nil {
		t.Fatal(err)
	}
	if backend.Identity().Name != BackendName || backend.Architecture() != cpu.ARMv5TE {
		t.Fatalf("backend identity = %+v", backend.Identity())
	}
	if status, err := backend.ReadRegister(cpu.RegisterCPSR); err != nil || status != 0 {
		t.Fatalf("initial application status = %#x, err=%v", status, err)
	}
	permissions := cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute
	if err := backend.Map(0x1000, 0x1000, permissions); err != nil {
		t.Fatal(err)
	}
	if err := backend.Map(0x1800, 0x1000, permissions); !errors.Is(err, cpu.ErrInvalidMapping) {
		t.Fatalf("unaligned map error = %v", err)
	}
	code := make([]byte, 8)
	binary.LittleEndian.PutUint32(code[0:4], 0xe3a00007) // MOV r0, #7
	binary.LittleEndian.PutUint32(code[4:8], 0xe1200070) // BKPT
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
	if result.Err != nil || result.Reason != cpu.StopBudget ||
		result.Instructions != 1 || result.PC != 0x1004 {
		t.Fatalf("first run = %+v", result)
	}
	saved, err := backend.SaveContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 99); err != nil {
		t.Fatal(err)
	}
	if err := backend.RestoreContext(saved); err != nil {
		t.Fatal(err)
	}
	value, err := backend.ReadRegister(cpu.RegisterR0)
	if err != nil || value != 7 {
		t.Fatalf("restored r0 = %d, err=%v", value, err)
	}
	result = backend.Run(context.Background(), 0x1004, cpu.ModeARM, 1)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
		result.Instructions != 1 || result.PC != 0x1008 {
		t.Fatalf("breakpoint run = %+v", result)
	}
}

func TestInstalledBackendHonorsCanceledContext(t *testing.T) {
	backend := openInstalledBackend(t)
	permissions := cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute
	if err := backend.Map(0x1000, 0x1000, permissions); err != nil {
		t.Fatal(err)
	}
	code := make([]byte, 4)
	binary.LittleEndian.PutUint32(code, 0xeafffffe) // B 0x1000
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := backend.Run(ctx, 0x1000, cpu.ModeARM, 0)
	if result.Reason != cpu.StopRequested || result.Instructions != 0 ||
		result.PC != 0x1000 || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("canceled run = %+v", result)
	}
}

func TestInstalledBackendRunsThumbAndStopsAtBreakpoint(t *testing.T) {
	backend := openInstalledBackend(t)
	permissions := cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute
	if err := backend.Map(0x1000, 0x1000, permissions); err != nil {
		t.Fatal(err)
	}
	code := []byte{
		0x07, 0x20, // MOVS r0, #7
		0x00, 0xbe, // BKPT
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 1)
	if result.Err != nil || result.Reason != cpu.StopBudget ||
		result.Instructions != 1 || result.PC != 0x1002 {
		t.Fatalf("Thumb instruction run = %+v", result)
	}
	value, err := backend.ReadRegister(cpu.RegisterR0)
	if err != nil || value != 7 {
		t.Fatalf("Thumb r0 = %d, err=%v", value, err)
	}
	saved, err := backend.SaveContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 99); err != nil {
		t.Fatal(err)
	}
	if err := backend.RestoreContext(saved); err != nil {
		t.Fatal(err)
	}
	pc, err := backend.ReadRegister(cpu.RegisterPC)
	if err != nil || pc != 0x1002 {
		t.Fatalf("restored Thumb PC = %#x, err=%v", pc, err)
	}
	status, err := backend.ReadRegister(cpu.RegisterCPSR)
	if err != nil || status&cpu.StatusThumb == 0 {
		t.Fatalf("restored Thumb status = %#x, err=%v", status, err)
	}
	result = backend.Run(context.Background(), 0x1002, cpu.ModeThumb, 1)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
		result.Instructions != 1 || result.PC != 0x1004 {
		t.Fatalf("Thumb breakpoint run = %+v", result)
	}
}

func TestInstalledBackendHonorsStop(t *testing.T) {
	backend := openInstalledBackend(t)
	permissions := cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute
	if err := backend.Map(0x1000, 0x1000, permissions); err != nil {
		t.Fatal(err)
	}
	code := make([]byte, 4)
	binary.LittleEndian.PutUint32(code, 0xeafffffe) // B 0x1000
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	finished := make(chan cpu.Result, 1)
	go func() {
		finished <- backend.Run(context.Background(), 0x1000, cpu.ModeARM, 0)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !backend.running.Load() && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if !backend.running.Load() {
		_ = backend.Stop()
		t.Fatal("Run did not become active")
	}
	if err := backend.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-finished:
		if result.Reason != cpu.StopRequested || result.PC != 0x1000 ||
			!errors.Is(result.Err, cpu.ErrStopped) {
			t.Fatalf("stopped run = %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}
