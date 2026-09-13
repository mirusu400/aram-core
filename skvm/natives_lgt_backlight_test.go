package skvm

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestLGTBacklightUsesSerializableVirtualDeadline(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	invoke := func(name, descriptor string, args ...Value) {
		t.Helper()
		_, has, err := vm.InvokeStatic(context.Background(), "mmpp/media/BackLight", name, descriptor, args...)
		if err != nil || has {
			t.Fatalf("%s: result=%v error=%v", name, has, err)
		}
	}
	invoke("on", "(I)V", IntValue(25))
	if on, until := vm.services.Device.Backlight(); !on || until != 25*time.Millisecond {
		t.Fatalf("millisecond deadline = %v, %s", on, until)
	}
	saved, err := vm.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.services.Device.Advance(25 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if on, _ := vm.services.Device.Backlight(); on {
		t.Fatal("backlight did not expire")
	}
	if err := vm.UnmarshalBinary(saved); err != nil {
		t.Fatal(err)
	}
	if err := vm.services.Device.Advance(24 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if on, _ := vm.services.Device.Backlight(); !on {
		t.Fatal("restored deadline expired early")
	}
	if err := vm.services.Device.Advance(25 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if on, _ := vm.services.Device.Backlight(); on {
		t.Fatal("restored deadline did not expire")
	}
	invoke("on", "(I)V", IntValue(0))
	if err := vm.services.Device.Advance(time.Hour); err != nil {
		t.Fatal(err)
	}
	if on, until := vm.services.Device.Backlight(); !on || until != 0 {
		t.Fatalf("zero duration is not indefinite: %v %s", on, until)
	}
	before, err := vm.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vm.InvokeStatic(context.Background(), "mmpp/media/BackLight", "on", "(I)V", IntValue(-1)); err == nil {
		t.Fatal("negative timeout accepted")
	}
	after, err := vm.MarshalBinary()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("invalid timeout mutated state: %v", err)
	}
	invoke("off", "()V")
	if on, until := vm.services.Device.Backlight(); on || until != 0 {
		t.Fatalf("off retained active deadline: %v %s", on, until)
	}
	if _, _, err := vm.InvokeStatic(context.Background(), "mmpp/media/BackLight", "getColor", "()I"); err == nil {
		t.Fatal("unimplemented color semantics faked success")
	}
}

func TestLGTBacklightDoesNotLeakToOtherPolicies(t *testing.T) {
	for _, policy := range []NativePolicy{NativePolicySKT, NativePolicyJ2ME} {
		vm := policyRegressionVM(t, policy)
		if _, _, err := vm.InvokeStatic(context.Background(), "mmpp/media/BackLight", "on", "(I)V", IntValue(0)); err == nil {
			t.Fatalf("LGT BackLight leaked to policy %d", policy)
		}
	}
}
