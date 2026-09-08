package raptor

import "testing"

// linkOrderRuntime builds a class graph shaped like the one a linked Raptor
// title produces: a root, two children of it, and a grandchild, registered
// under holder addresses that do not follow the inheritance order.
func linkOrderRuntime() *JavaRuntime {
	root := &raptorJavaClass{Name: "Root", Holder: 0x3000}
	childA := &raptorJavaClass{Name: "ChildA", Holder: 0x1000, parentName: "Root"}
	childB := &raptorJavaClass{Name: "ChildB", Holder: 0x4000, parentName: "Root"}
	grandchild := &raptorJavaClass{Name: "Grandchild", Holder: 0x2000, parentName: "ChildA"}
	orphan := &raptorJavaClass{Name: "Orphan", Holder: 0x5000, parentName: "Missing"}
	java := &JavaRuntime{
		classes:     map[uint32]*raptorJavaClass{},
		ClassByName: map[string]*raptorJavaClass{},
	}
	for _, class := range []*raptorJavaClass{root, childA, childB, grandchild, orphan} {
		java.classes[class.Holder] = class
		java.ClassByName[class.Name] = class
	}
	return java
}

// TestRaptorJavaLinkOrderIsStable pins the property the link pass depends on:
// the same class table always produces the same sequence. Go randomizes map
// iteration per range statement, so ranging the table directly produced a
// different vtable build order - and a different number of vtables - on every
// run of the same title, seed and frame count.
func TestRaptorJavaLinkOrderIsStable(t *testing.T) {
	java := linkOrderRuntime()
	first := raptorJavaLinkOrder(java)
	if len(first) != len(java.classes) {
		t.Fatalf("link order covers %d of %d classes", len(first), len(java.classes))
	}
	for attempt := 0; attempt < 64; attempt++ {
		again := raptorJavaLinkOrder(java)
		if len(again) != len(first) {
			t.Fatalf("attempt %d ordered %d classes, first ordered %d",
				attempt, len(again), len(first))
		}
		for index := range first {
			if again[index] != first[index] {
				t.Fatalf("attempt %d position %d is %s, first run had %s",
					attempt, index, again[index].Name, first[index].Name)
			}
		}
	}
}

// TestRaptorJavaLinkOrderNamesParentsFirst is why the order was chosen:
// buildRaptorJavaVTable builds a parent on demand when it meets a subclass
// first, and the link loop then built that parent a second time. Parents
// first means each class is built once.
func TestRaptorJavaLinkOrderNamesParentsFirst(t *testing.T) {
	java := linkOrderRuntime()
	position := make(map[string]int)
	for index, class := range raptorJavaLinkOrder(java) {
		position[class.Name] = index
	}
	for child, parent := range map[string]string{
		"ChildA":     "Root",
		"ChildB":     "Root",
		"Grandchild": "ChildA",
	} {
		if position[parent] >= position[child] {
			t.Fatalf("%s is linked at %d, after its subclass %s at %d",
				parent, position[parent], child, position[child])
		}
	}
}

// TestRaptorJavaClassDepthStopsOnACycle keeps a malformed descriptor chain -
// a class named as its own ancestor - from spinning the ordering pass.
func TestRaptorJavaClassDepthStopsOnACycle(t *testing.T) {
	loop := &raptorJavaClass{Name: "Loop", Holder: 0x10, parentName: "Loop"}
	mutual := &raptorJavaClass{Name: "Mutual", Holder: 0x20, parentName: "Other"}
	other := &raptorJavaClass{Name: "Other", Holder: 0x30, parentName: "Mutual"}
	java := &JavaRuntime{
		classes: map[uint32]*raptorJavaClass{
			loop.Holder: loop, mutual.Holder: mutual, other.Holder: other,
		},
		ClassByName: map[string]*raptorJavaClass{
			"Loop": loop, "Mutual": mutual, "Other": other,
		},
	}
	if depth := raptorJavaClassDepth(java, loop); depth != 1 {
		t.Fatalf("self-parented class has depth %d, want 1", depth)
	}
	if depth := raptorJavaClassDepth(java, mutual); depth != 2 {
		t.Fatalf("mutually parented class has depth %d, want 2", depth)
	}
	if ordered := raptorJavaLinkOrder(java); len(ordered) != 3 {
		t.Fatalf("cyclic graph ordered %d classes, want 3", len(ordered))
	}
}
