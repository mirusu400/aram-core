package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// benchmarkARMRun mirrors BenchmarkThumbRun's 4-ALU-plus-branch shape so the
// precise decoder and both backends' portable ARM block translator remain
// directly comparable.
func benchmarkARMRun(b *testing.B, newBackend func() *Backend) {
	backend := newBackend()
	b.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(0x1000, 0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		b.Fatal(err)
	}
	if err := backend.WriteMemory(0x1000, []byte{
		0x01, 0x00, 0x90, 0xe2, // adds r0, r0, #1
		0x01, 0x10, 0x91, 0xe2, // adds r1, r1, #1
		0x01, 0x20, 0x92, 0xe2, // adds r2, r2, #1
		0x01, 0x30, 0x93, 0xe2, // adds r3, r3, #1
		0xfa, 0xff, 0xff, 0xea, // b 0x1000
	}); err != nil {
		b.Fatal(err)
	}
	const budget = uint64(100_000)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		result := backend.Run(ctx, 0x1000, cpu.ModeARM, budget)
		if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != budget {
			b.Fatalf("run result = %+v", result)
		}
	}
	b.ReportMetric(float64(b.N)*float64(budget)/b.Elapsed().Seconds(), "guest-insn/s")
}

func BenchmarkARMRun(b *testing.B)      { benchmarkARMRun(b, New) }
func BenchmarkARMRunGoJIT(b *testing.B) { benchmarkARMRun(b, NewJIT) }
