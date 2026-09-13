package ktf

import (
	"fmt"
	"sort"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Keep the original collector's lookup as an independent test oracle.
func originalKTFHeapLookup(blocks []ktfHeapBlock, address uint32) int {
	index := sort.Search(len(blocks), func(index int) bool {
		return blocks[index].start > address
	}) - 1
	if index < 0 || address >= blocks[index].end {
		return -1
	}
	return index
}

func TestKTFHeapLookupEquivalent(t *testing.T) {
	for name, blocks := range map[string][]ktfHeapBlock{
		"empty":           nil,
		"single":          {{0x100, 0x120}},
		"zero-start":      {{0, 8}},
		"gaps-adjacent":   {{0x100, 0x108}, {0x108, 0x110}, {0x130, 0x150}},
		"page-spanning":   {{0x1ffe, 0x4001}, {0x5000, 0x5020}},
		"upper-addresses": {{0xffffff00, 0xffffff10}, {0xfffffff0, 0xffffffff}},
		// These shapes are not required of normal allocations, but ensure the
		// guard adds no new assumption beyond the original sorted lookup.
		"empty-block": {{0x100, 0x100}},
		"overlap":     {{0x100, 0x200}, {0x180, 0x190}},
		"wrapped-end": {{0xfffffff0, 0}},
	} {
		t.Run(name, func(t *testing.T) {
			lookup := newKTFHeapLookup(blocks)
			addresses := []uint32{0, 1, 3, 0x7fffffff, 0x80000000, 0xfffffffe, 0xffffffff}
			for _, block := range blocks {
				addresses = append(addresses, block.start-1, block.start, block.start+1, block.start+3, block.end-1, block.end, block.end+1)
			}
			for address := uint32(0); address < 0x6000; address++ {
				addresses = append(addresses, address)
			}
			for _, address := range addresses {
				got, want := lookup.find(address), originalKTFHeapLookup(blocks, address)
				if got != want {
					t.Fatalf("address=%08x got=%d want=%d", address, got, want)
				}
				// Collector dead(nonheap) stays false, and duplicate marks add
				// nothing regardless of which block is already marked.
				for marked := -1; marked < len(blocks); marked++ {
					if (got >= 0 && got != marked) != (want >= 0 && want != marked) {
						t.Fatal("mark/dead classification changed")
					}
				}
			}
		})
	}
}

func TestKTFHeapLookupCollectionInteriorRootAndReclaim(t *testing.T) {
	r := newTestRuntime(t)
	allocate := func() uint32 {
		address, err := r.Heap.Allocate(32, true)
		check(t, err)
		if address == 0 {
			t.Fatal("allocation failed")
		}
		return address
	}
	// The runtime itself contains heap-base values, which conservatively retain
	// an allocation starting there. Keep that incidental root out of this case.
	_ = allocate()
	parent, child, garbage := allocate(), allocate(), allocate()
	weakChild, deadChild, nonheapValue := allocate(), allocate(), allocate()
	weak := map[uint32]uint32{child: weakChild + 5, garbage: deadChild, 0: nonheapValue}
	r.AddGCWeakTable(weak)
	check(t, r.WriteU32(parent, child+7))
	check(t, r.CPU.WriteRegister(cpu.RegisterR1, parent+3))
	r.collectJavaHeap()
	for _, address := range []uint32{parent, child, weakChild, nonheapValue} {
		if r.Heap.Root().Allocations[address] == 0 {
			t.Fatalf("lost interior-root reachability at %08x", address)
		}
	}
	if r.Heap.Root().Allocations[garbage] != 0 || r.Heap.Root().Allocations[deadChild] != 0 {
		t.Fatal("unreachable weak key/value survived")
	}
	if _, ok := weak[garbage]; ok {
		t.Fatal("dead weak entry was not pruned")
	}
	check(t, r.CPU.WriteRegister(cpu.RegisterR1, 0))
	r.collectJavaHeap()
	for _, address := range []uint32{parent, child, weakChild} {
		if r.Heap.Root().Allocations[address] != 0 {
			t.Fatalf("unrooted block survived at %08x", address)
		}
	}
	if r.Heap.Root().Allocations[nonheapValue] == 0 || weak[0] != nonheapValue {
		t.Fatal("nonheap weak key changed classification")
	}
	if _, ok := weak[child]; ok {
		t.Fatal("newly dead weak key was not pruned")
	}
}

var ktfLookupBenchmarkSink int

func BenchmarkKTFHeapLookup(b *testing.B) {
	for _, count := range []int{1024, 65536} {
		blocks := make([]ktfHeapBlock, count)
		for i := range blocks {
			start := uint32(0x10000000 + i*64)
			blocks[i] = ktfHeapBlock{start, start + 32}
		}
		lookup := newKTFHeapLookup(blocks)
		for _, mix := range []string{"nonpointer", "mixed", "interior", "gaps"} {
			values := make([]uint32, 4096)
			for i := range values {
				block := blocks[(i*37)%count]
				switch mix {
				case "nonpointer":
					values[i] = uint32(i * 4)
				case "mixed":
					if i%8 == 0 {
						values[i] = block.start + 7
					} else {
						values[i] = uint32(i * 4)
					}
				case "interior":
					values[i] = block.start + 7
				case "gaps":
					values[i] = block.end + 7
				}
			}
			for _, candidate := range []bool{false, true} {
				b.Run(fmt.Sprintf("blocks=%d/%s/guard=%t", count, mix, candidate), func(b *testing.B) {
					b.ReportAllocs()
					sum := 0
					for i := 0; i < b.N; i++ {
						address := values[i%len(values)]
						if candidate {
							sum += lookup.find(address)
						} else {
							sum += originalKTFHeapLookup(blocks, address)
						}
					}
					ktfLookupBenchmarkSink = sum
				})
			}
		}
		b.Run(fmt.Sprintf("blocks=%d/build-envelope", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ktfLookupBenchmarkSink = int(newKTFHeapLookup(blocks).upper)
			}
		})
	}
}
