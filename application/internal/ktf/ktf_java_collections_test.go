package ktf

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
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

// 천공의 기사단 registers its scenes in a Hashtable under new Byte(n) keys and
// later looks one up with another new Byte(n). Hashtable matches keys with
// equals(), so the lookup must find the scene; answering null by identity
// made the title's next putfield throw NullPointerException.
func TestKTFHashtableMatchesBoxedKeysByValue(t *testing.T) {
	runtime := newTestRuntime(t)
	box := func(className, descriptor string, value uint32) uint32 {
		t.Helper()
		instance := newHostObject(t, runtime, className)
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, instance))
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, value))
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 0))
		_, err := HostJavaMethod(className, "<init>", descriptor)(
			context.Background(), runtime,
		)
		check(t, err)
		return instance
	}
	table := newHostObject(t, runtime, "java/util/Hashtable")
	call := func(name, descriptor string, key, value uint32) uint32 {
		t.Helper()
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, table))
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, key))
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, value))
		result, err := runtime.handleHashtableMethod(name, descriptor)
		check(t, err)
		return result
	}
	const (
		put = "(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;"
		get = "(Ljava/lang/Object;)Ljava/lang/Object;"
	)
	call("<init>", "()V", 0, 0)
	scene := newHostObject(t, runtime, "java/lang/Object")
	call("put", put, box("java/lang/Byte", "(B)V", 1), scene)

	if got := call("get", get, box("java/lang/Byte", "(B)V", 1), 0); got != scene {
		t.Fatalf("get(new Byte(1)) = 0x%08x, want 0x%08x", got, scene)
	}
	if got := call("containsKey", "(Ljava/lang/Object;)Z",
		box("java/lang/Byte", "(B)V", 1), 0); got != 1 {
		t.Fatal("containsKey(new Byte(1)) = false")
	}
	if got := call("get", get, box("java/lang/Byte", "(B)V", 2), 0); got != 0 {
		t.Fatalf("get(new Byte(2)) = 0x%08x, want null", got)
	}
	// Integer.equals(Byte) is false even for the same number.
	if got := call("get", get, box("java/lang/Integer", "(I)V", 1), 0); got != 0 {
		t.Fatalf("get(new Integer(1)) = 0x%08x, want null", got)
	}
	replaced := newHostObject(t, runtime, "java/lang/Object")
	if previous := call("put", put, box("java/lang/Byte", "(B)V", 1), replaced); previous != scene {
		t.Fatalf("put over an equal key returned 0x%08x, want 0x%08x", previous, scene)
	}
	if size := call("size", "()I", 0, 0); size != 1 {
		t.Fatalf("size after replacing an equal key = %d, want 1", size)
	}
	if got := call("remove", get, box("java/lang/Byte", "(B)V", 1), 0); got != replaced {
		t.Fatalf("remove(new Byte(1)) = 0x%08x, want 0x%08x", got, replaced)
	}
}

// A save written before boxed keys compared by value named a Byte key by its
// address. Restoring it must name the entry the way lookups now do, or the
// title loses every boxed-key entry it had across a reload.
func TestKTFHashtableRestoreRenamesBoxedKeys(t *testing.T) {
	runtime := newTestRuntime(t)
	key := newHostObject(t, runtime, "java/lang/Byte")
	runtime.integerValues[key] = 1
	table := newHostObject(t, runtime, "java/util/Hashtable")
	scene := newHostObject(t, runtime, "java/lang/Object")
	runtime.hashtables[table] = map[string]ktfHashtableEntry{
		fmt.Sprintf("object:%08x", key): {key: key, value: scene},
	}

	var state bytes.Buffer
	check(t, WriteState(runtime, runtime.CPU, true, guest.NewStateWriter(&state)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(state.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	check(t, err)
	started := false
	check(t, RestoreState(runtime, runtime.CPU, saved, &started))

	lookup := newHostObject(t, runtime, "java/lang/Byte")
	runtime.integerValues[lookup] = 1
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, table))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, lookup))
	got, err := runtime.handleHashtableMethod("get", "(Ljava/lang/Object;)Ljava/lang/Object;")
	check(t, err)
	if got != scene {
		t.Fatalf("restored get(new Byte(1)) = 0x%08x, want 0x%08x", got, scene)
	}
}

func TestKTFHashtableRejectsNullKeysAndValues(t *testing.T) {
	runtime := newTestRuntime(t)
	table := newHostObject(t, runtime, "java/util/Hashtable")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, table))
	_, err := runtime.handleHashtableMethod("<init>", "()V")
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 0))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 1))
	_, err = runtime.handleHashtableMethod(
		"put", "(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;",
	)
	if err == nil || runtime.LastJavaThrowName != "java/lang/NullPointerException" {
		t.Fatalf("null Hashtable key error=%v exception=%q", err, runtime.LastJavaThrowName)
	}
	key := newJavaString(t, runtime, "key")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, key))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 0))
	_, err = runtime.handleHashtableMethod(
		"put", "(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;",
	)
	if err == nil || runtime.LastJavaThrowName != "java/lang/NullPointerException" {
		t.Fatalf("null Hashtable value error=%v exception=%q", err, runtime.LastJavaThrowName)
	}
}
