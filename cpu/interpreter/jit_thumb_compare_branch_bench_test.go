package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// This isolates the optional tail optimization in a single compiled executor.
// Disabling only warmed metadata is not a historical product-binary benchmark.
// Real corpus comparisons must still use immutable before/after binaries.
func BenchmarkThumbApplicationCompareBranchTail(b *testing.B) {
	for _, program := range []struct {
		name  string
		words []uint16
	}{
		{"immediate-self", []uint16{0x2800, 0xd0fd}},
		{"register-self", []uint16{0x4288, 0xd0fd}},
		{"six-op-prefix", []uint16{0x2111, 0x460c, 0x2407, 0x403c, 0x1c22, 0x0013, 0x2800, 0xd0f7}},
		{"two-successors", thumbApplicationChainCycle},
	} {
		b.Run(program.name, func(b *testing.B) {
			for _, fused := range []bool{false, true} {
				name := "ordinary"
				if fused {
					name = "fused"
				}
				b.Run(name, func(b *testing.B) {
					backend := NewJIT()
					b.Cleanup(func() { _ = backend.Close() })
					check(b, backend.Map(0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
					check(b, backend.WriteMemory(0x1000, thumbTestCode(program.words...)))
					check(b, backend.WriteRegister(cpu.RegisterCPSR, 0x3f))
					const budget = uint64(100_000)
					ctx := context.Background()
					warm := backend.Run(ctx, 0x1000, cpu.ModeThumb, budget)
					if warm.Err != nil || warm.Reason != cpu.StopBudget || warm.Instructions != budget {
						b.Fatalf("warmup: %+v", warm)
					}
					candidates := 0
					for _, block := range backend.jitBlocks {
						if block == nil || block.compareBranchTail == nil {
							continue
						}
						if block.countedLoop != nil || block.extraLoop != nil {
							b.Fatal("synthetic diagnostic unexpectedly matches another accelerator")
						}
						candidates++
						if !fused {
							block.compareBranchTail = nil
						}
					}
					if candidates == 0 {
						b.Fatal("synthetic diagnostic has no recognized compare/branch tail")
					}
					translations := backend.executionStatistics.TranslatedBlocks
					b.ResetTimer()
					for range b.N {
						result := backend.Run(ctx, 0x1000, cpu.ModeThumb, budget)
						if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != budget {
							b.Fatalf("run: %+v", result)
						}
					}
					b.StopTimer()
					if backend.executionStatistics.TranslatedBlocks != translations {
						b.Fatal("unexpected cold translation inside synthetic diagnostic")
					}
					b.ReportMetric(float64(b.N)*float64(budget)/b.Elapsed().Seconds(), "guest-insn/s")
				})
			}
		})
	}
}
