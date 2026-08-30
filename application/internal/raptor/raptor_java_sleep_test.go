package raptor

import (
	"testing"

	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
)

// TestSleepingJavaTaskIsNotRunnable pins that Thread.sleep actually parks a
// thread. With the sleep a no-op, 현영맞고2006's game loop ran its
// step-and-sleep body 2300 times per frame, so its own sense of elapsed time
// outran the handset clock its state machine waits on (issue #79).
func TestSleepingJavaTaskIsNotRunnable(t *testing.T) {
	public := &wipirt.Runtime{}
	awake := &JavaTask{Procedure: 0x1001}
	asleep := &JavaTask{Procedure: 0x2001, WakeAtMS: 100}
	runtime := &Runtime{
		Public: public,
		Java:   &JavaRuntime{Tasks: []*JavaTask{awake, asleep}},
	}

	public.TickMS = 40
	for slice := 0; slice < 3; slice++ {
		if got := runtime.NextRunnableJavaTask(); got != awake {
			t.Fatalf("slice %d scheduled a sleeping thread", slice)
		}
	}

	// The clock reaching the wake time makes the thread runnable again, and the
	// rotation resumes over both threads.
	public.TickMS = 100
	if got := runtime.NextRunnableJavaTask(); got != asleep {
		t.Fatalf("woken thread was not scheduled: ran 0x%04x", got.Procedure)
	}
	if got := runtime.NextRunnableJavaTask(); got != awake {
		t.Fatalf("rotation did not resume: ran 0x%04x", got.Procedure)
	}

	// Every thread asleep means no Java runs this frame rather than a busy
	// thread being invented.
	awake.WakeAtMS = 200
	asleep.WakeAtMS = 200
	if got := runtime.NextRunnableJavaTask(); got != nil {
		t.Fatalf("all threads asleep but 0x%04x was scheduled", got.Procedure)
	}
}

// TestTakeJavaYieldClears pins the one-shot slice yield a park raises.
func TestTakeJavaYieldClears(t *testing.T) {
	runtime := &Runtime{}
	if runtime.TakeJavaYield() {
		t.Fatal("a fresh runtime reported a pending yield")
	}
	runtime.javaYieldRequested = true
	if !runtime.TakeJavaYield() {
		t.Fatal("a raised yield was not reported")
	}
	if runtime.TakeJavaYield() {
		t.Fatal("the yield was reported twice")
	}
	if (*Runtime)(nil).TakeJavaYield() {
		t.Fatal("a nil runtime reported a pending yield")
	}
}

// TestSetActiveJavaTask keeps the sleep target addressable without a Java host.
func TestSetActiveJavaTask(t *testing.T) {
	runtime := &Runtime{Java: &JavaRuntime{}}
	task := &JavaTask{Procedure: 0x3001}
	runtime.SetActiveJavaTask(task)
	if runtime.Java.activeTask != task {
		t.Fatal("the active thread was not recorded")
	}
	runtime.SetActiveJavaTask(nil)
	if runtime.Java.activeTask != nil {
		t.Fatal("the active thread outlived its slice")
	}
	// A runtime without a Java host must stay quiet rather than panic.
	(&Runtime{}).SetActiveJavaTask(task)
}

// TestRaptorJavaReaderVirtualSlots pins the Reader vtable layout. The slots the
// title dispatches through are read at 0x30 and close at 0x4c; unresolved, both
// fell through to the no-op backstop and every text resource decoded empty.
func TestRaptorJavaReaderVirtualSlots(t *testing.T) {
	for _, className := range []string{"java/io/Reader", "java/io/InputStreamReader"} {
		layout := raptorJavaFixedVirtualMethods[className]
		if len(layout) == 0 {
			t.Fatalf("%s has no fixed vtable layout", className)
		}
		found := map[uint32]string{}
		for _, method := range layout {
			found[method.offset] = method.Name + method.descriptor
		}
		for offset, want := range map[uint32]string{
			0x30: "read([C)I",
			0x34: "read([CII)I",
			0x4c: "close()V",
		} {
			if got := found[offset]; got != want {
				t.Fatalf("%s slot 0x%02x = %q, want %q", className, offset, got, want)
			}
		}
	}
}
