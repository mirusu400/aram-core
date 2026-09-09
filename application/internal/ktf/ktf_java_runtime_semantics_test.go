package ktf

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

func TestKTFJavaRuntimeReportsAndCollectsActualHeap(t *testing.T) {
	runtime := newTestRuntime(t)
	freeMemory := HostJavaMethod("java/lang/Runtime", "freeMemory", "()J")

	readFree := func() uint64 {
		t.Helper()
		low, err := freeMemory(context.Background(), runtime)
		check(t, err)
		high, err := runtime.CPU.ReadRegister(cpu.RegisterR1)
		check(t, err)
		return uint64(high)<<32 | uint64(low)
	}

	before := readFree()
	if before != uint64(guest.HeapSize) {
		t.Fatalf("initial Runtime.freeMemory = %d, want %d", before, guest.HeapSize)
	}
	// The first allocation begins exactly at HeapBase, a value the conservative
	// collector also sees in runtime metadata. Put the test garbage after it so
	// an address-valued constant cannot conservatively retain the test block.
	heapAlloc(t, runtime, 1, true)
	garbage := heapAlloc(t, runtime, 13, true)
	afterAllocation := readFree()
	if afterAllocation != before-24 {
		t.Fatalf("Runtime.freeMemory after aligned allocations = %d, want %d", afterAllocation, before-24)
	}

	_, err := HostJavaMethod("java/lang/Runtime", "gc", "()V")(
		context.Background(),
		runtime,
	)
	check(t, err)
	if _, exists := runtime.Heap.Root().Allocations[garbage]; exists {
		t.Fatalf("Runtime.gc retained unreachable allocation 0x%08x", garbage)
	}
	if afterGC := readFree(); afterGC <= afterAllocation {
		t.Fatalf("Runtime.freeMemory after gc = %d, want more than %d", afterGC, afterAllocation)
	}
}

func TestKTFJavaRuntimeExitTerminatesAllTasks(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.Tasks = []*Task{{}, {}}
	runtime.PendingJavaCalls = []ktfPendingJavaCall{{instance: 1, name: "run", descriptor: "()V"}}

	_, err := HostJavaMethod("java/lang/Runtime", "exit", "(I)V")(
		context.Background(),
		runtime,
	)
	check(t, err)
	if !runtime.terminationRequested {
		t.Fatal("Runtime.exit did not request termination")
	}
	if len(runtime.PendingJavaCalls) != 0 {
		t.Fatalf("Runtime.exit retained %d pending calls", len(runtime.PendingJavaCalls))
	}
	for index, task := range runtime.Tasks {
		if !task.Done {
			t.Fatalf("Runtime.exit left task %d alive", index)
		}
	}
}

func TestKTFJavaRandomMatchesJavaContracts(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	random := newHostObject(t, runtime, "java/util/Random")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, random))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 42))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, 0))
	_, err := runtime.handleRandomMethod("<init>", "(J)V")
	check(t, err)

	value, err := runtime.handleRandomMethod("nextInt", "()I")
	check(t, err)
	if value != 0xba419d35 {
		t.Fatalf("new Random(42).nextInt() = 0x%08x, want 0xba419d35", value)
	}

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 0))
	_, err = runtime.handleRandomMethod("nextInt", "(I)I")
	if err == nil || runtime.LastJavaThrowName != "java/lang/IllegalArgumentException" {
		t.Fatalf("Random.nextInt(0) error=%v exception=%q", err, runtime.LastJavaThrowName)
	}
}

func TestKTFJavaRandomDefaultConstructorUsesClockSeed(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.TickMS = 0x12345678
	clockSeeded := newHostObject(t, runtime, "java/util/Random")
	explicitlySeeded := newHostObject(t, runtime, "java/util/Random")

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clockSeeded))
	_, err := runtime.handleRandomMethod("<init>", "()V")
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, explicitlySeeded))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, uint32(runtime.TickMS)))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, uint32(runtime.TickMS>>32)))
	_, err = runtime.handleRandomMethod("<init>", "(J)V")
	check(t, err)

	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, clockSeeded))
	clockValue, err := runtime.handleRandomMethod("nextInt", "()I")
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, explicitlySeeded))
	explicitValue, err := runtime.handleRandomMethod("nextInt", "()I")
	check(t, err)
	if clockValue != explicitValue {
		t.Fatalf("default Random seed value = 0x%08x, clock-seeded value = 0x%08x", clockValue, explicitValue)
	}
}
