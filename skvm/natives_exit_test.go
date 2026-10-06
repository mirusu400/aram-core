package skvm

import (
	"context"
	"errors"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestSKVMTitleExitStopsGuestExecution(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	// System.exit ends the title. It unwinds the guest call stack with
	// ErrHalted, and from then on Advance moves virtual time without running
	// guest code, so the host keeps presenting the last frame the title drew
	// instead of reporting a machine fault.
	native := vm.natives[nativeKey{
		class: "java/lang/System", name: "exit", descriptor: "(I)V",
	}]
	if native == nil {
		t.Fatal("java/lang/System.exit(I)V is missing")
	}
	if _, _, err := native(
		context.Background(), vm, 0, []Value{IntValue(0)},
	); !errors.Is(err, ErrHalted) {
		t.Fatalf("System.exit error = %v, want ErrHalted", err)
	}
	if !vm.Halted() {
		t.Fatal("System.exit did not halt the VM")
	}
	thread := vm.NewObject("java/lang/Thread", &threadState{active: true})
	if err := vm.Advance(context.Background(), time.Millisecond, nil); err != nil {
		t.Fatalf("Advance after exit: %v", err)
	}
	object, _ := vm.Object(thread)
	if state, ok := object.Native.(*threadState); !ok || !state.active {
		t.Fatal("Advance ran guest threads after the title exited")
	}
	// A stopped MIDlet can still have pending input and timers, while the host
	// continues advancing time to present its final frame. Those events must
	// never accumulate until the bounded service queue rejects a frame.
	otherOwner := vm.serviceOwner + 1
	if _, err := vm.services.Events.Enqueue(shared.Event{
		Owner: otherOwner, Kind: shared.EventApplication, At: time.Hour,
	}); err != nil {
		t.Fatal(err)
	}
	// Older save states can resume with an already full queue. Clear the
	// ended title's entries before the next services.Advance adds anything.
	for index := 0; index < 1023; index++ {
		if _, err := vm.services.Events.Enqueue(shared.Event{
			Owner: vm.serviceOwner, Kind: shared.EventApplication,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for frame := 0; frame < 1100; frame++ {
		if frame != 0 {
			_, err := vm.services.Events.Enqueue(shared.Event{
				Owner: vm.serviceOwner, Kind: shared.EventInputPress,
				Control: "ok", At: vm.services.Clock.Monotonic(),
			})
			if err != nil {
				t.Fatalf("enqueue stopped title event at frame %d: %v", frame, err)
			}
		}
		if err := vm.Advance(context.Background(), time.Millisecond, nil); err != nil {
			t.Fatalf("advance stopped title frame %d: %v", frame, err)
		}
		if events := vm.services.Events.Snapshot().Events; len(events) != 1 || events[0].Owner != otherOwner {
			t.Fatalf("frame %d retained %d events, want only the other owner's event", frame, len(events))
		}
	}
}

func TestSKVMMIDletNotifyDestroyedStopsGuestExecution(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	midlet := vm.NewObject("javax/microedition/midlet/MIDlet", nil)
	worker := vm.NewObject("java/lang/Thread", &threadState{active: true})
	native := vm.natives[nativeKey{
		class: "javax/microedition/midlet/MIDlet", name: "notifyDestroyed", descriptor: "()V",
	}]
	if native == nil {
		t.Fatal("MIDlet.notifyDestroyed()V is missing")
	}
	if _, _, err := native(context.Background(), vm, midlet, nil); !errors.Is(err, ErrHalted) {
		t.Fatalf("notifyDestroyed error = %v, want ErrHalted", err)
	}
	object, _ := vm.Object(midlet)
	if state, _ := object.Fields[midletLifecycleField].Int(); state != 2 {
		t.Fatalf("MIDlet lifecycle = %d, want destroyed", state)
	}
	if !vm.Halted() {
		t.Fatal("notifyDestroyed did not halt the VM")
	}
	if err := vm.Advance(context.Background(), time.Millisecond, nil); err != nil {
		t.Fatalf("Advance after notifyDestroyed: %v", err)
	}
	object, _ = vm.Object(worker)
	if state, ok := object.Native.(*threadState); !ok || !state.active {
		t.Fatal("Advance ran a worker after notifyDestroyed")
	}
}
