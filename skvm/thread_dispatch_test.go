package skvm

import (
	"context"
	"testing"
)

func TestThreadSubclassRunWithNullRunnable(t *testing.T) {
	vm, err := New(map[string][]byte{})
	if err != nil {
		t.Fatal(err)
	}
	vm.classes["WorkerThread"] = &runtimeClass{class: &Class{
		Name:      "WorkerThread",
		SuperName: "java/lang/Thread",
		Methods:   []Method{{Name: "run", Descriptor: "()V", MaxLocals: 1, Code: []byte{0xb1}}},
	}}
	thread := vm.NewObject("WorkerThread", &threadState{active: true})
	state, err := vm.thread(thread)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.runThread(context.Background(), thread, state); err != nil {
		t.Fatal(err)
	}
	if state.active {
		t.Fatal("thread remained active after run returned")
	}
}
