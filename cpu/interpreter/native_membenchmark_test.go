//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// blitterCode is a word copy loop (LDR/STR through a register offset) between
// two pages, the shape of the guest software blitters that dominate a heavy
// frame: every iteration is two guest memory accesses. r0/r1 are the source and
// destination bases, r2 the rolling offset, r4 its wrap mask.
var blitterCode = []byte{
	0x83, 0x58, // ldr  r3, [r0, r2]
	0x8b, 0x50, // str  r3, [r1, r2]
	0x04, 0x32, // adds r2, #4
	0x22, 0x40, // ands r2, r4      ; wrap the offset inside the region
	0xfa, 0xe7, // b    .-8
}

// benchBlitter is the memory-heavy counterpart of benchBackend. It is the
// benchmark the native JIT's inline software-TLB path exists for; the
// register-only benchBackend loop cannot show it. When executable is true the
// source and destination live in the same read-write-execute region the code
// runs from - the KTF/WIPI mapping - so it also measures the self-modifying-code
// invalidation path a framebuffer write would otherwise trip on every store.
func benchBlitter(b *testing.B, make func() *Backend, executable bool) {
	backend := make()
	b.Cleanup(func() { _ = backend.Close() })
	rwx := cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute
	rw := cpu.PermissionRead | cpu.PermissionWrite
	if executable {
		if err := backend.Map(0x1000, 0x4000, rwx); err != nil {
			b.Fatal(err)
		}
	} else {
		for _, m := range []struct {
			base  uint32
			perms cpu.Permissions
		}{{0x1000, rwx}, {0x2000, rw}, {0x3000, rw}} {
			if err := backend.Map(m.base, 0x1000, m.perms); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := backend.WriteMemory(0x1000, blitterCode); err != nil {
		b.Fatal(err)
	}
	for id, value := range map[uint32]uint32{
		cpu.RegisterR0: 0x2000, cpu.RegisterR1: 0x3000,
		cpu.RegisterR2: 0, cpu.RegisterR4: 0xffc,
	} {
		if err := backend.WriteRegister(id, value); err != nil {
			b.Fatal(err)
		}
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

func BenchmarkBlitterInterp(b *testing.B) { benchBlitter(b, New, false) }
func BenchmarkBlitterGoJIT(b *testing.B)  { benchBlitter(b, NewJIT, false) }
func BenchmarkBlitterNative(b *testing.B) { benchBlitter(b, NewNativeJIT, false) }

func BenchmarkBlitterRWXInterp(b *testing.B) { benchBlitter(b, New, true) }
func BenchmarkBlitterRWXGoJIT(b *testing.B)  { benchBlitter(b, NewJIT, true) }
func BenchmarkBlitterRWXNative(b *testing.B) { benchBlitter(b, NewNativeJIT, true) }
