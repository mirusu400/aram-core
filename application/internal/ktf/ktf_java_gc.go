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
//
// The one thing that is not a root is a host table keyed by a guest object:
// those record something *about* an object the guest reaches by its own route,
// so their keys are weak and their values live only for as long as their key
// does. See weakTables - treating those keys as roots kept every Java object a
// title ever made alive, which is what this collector was written to stop.

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

	weak := r.weakTables()
	skip := make(map[uintptr]bool, len(weak))
	for _, table := range weak {
		skip[table.Pointer()] = true
	}
	r.markHostRoots(mark, skip)
	r.markRegionRoots(mark)

	// Transitive closure: a marked block's own words are references too.
	scratch := make([]byte, 0, 4096)
	drainPending := func() {
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
	}
	drainPending()

	// A weak table's key is not a root, so its entries are settled only once
	// the strong closure is complete: an entry whose key survived it holds
	// values that are live, and an entry whose key did not is itself garbage.
	// Marking a value can revive another table's key, so this repeats until a
	// pass adds nothing.
	dead := func(address uint32) bool {
		index := sort.Search(len(blocks), func(index int) bool {
			return blocks[index].start > address
		}) - 1
		// An address that is no heap block of ours is not something this
		// collection can prove dead.
		return index >= 0 && address < blocks[index].end && !marked[index]
	}
	seen := make(map[uintptr]bool)
	seenWeak := make(map[weakEntry]bool)
	for {
		progress := false
		for _, table := range weak {
			iterator := table.MapRange()
			for iterator.Next() {
				key := uint32(iterator.Key().Uint())
				if dead(key) {
					continue
				}
				entry := weakEntry{table: table.Pointer(), key: key}
				if seenWeak[entry] {
					continue
				}
				seenWeak[entry] = true
				markValue(iterator.Value(), mark, seen, 1, skip)
				progress = true
			}
		}
		if !progress {
			break
		}
		drainPending()
	}

	// The entries left over describe objects nothing can reach. Dropping them
	// is what lets the block go, and it keeps the host-side table from growing
	// for as long as the title runs.
	pruned := 0
	for _, table := range weak {
		for _, key := range table.MapKeys() {
			if !dead(uint32(key.Uint())) {
				continue
			}
			table.SetMapIndex(key, reflect.Value{})
			pruned++
		}
	}

	deadBlocks := make([]uint32, 0, len(blocks))
	for index, block := range blocks {
		if !marked[index] {
			deadBlocks = append(deadBlocks, block.start)
		}
	}
	freed := root.ReleaseAll(deadBlocks)
	r.tracef("java_heap_collect:blocks=%d:freed=%d:pruned=%d", len(blocks), freed, pruned)
	return freed
}

// AddGCRootRegion names an additional guest memory range markRegionRoots
// scans for live heap pointers, alongside the client image, the stack, and
// low work RAM.
//
// A shared Java host (the Raptor bridge builds one with a one-byte dummy
// Client, since the real image belongs to the Raptor runtime that owns the
// address space) has no client image of its own for markRegionRoots to
// cover, so any reference the collector can only reach through Raptor's own
// statics - its .data and .bss sections - was invisible to the root walk.
// The collector could then reclaim a heap block a live Raptor object field
// still named, and reading it back later found whatever unrelated data had
// since been written there instead (당신은골프왕 crashes dereferencing a
// vtable pointer that reads as unrelated heap bytes for exactly this reason).
func (r *Runtime) AddGCRootRegion(base, size uint32) {
	if size == 0 {
		return
	}
	r.incrementalMemory = append(
		r.incrementalMemory,
		ktfIncrementalMemoryRegion{base: base, size: size},
	)
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
// being complete. The weak tables are the exception and are named by hand, so
// skip carries them and the ephemeron pass settles them instead.
func (r *Runtime) markHostRoots(mark func(uint32), skip map[uintptr]bool) {
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
	markValue(reflect.ValueOf(r), mark, seen, 0, skip)
}

// markValue is the reflective walk. It reads only, so unexported fields are
// fine; it never calls Interface() or Set.
func markValue(
	value reflect.Value,
	mark func(uint32),
	seen map[uintptr]bool,
	depth int,
	skip map[uintptr]bool,
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
		markValue(value.Elem(), mark, seen, depth+1, skip)
	case reflect.Struct:
		// The heap's own allocation table lists every block by address, so
		// walking it would mark the entire heap and collect nothing.
		if value.Type() == reflect.TypeOf(guest.Heap{}) {
			return
		}
		for index := 0; index < value.NumField(); index++ {
			markValue(value.Field(index), mark, seen, depth+1, skip)
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
			markValue(value.Index(index), mark, seen, depth+1, skip)
		}
	case reflect.Map:
		if value.IsNil() {
			return
		}
		if skip[value.Pointer()] {
			// A weak table. Its keys are not roots and its values are live
			// only for as long as their key is, so the ephemeron pass in
			// collectJavaHeap settles it once the strong closure is done.
			return
		}
		iterator := value.MapRange()
		for iterator.Next() {
			markValue(iterator.Key(), mark, seen, depth+1, skip)
			markValue(iterator.Value(), mark, seen, depth+1, skip)
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

// weakEntry names one entry of one weak table, so the ephemeron pass can tell
// an entry it has already followed from one a later round revived.
type weakEntry struct {
	table uintptr
	key   uint32
}

// weakTables are the host-side tables keyed by a guest object's address that
// hold state *about* an object rather than a reference *to* it: the text behind
// a String, the pixels behind a Graphics, the elements of a Vector. The guest
// reaches these objects through its own fields and stack; the table is only how
// the host finds what it recorded. Treating a key here as a root is what kept
// every Java object a title ever made alive - 트랜스포머 calls
// Image.getGraphics() a few hundred times a frame, and two million dead
// Graphics filled the heap while the collector was unable to touch one of them.
//
// A table belongs here only if its key is a Java object the guest allocated and
// its value carries no host resource that has to be released by hand. Leaving a
// table out is safe - it keeps behaving as a root and retains a little garbage;
// putting one in that does not belong is not, so the streams, the media clips,
// the images with their surfaces, the LWC components and everything about a
// class or a thread stay strong.
func (r *Runtime) weakTables() []reflect.Value {
	candidates := []any{
		r.JavaStrings,
		r.stringBuffers,
		r.stringBuffersConsumed,
		r.integerValues,
		r.longValues,
		r.dates,
		r.randomSeeds,
		r.throwableMessages,
		r.Vectors,
		r.hashtables,
		r.enumerations,
		r.Graphics,
		r.GraphicsServices,
	}
	tables := make([]reflect.Value, 0, len(candidates))
	for _, candidate := range candidates {
		value := reflect.ValueOf(candidate)
		if !value.IsValid() || value.IsNil() {
			continue
		}
		tables = append(tables, value)
	}
	return tables
}

// WeakTableEntriesForTest answers how many entries the weak host tables hold,
// so a test can watch a collection drop the dead ones.
func (r *Runtime) WeakTableEntriesForTest() int {
	total := 0
	for _, table := range r.weakTables() {
		total += table.Len()
	}
	return total
}
