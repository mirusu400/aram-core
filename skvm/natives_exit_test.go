package skvm

import (
	"context"
	"errors"
	"testing"
	"time"
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
}
