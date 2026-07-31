package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func BenchmarkThumbRun(b *testing.B) {
	backend := New()
	b.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(
		0x1000,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
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
		if result.Err != nil ||
			result.Reason != cpu.StopBudget ||
			result.Instructions != budget {
			b.Fatalf("run result = %+v", result)
		}
	}
	b.ReportMetric(
		float64(b.N)*float64(budget)/b.Elapsed().Seconds(),
		"guest-insn/s",
	)
}
