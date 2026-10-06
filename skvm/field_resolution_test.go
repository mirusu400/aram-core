package skvm

import "testing"

func TestFieldOwnerResolvesInheritedAndShadowedFields(t *testing.T) {
	vm := &VM{classes: map[string]*runtimeClass{
		"Base":   {class: &Class{Name: "Base", Fields: []Field{{Name: "worker", Descriptor: "Ljava/lang/Thread;"}}}},
		"Child":  {class: &Class{Name: "Child", SuperName: "Base"}},
		"Shadow": {class: &Class{Name: "Shadow", SuperName: "Base", Fields: []Field{{Name: "worker", Descriptor: "Ljava/lang/Thread;"}}}},
	}}
	for _, test := range []struct {
		class string
		want  string
	}{
		{"Child", "Base"},
		{"Shadow", "Shadow"},
		{"java/lang/Thread", "java/lang/Thread"},
	} {
		ref := Reference{Kind: ReferenceField, Class: test.class, Name: "worker", Descriptor: "Ljava/lang/Thread;"}
		if got := vm.fieldOwner(ref); got != test.want {
			t.Errorf("%s.worker owner = %s, want %s", test.class, got, test.want)
		}
	}
}
