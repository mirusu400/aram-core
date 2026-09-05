package ktf

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// A started thread has to see its own java/lang/Thread from
// Thread.currentThread(). 라피스라줄리 guards its worker with
//
//	if (Thread.currentThread() != this.worker) return;
//
// so answering with one shared host-made Thread left run() on its first
// instruction, every task finished, and the title stopped on a black screen
// after a single present (issue #147).
func TestKTFCurrentThreadAnswersTheRunningTasksThread(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()

	const jletThread = uint32(0x10001000)
	const workerThread = uint32(0x10002000)
	runtime.currentThread = jletThread
	worker := &Task{javaThread: workerThread}

	runtime.activeTask = worker
	thread, err := runtime.handleThreadMethod(
		context.Background(),
		"currentThread",
		"()Ljava/lang/Thread;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if thread != workerThread {
		t.Fatalf("currentThread on the worker = 0x%08x, want 0x%08x",
			thread, workerThread)
	}

	// A task that is not a started thread - a paint, a key event, a timer -
	// keeps answering with the Jlet's own thread.
	runtime.activeTask = &Task{}
	thread, err = runtime.handleThreadMethod(
		context.Background(),
		"currentThread",
		"()Ljava/lang/Thread;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if thread != jletThread {
		t.Fatalf("currentThread on a paint task = 0x%08x, want 0x%08x",
			thread, jletThread)
	}
}

// A thread that waited for a free task slot still has to know its own Thread,
// and the receiver its run() belongs to is either the Thread itself or the
// Runnable the Thread was constructed around.
func TestKTFJavaThreadForResolvesEitherShape(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()

	const subclassed = uint32(0x10003000)
	const wrapper = uint32(0x10004000)
	const runnable = uint32(0x10005000)
	runtime.ThreadTargets[subclassed] = 0
	runtime.ThreadTargets[wrapper] = runnable

	if got := runtime.javaThreadFor(subclassed); got != subclassed {
		t.Fatalf("Thread subclass resolved to 0x%08x, want 0x%08x",
			got, subclassed)
	}
	if got := runtime.javaThreadFor(runnable); got != wrapper {
		t.Fatalf("Runnable resolved to 0x%08x, want 0x%08x", got, wrapper)
	}
	if got := runtime.javaThreadFor(0); got != 0 {
		t.Fatalf("a null receiver resolved to 0x%08x", got)
	}
}
