package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestKTFVectorTracksCapacityAndIndexedSearch(t *testing.T) {
	runtime := newTestRuntime(t)
	vector := newHostObject(t, runtime, "java/util/Vector")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, vector))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 2))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 3))
	_, err := runtime.handleVectorMethod("<init>", "(II)V")
	check(t, err)
	for _, value := range []uint32{10, 20, 10} {
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, value))
		_, err = runtime.handleVectorMethod("addElement", "(Ljava/lang/Object;)V")
		check(t, err)
	}
	capacity, err := runtime.handleVectorMethod("capacity", "()I")
	check(t, err)
	if capacity != 5 {
		t.Fatalf("grown vector capacity = %d, want 5", capacity)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 10))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 1))
	index, err := runtime.handleVectorMethod("indexOf", "(Ljava/lang/Object;I)I")
	check(t, err)
	if index != 2 {
		t.Fatalf("indexOf(value,1) = %d, want 2", index)
	}
	_, err = runtime.handleVectorMethod("trimToSize", "()V")
	check(t, err)
	capacity, err = runtime.handleVectorMethod("capacity", "()I")
	check(t, err)
	if capacity != 3 {
		t.Fatalf("trimmed vector capacity = %d", capacity)
	}
}

func TestKTFCollectionsThrowDocumentedBoundsAndEmptyErrors(t *testing.T) {
	runtime := newTestRuntime(t)
	vector := newHostObject(t, runtime, "java/util/Vector")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, vector))
	_, err := runtime.handleVectorMethod("<init>", "()V")
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 0))
	_, err = runtime.handleVectorMethod("elementAt", "(I)Ljava/lang/Object;")
	if err == nil || runtime.LastJavaThrowName != "java/lang/ArrayIndexOutOfBoundsException" {
		t.Fatalf("elementAt error=%v exception=%q", err, runtime.LastJavaThrowName)
	}
	_, err = runtime.handleVectorMethod("pop", "()Ljava/lang/Object;")
	if err == nil || runtime.LastJavaThrowName != "java/util/EmptyStackException" {
		t.Fatalf("pop error=%v exception=%q", err, runtime.LastJavaThrowName)
	}
	enumeration, err := runtime.newJavaEnumeration(nil)
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, enumeration))
	_, err = runtime.handleEnumerationMethod("nextElement", "()Ljava/lang/Object;")
	if err == nil || runtime.LastJavaThrowName != "java/util/NoSuchElementException" {
		t.Fatalf("nextElement error=%v exception=%q", err, runtime.LastJavaThrowName)
	}
}
