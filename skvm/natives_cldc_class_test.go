package skvm

import "testing"

func TestCLDCClassIntrospectionForHostTypes(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	class := vm.NewObject("java/lang/Class", "java/io/DataInput")
	value := invokeTestNative(t, vm, "java/lang/Class", "isInterface", "()Z", class)
	got, err := value.Int()
	check(t, err)
	if got != 1 {
		t.Fatal("DataInput was not reported as an interface")
	}
	name := invokeTestNative(t, vm, "java/lang/Class", "getName", "()Ljava/lang/String;", class)
	nameReference, err := name.Reference()
	check(t, err)
	text, err := vm.String(nameReference)
	check(t, err)
	if text != "java.io.DataInput" {
		t.Fatalf("Class.getName() = %q", text)
	}
}
