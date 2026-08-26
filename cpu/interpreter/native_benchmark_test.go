//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Compares the three CPU backends on the translatable hot loop from
// BenchmarkThumbRun (4 ALU + branch, no memory -> best case for the native JIT).
// With self-loop block linking the native backend stays in host code across
// iterations (one syscall.SyscallN per batch instead of per iteration) and beats
// the interpreter. Supported private and whole-system RAM accesses stay native
// through the inline software TLB; MMIO and faults retain the one-instruction
// interpreter fallback. This benchmark is the regression guard for the native
// ALU fast path.
func benchBackend(b *testing.B, make func() *Backend) {
	backend := make()
	b.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(0x1000, 0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		b.Fatal(err)
	}
	if err := backend.WriteMemory(0x1000, []byte{
		0x01, 0x30, // adds r0, #1
		0x01, 0x31, // adds r1, #1
		0x01, 0x32, // adds r2, #1
		0x01, 0x33, // adds r3, #1
		0xfa, 0xe7, // b 0x1000
	}); err != nil {
		b.Fatal(err)
	}
	const budget = uint64(100_000)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		result := backend.Run(ctx, 0x1000, cpu.ModeThumb, budget)
		if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != budget {
			b.Fatalf("run result = %+v", result)
		}
	}
	b.ReportMetric(float64(b.N)*float64(budget)/b.Elapsed().Seconds(), "guest-insn/s")
}

func BenchmarkBackendInterp(b *testing.B) { benchBackend(b, New) }
func BenchmarkBackendGoJIT(b *testing.B)  { benchBackend(b, NewJIT) }
func BenchmarkBackendNative(b *testing.B) { benchBackend(b, NewNativeJIT) }
func BenchmarkARMRunNative(b *testing.B)  { benchmarkARMRun(b, NewNativeJIT) }

func benchmarkWholeSystemThumb(b *testing.B, makeBackend func() *Backend) {
	backend := makeBackend()
	b.Cleanup(func() { _ = backend.Close() })
	bus := &nativeSystemBus{ram: make([]byte, 0x4000), mmio: 0x9000}
	// loop: ldr r2,[r0]; adds r2,#1; str r2,[r0]; adds r3,#1; b loop
	putThumb(bus.ram, 0x1000, 0x6802, 0x3201, 0x6002, 0x3301, 0xe7fa)
	if err := backend.AttachSystemBus(bus); err != nil {
		b.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 0x2000); err != nil {
		b.Fatal(err)
	}
	const budget = uint64(100_000)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		result := backend.Run(ctx, 0x1000, cpu.ModeThumb, budget)
		if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != budget {
			b.Fatalf("run result = %+v", result)
		}
	}
	b.ReportMetric(float64(b.N)*float64(budget)/b.Elapsed().Seconds(), "guest-insn/s")
}

func BenchmarkWholeSystemThumbInterp(b *testing.B) {
	benchmarkWholeSystemThumb(b, New)
}

func BenchmarkWholeSystemThumbNative(b *testing.B) {
	benchmarkWholeSystemThumb(b, NewNativeJIT)
}
