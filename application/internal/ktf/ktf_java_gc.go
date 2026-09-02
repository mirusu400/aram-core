package ktf

import (
	"encoding/binary"
	"reflect"
	"sort"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

// KTF Java has no collector of its own: every object, array and String a host
// handler builds comes out of the guest heap and used to stay there for as long
// as the title ran. A title that formats a string in its draw loop allocates a
// few hundred blocks a frame, so the 32 MiB heap was gone in about two and a
// half minutes and the title died where a handset would simply have carried on.
//
// This is a conservative mark-sweep, and it runs only when an allocation has
// already failed. That ordering is the safety argument: a healthy title never
// enters it, so a mistake here can only change how an already-dead title dies.
//
// Conservative means a word that happens to hold a value inside a live block
// keeps that block, whether or not it is really a pointer. It also means an
// interior pointer counts - guest code walking an array holds a pointer past
// its start, and treating that as a reference is the difference between
// retaining a little garbage and freeing something still in use.

// ktfHeapBlock is one allocation, as a half-open address range.
type ktfHeapBlock struct {
	start uint32
	end   uint32
}

// ktfGCScanLimit bounds one region read, so scanning does not build a copy of
// a whole 13 MiB region in one allocation.
const ktfGCScanLimit = 1 << 20

// ktfGCGrowthBeforeRetry is how many allocations must accumulate after one
// collection before another is worth its cost. A string-building title
// allocates a few hundred blocks a frame, so this is several seconds of guest
// time rather than a stall on every allocation.
const ktfGCGrowthBeforeRetry = 65536

// allocateJavaHeapBytes allocates from the guest heap, collecting once and
// retrying if the heap is full.
func (r *Runtime) allocateJavaHeapBytes(size uint32, clear bool) (uint32, error) {
	address, err := r.Heap.Allocate(size, clear)
	if err != nil {
		return 0, err
	}
	if address != 0 {
		return address, nil
	}
	// A collection costs a scan of every mapped region and every live block, so
	// a title whose live set genuinely fills the heap must not pay for one on
	// every allocation from here on. Wait until it has allocated its way
	// meaningfully past the last collection before trying again.
	live := len(r.Heap.Root().Allocations)
	if r.javaHeapCollected != 0 && live < r.javaHeapCollected+ktfGCGrowthBeforeRetry {
		return 0, nil
	}
	freed := r.collectJavaHeap()
	r.javaHeapCollected = len(r.Heap.Root().Allocations)
	if freed == 0 {
		return 0, nil
	}
	return r.Heap.Allocate(size, clear)
}

// collectJavaHeap frees every heap block no root can reach, and answers how
// many it freed.
func (r *Runtime) collectJavaHeap() int {
	root := r.Heap.Root()
	if len(root.Allocations) == 0 {
		return 0
	}
	blocks := make([]ktfHeapBlock, 0, len(root.Allocations))
	for address, size := range root.Allocations {
		blocks = append(blocks, ktfHeapBlock{start: address, end: address + size})
	}
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].start < blocks[j].start
	})

	marked := make([]bool, len(blocks))
	pending := make([]int, 0, 1024)
	mark := func(address uint32) {
		// The largest block still has to be found by its start, so search for
		// the last block that begins at or before the address.
		index := sort.Search(len(blocks), func(index int) bool {
			return blocks[index].start > address
		}) - 1
		if index < 0 || address >= blocks[index].end || marked[index] {
			return
		}
		marked[index] = true
		pending = append(pending, index)
	}

	r.markHostRoots(mark)
	r.markRegionRoots(mark)

	// Transitive closure: a marked block's own words are references too.
	scratch := make([]byte, 0, 4096)
	for len(pending) != 0 {
		index := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		block := blocks[index]
		size := block.end - block.start
		if size > uint32(cap(scratch)) {
			scratch = make([]byte, size)
		}
		buffer := scratch[:size]
		if err := r.CPU.ReadMemory(block.start, buffer); err != nil {
			// A block we cannot read is a block we cannot prove dead.
			continue
		}
		scanWords(buffer, mark)
	}

	dead := make([]uint32, 0, len(blocks))
	for index, block := range blocks {
		if !marked[index] {
			dead = append(dead, block.start)
		}
	}
	freed := root.ReleaseAll(dead)
	r.tracef("java_heap_collect:blocks=%d:freed=%d", len(blocks), freed)
	return freed
}

