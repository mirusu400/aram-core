package raptor

import "sort"

// raptorJavaLinkOrder returns every registered Java class in a deterministic
// order that names a parent before any of its subclasses.
//
// The class table is a map keyed by holder address, and the link pass used to
// range it directly. Go randomizes map iteration, and buildRaptorJavaVTable
// builds a class's parent on demand when it reaches a subclass whose parent
// has no table yet - so whenever the random order happened to reach a subclass
// first, the outer loop reached that parent again afterwards and built it a
// second time. The same title then linked 133 or 134 vtables, allocated 904
// more or fewer bytes of guest heap before its first frame, and every
// allocation address after that point shifted.
//
// That is not a cosmetic difference. 훼밀리마트타이쿤 completes 10000 fuzzed
// frames on seed 75 in the 133-vtable order and dies at frame 5260 in the
// 134-vtable one, so a headless replay of the same (title, seed, frames,
// density) passed or failed depending on nothing but the map's iteration
// start - which is what aram-fuzz's whole report-as-a-repro-line contract
// rests on.
//
// Ordering parents first is also the order the pass wants on its own merits:
// a subclass inherits its parent's resolved slots, so building the parent
// first means the on-demand recursion never fires and no class is built
// twice into a block the first copy leaks.
func raptorJavaLinkOrder(java *JavaRuntime) []*raptorJavaClass {
	if java == nil || len(java.classes) == 0 {
		return nil
	}
	holders := make([]uint32, 0, len(java.classes))
	for holder := range java.classes {
		holders = append(holders, holder)
	}
	sort.Slice(holders, func(i, j int) bool { return holders[i] < holders[j] })

	depths := make(map[uint32]int, len(holders))
	for _, holder := range holders {
		depths[holder] = raptorJavaClassDepth(java, java.classes[holder])
	}
	sort.SliceStable(holders, func(i, j int) bool {
		left, right := holders[i], holders[j]
		if depths[left] != depths[right] {
			return depths[left] < depths[right]
		}
		return left < right
	})

	ordered := make([]*raptorJavaClass, 0, len(holders))
	for _, holder := range holders {
		ordered = append(ordered, java.classes[holder])
	}
	return ordered
}

// raptorJavaClassDepth counts how many ancestors a class has. A parent always
// scores lower than its children, so sorting by it puts parents first; a
// parent chain that loops back on itself stops at the class it repeats rather
// than spinning, the same way every other walk over this chain is bounded.
func raptorJavaClassDepth(java *JavaRuntime, class *raptorJavaClass) int {
	seen := make(map[*raptorJavaClass]bool, 8)
	depth := 0
	for walk := class; walk != nil && depth < 256; walk = java.ClassByName[walk.parentName] {
		if seen[walk] {
			break
		}
		seen[walk] = true
		depth++
	}
	return depth
}
