package skvm

import (
	"bytes"
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestReadyThreadsKeepOrderAndDeferNewWorkers(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	vm.classes["IndexedWorker"] = &runtimeClass{class: &Class{
		Name: "IndexedWorker", SuperName: "java/lang/Thread",
		Methods: []Method{{Name: "run", Descriptor: "()V", MaxLocals: 1, Code: []byte{0xb1}}},
	}}
	first := vm.NewObject("IndexedWorker", nil)
	second := vm.NewObject("IndexedWorker", &threadState{active: true})
	// Construct the older object last: allocation/constructor order must not
	// replace the scheduler's ascending-reference order.
	check(t, vm.setNative(first, &threadState{active: true}))
	var ran []uint32
	var added uint32
	vm.SetTraceHook(func(event TraceEvent) error {
		if event.Method == "run" {
			ran = append(ran, vm.runningThread)
			if added == 0 {
				added = vm.NewObject("IndexedWorker", &threadState{active: true})
			}
		}
		return nil
	})
	check(t, vm.runReadyThreads(context.Background()))
	if !reflect.DeepEqual(ran, []uint32{first, second}) {
		t.Fatalf("first pass ran %v, want [%d %d]", ran, first, second)
	}
	check(t, vm.runReadyThreads(context.Background()))
	if !reflect.DeepEqual(ran, []uint32{first, second, added}) {
		t.Fatalf("second pass ran %v", ran)
	}
}

func TestThreadIndexRestorePaintAndCollection(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	method := Method{Name: "paint", Descriptor: "(Ljavax/microedition/lcdui/Graphics;)V",
		MaxLocals: 2, Code: []byte{0xb1}}
	class := &Class{Name: "IndexedCanvas", SuperName: "javax/microedition/lcdui/Canvas", Methods: []Method{method}}
	vm.classes[class.Name] = &runtimeClass{class: class, static: map[string]Value{}}
	canvas := vm.NewObject(class.Name, nil)
	vm.currentDisplay = canvas
	worker := vm.NewObject("java/lang/Thread", &threadState{active: true, started: true, target: canvas,
		continuation: []*frame{{class: class, method: method, invokePC: -1,
			locals: []Value{ReferenceValue(canvas), ReferenceValue(vm.ScreenGraphics())}}}})
	if !vm.paintInFlight() {
		t.Fatal("parked worker paint was not found")
	}
	before, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.setNative(worker, nil))
	if vm.paintInFlight() {
		t.Fatal("removed thread still suppresses paint")
	}
	check(t, vm.UnmarshalBinary(before))
	if !vm.paintInFlight() {
		t.Fatal("restored worker no longer suppresses overlapping paint")
	}
	after, err := vm.MarshalBinary()
	check(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("derived thread index changed serialized state")
	}
	state, err := vm.thread(worker)
	check(t, err)
	state.active = false
	check(t, vm.collectGarbage())
	if _, exists := vm.Object(worker); exists {
		t.Fatal("inactive unreferenced worker was not collected")
	}
	if _, exists := vm.threadObjects[worker]; exists {
		t.Fatal("collection retained a stale scheduler entry")
	}
	if vm.paintInFlight() {
		t.Fatal("collected worker still suppresses paint")
	}
}

func BenchmarkThreadChecksWithLargeHeap(b *testing.B) {
	for _, objects := range []int{100, 50_000} {
		b.Run(strconv.Itoa(objects), func(b *testing.B) {
			vm, err := New(map[string][]byte{})
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < objects; i++ {
				vm.NewString("unrelated")
			}
			vm.NewObject("java/lang/Thread", &threadState{active: true, wakeAt: time.Hour})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := vm.runReadyThreads(context.Background()); err != nil {
					b.Fatal(err)
				}
				if vm.paintInFlight() {
					b.Fatal("unexpected paint")
				}
			}
		})
	}
}
