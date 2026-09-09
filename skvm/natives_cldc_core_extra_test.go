package skvm

import (
	"context"
	"testing"
)

func TestCLDCIntegralWrappersAndRadixFormatting(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	long := vm.NewObject("java/lang/Long", nil)
	invokeTestNative(t, vm, "java/lang/Long", "<init>", "(J)V", long, LongValue(0x100000002))
	hash := invokeTestNative(t, vm, "java/lang/Long", "hashCode", "()I", long)
	if got := mustInt(t, hash); got != 3 {
		t.Fatalf("Long.hashCode() = %d", got)
	}
	binary := invokeTestNative(t, vm, "java/lang/Integer", "toBinaryString", "(I)Ljava/lang/String;", 0, IntValue(-1))
	reference, err := binary.Reference()
	check(t, err)
	text, err := vm.String(reference)
	check(t, err)
	if text != "11111111111111111111111111111111" {
		t.Fatalf("Integer.toBinaryString(-1) = %q", text)
	}
}

func TestCLDCStringBufferPrimitiveInsert(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	buffer := vm.NewObject("java/lang/StringBuffer", &stringBufferState{value: "ab"})
	invokeTestNative(t, vm, "java/lang/StringBuffer", "insert", "(IZ)Ljava/lang/StringBuffer;", buffer, IntValue(1), IntValue(1))
	state, err := vm.stringBuffer(buffer)
	check(t, err)
	if state.value != "atrueb" {
		t.Fatalf("StringBuffer.insert = %q", state.value)
	}
}

func TestCLDCRandomBoundedValues(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	random := vm.NewObject("java/util/Random", nil)
	invokeTestNative(t, vm, "java/util/Random", "<init>", "(J)V", random, LongValue(1234))
	for range 100 {
		value := invokeTestNative(t, vm, "java/util/Random", "nextInt", "(I)I", random, IntValue(7))
		got := mustInt(t, value)
		if got < 0 || got >= 7 {
			t.Fatalf("Random.nextInt(7) = %d", got)
		}
	}
}

func TestCLDCObjectCloneCopiesArraysAndRejectsOrdinaryObjects(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	source := vm.NewByteArray([]byte{1, 2, 3})
	clone := mustReference(t, invokeTestNative(t, vm, "java/lang/Object", "clone", "()Ljava/lang/Object;", source))
	cloneObject, _ := vm.Object(clone)
	cloneObject.Array.Elements[0] = IntValue(9)
	original, err := vm.ByteArray(source)
	check(t, err)
	if original[0] != 1 {
		t.Fatalf("array clone aliased source: %v", original)
	}
	plain := vm.NewObject("java/lang/Object", nil)
	native := vm.natives[nativeKey{"java/lang/Object", "clone", "()Ljava/lang/Object;"}]
	_, _, err = native(context.Background(), vm, plain, nil)
	if thrown, ok := err.(*thrown); !ok || thrown.class != "java/lang/CloneNotSupportedException" {
		t.Fatalf("plain Object clone error = %v", err)
	}
}

func TestCLDCThreadCannotRestartAfterCompletion(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	thread := vm.NewObject("java/lang/Thread", nil)
	invokeTestNative(t, vm, "java/lang/Thread", "<init>", "()V", thread)
	invokeTestNative(t, vm, "java/lang/Thread", "start", "()V", thread)
	native := vm.natives[nativeKey{"java/lang/Thread", "start", "()V"}]
	_, _, err = native(context.Background(), vm, thread, nil)
	if thrown, ok := err.(*thrown); !ok || thrown.class != "java/lang/IllegalThreadStateException" {
		t.Fatalf("second Thread.start error = %v", err)
	}
}