// markRegionRoots scans every mapped region that is not the heap itself. The
// client image holds the title's statics, the stack holds every live frame,
// and low work RAM is where KTF titles keep their own structures.
func (r *Runtime) markRegionRoots(mark func(uint32)) {
	regions := []ktfIncrementalMemoryRegion{
		{base: ImageBase, size: r.ImageSz},
		{base: guest.DefaultStackBase, size: guest.DefaultStackSize},
		{base: LowWorkRAMBase, size: LowWorkRAMSize},
	}
	regions = append(regions, r.incrementalMemory...)
	buffer := make([]byte, ktfGCScanLimit)
	for _, region := range regions {
		if region.size == 0 {
			continue
		}
		for offset := uint32(0); offset < region.size; {
			count := min(uint32(len(buffer)), region.size-offset)
			window := buffer[:count]
			if err := r.CPU.ReadMemory(region.base+offset, window); err != nil {
				break
			}
			scanWords(window, mark)
			offset += count
		}
	}
}

// markHostRoots walks the runtime itself. Dozens of host-side maps are keyed by
// a guest object's address - the images, the clips, the graphics contexts, the
// timer tasks - and every one of them is a reference the guest can still reach.
// Walking by reflection rather than by hand is deliberate: a map added later is
// a root the moment it exists, where a hand-written list would quietly stop
// being complete.
func (r *Runtime) markHostRoots(mark func(uint32)) {
	// A task's registers live in the backend's own context until they are
	// marshalled, and a suspended task's receiver may be held in nothing else.
	for _, task := range r.Tasks {
		if task == nil {
			continue
		}
		_ = r.materializeTaskContext(task)
		// A suspended task's whole register file lives in these bytes, and a
		// receiver it is about to return to may be held in nothing else. The
		// reflective walk skips byte slices, so this one is scanned by hand.
		scanWords(task.Context, mark)
	}
	for index := uint32(0); index <= 15; index++ {
		if value, err := r.CPU.ReadRegister(index); err == nil {
			mark(value)
		}
	}
	seen := make(map[uintptr]bool)
	markValue(reflect.ValueOf(r), mark, seen, 0)
}

// markValue is the reflective walk. It reads only, so unexported fields are
// fine; it never calls Interface() or Set.
func markValue(
	value reflect.Value,
	mark func(uint32),
	seen map[uintptr]bool,
	depth int,
) {
	if depth > 8 || !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Uint32:
		mark(uint32(value.Uint()))
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return
		}
		if value.Kind() == reflect.Pointer {
			address := value.Pointer()
			if seen[address] {
				return
			}
			seen[address] = true
		}
		markValue(value.Elem(), mark, seen, depth+1)
	case reflect.Struct:
		// The heap's own allocation table lists every block by address, so
		// walking it would mark the entire heap and collect nothing.
		if value.Type() == reflect.TypeOf(guest.Heap{}) {
			return
		}
		for index := 0; index < value.NumField(); index++ {
			markValue(value.Field(index), mark, seen, depth+1)
		}
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return
		}
		switch value.Type().Elem().Kind() {
		case reflect.Uint8, reflect.String, reflect.Bool:
			return
		}
		for index := 0; index < value.Len(); index++ {
			markValue(value.Index(index), mark, seen, depth+1)
		}
	case reflect.Map:
		if value.IsNil() {
			return
		}
		iterator := value.MapRange()
		for iterator.Next() {
			markValue(iterator.Key(), mark, seen, depth+1)
			markValue(iterator.Value(), mark, seen, depth+1)
		}
	}
}

func scanWords(buffer []byte, mark func(uint32)) {
	for offset := 0; offset+4 <= len(buffer); offset += 4 {
		mark(binary.LittleEndian.Uint32(buffer[offset:]))
	}
}

// CollectJavaHeapForTest runs a collection on demand. Collections normally
// happen only when the heap is full, which is far too rare to test against, so
// a test can force one and check that a healthy title does not notice.
func (r *Runtime) CollectJavaHeapForTest() int { return r.collectJavaHeap() }
