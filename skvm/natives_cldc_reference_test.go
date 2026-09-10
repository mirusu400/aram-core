package skvm

import "testing"

func TestCLDCWeakReferenceClear(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	target := vm.NewObject("java/lang/Object", nil)
	weak := vm.NewObject("java/lang/ref/WeakReference", nil)
	invokeTestNative(t, vm, "java/lang/ref/WeakReference", "<init>", "(Ljava/lang/Object;)V", weak, ReferenceValue(target))
	value := invokeTestNative(t, vm, "java/lang/ref/Reference", "get", "()Ljava/lang/Object;", weak)
	got, err := value.Reference()
	check(t, err)
	if got != target {
		t.Fatalf("WeakReference.get() = %d, want %d", got, target)
	}
	invokeTestNative(t, vm, "java/lang/ref/Reference", "clear", "()V", weak)
	value = invokeTestNative(t, vm, "java/lang/ref/Reference", "get", "()Ljava/lang/Object;", weak)
	got, err = value.Reference()
	check(t, err)
	if got != 0 {
		t.Fatalf("cleared WeakReference.get() = %d", got)
	}
}

func TestCLDCWeakReferenceDoesNotKeepReferentAlive(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	target := vm.NewObject("java/lang/Object", nil)
	weak := vm.NewObject("java/lang/ref/WeakReference", nil)
	invokeTestNative(t, vm, "java/lang/ref/WeakReference", "<init>", "(Ljava/lang/Object;)V", weak, ReferenceValue(target))
	vm.RegisterStaticField("test/Roots", "weak", "Ljava/lang/ref/WeakReference;", ReferenceValue(weak))
	check(t, vm.collectGarbage())
	if _, ok := vm.Object(target); ok {
		t.Fatal("weak referent survived collection")
	}
	value := invokeTestNative(t, vm, "java/lang/ref/Reference", "get", "()Ljava/lang/Object;", weak)
	got, err := value.Reference()
	check(t, err)
	if got != 0 {
		t.Fatalf("collected WeakReference.get() = %d", got)
	}
}
