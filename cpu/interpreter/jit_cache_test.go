package interpreter

import "testing"

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
