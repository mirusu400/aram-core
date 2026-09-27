package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Exercise the actual block dispatcher, not only jitBlockAt: alternating PCs
// collide in one two-way set, then a code write changes its generation.
func TestThumbDispatchRunCollisionsAndInvalidationMatchInterpreter(t *testing.T) {
	precise, jit := New(), NewJIT()
	const firstPC = uint32(0x1000)
	secondPC := firstPC + 2*jitCacheSize
	for _, backend := range []*Backend{precise, jit} {
		t.Cleanup(func() { _ = backend.Close() })
		for _, pc := range []uint32{firstPC, secondPC} {
			check(t, backend.Map(pc, 4096, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
		}
		check(t, backend.WriteMemory(firstPC, thumbTestCode(0x3001, 0x4708)))  // adds r0,#1; bx r1
		check(t, backend.WriteMemory(secondPC, thumbTestCode(0x3002, 0x4710))) // adds r0,#2; bx r2
		check(t, backend.WriteRegister(cpu.RegisterR1, secondPC|1))
		check(t, backend.WriteRegister(cpu.RegisterR2, firstPC|1))
	}
	for phase := 0; phase < 3; phase++ {
		if phase == 1 {
			for _, backend := range []*Backend{precise, jit} {
				check(t, backend.WriteMemory(secondPC, thumbTestCode(0x3003)))
			}
		}
		for _, backend := range []*Backend{precise, jit} {
			result := backend.Run(context.Background(), firstPC, cpu.ModeThumb, 8)
			if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != 8 {
				t.Fatalf("phase %d: run = %+v", phase, result)
			}
		}
		for index := uint32(0); index <= cpu.RegisterCPSR; index++ {
			if immediateMemoryRegisterValue(t, precise, index) != immediateMemoryRegisterValue(t, jit, index) {
				t.Fatalf("phase %d: architectural register %d differs", phase, index)
			}
		}
	}
	if got := immediateMemoryRegisterValue(t, jit, cpu.RegisterR0); got != 22 {
		t.Fatalf("collision/invalidation loop r0 = %d, want 22", got)
	}
}

func TestJITDispatchCacheRetainsNegativeTranslation(t *testing.T) {
	backend := NewJIT()
	const pc = uint32(0x1000)

	backend.jitBlocks[pc] = nil
	if block := backend.jitBlockAt(pc); block != nil {
		t.Fatalf("first negative lookup = %p", block)
	}

	sentinel := &jitBlock{start: pc, end: pc + 2}
	backend.jitBlocks[pc] = sentinel
	if block := backend.jitBlockAt(pc); block != nil {
		t.Fatalf("negative cache miss returned map replacement %p", block)
	}

	backend.jitGen++
	if block := backend.jitBlockAt(pc); block != sentinel {
		t.Fatalf("generation refresh = %p, want %p", block, sentinel)
	}
}

func TestJITDispatchCacheRetainsTwoCollidingBlocks(t *testing.T) {
	backend := NewJIT()
	firstPC := uint32(0x1000)
	secondPC := firstPC + 2*jitCacheSize
	first := &jitBlock{start: firstPC, end: firstPC + 2}
	second := &jitBlock{start: secondPC, end: secondPC + 2}
	backend.jitBlocks[firstPC] = first
	backend.jitBlocks[secondPC] = second

	if backend.jitBlockAt(firstPC) != first || backend.jitBlockAt(secondPC) != second {
		t.Fatal("failed to populate colliding cache ways")
	}
	delete(backend.jitBlocks, firstPC)
	delete(backend.jitBlocks, secondPC)

	if block := backend.jitBlockAt(firstPC); block != first {
		t.Fatalf("first colliding lookup = %p, want %p", block, first)
	}
	if block := backend.jitBlockAt(secondPC); block != second {
		t.Fatalf("second colliding lookup = %p, want %p", block, second)
	}
}

func TestARMJITDispatchCacheRetainsTwoCollidingBlocks(t *testing.T) {
	backend := NewJIT()
	firstPC := uint32(0x1000)
	secondPC := firstPC + 4*jitCacheSize
	first := &jitBlock{start: firstPC, end: firstPC + 4}
	second := &jitBlock{start: secondPC, end: secondPC + 4}
	backend.armJITBlocks[firstPC] = first
	backend.armJITBlocks[secondPC] = second

	if backend.armJITBlockAt(firstPC) != first || backend.armJITBlockAt(secondPC) != second {
		t.Fatal("failed to populate colliding ARM cache ways")
	}
	delete(backend.armJITBlocks, firstPC)
	delete(backend.armJITBlocks, secondPC)

	if block := backend.armJITBlockAt(firstPC); block != first {
		t.Fatalf("first colliding ARM lookup = %p, want %p", block, first)
	}
	if block := backend.armJITBlockAt(secondPC); block != second {
		t.Fatalf("second colliding ARM lookup = %p, want %p", block, second)
	}
}
