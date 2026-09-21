package skvm

import (
	"context"
	"testing"
)

func TestLGTPhonePropertyIsPolicyScoped(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	value := invokeTestNative(t, vm, lgtPhoneClass, "getProperty", "(Ljava/lang/String;)Ljava/lang/String;", 0, ReferenceValue(vm.NewString("MIN")))
	ref, err := value.Reference()
	if err != nil {
		t.Fatal(err)
	}
	text, err := vm.String(ref)
	if err != nil || text != "01000000000" {
		t.Fatalf("MIN property = %q err=%v", text, err)
	}
	value = invokeTestNative(t, vm, lgtPhoneClass, "getProperty", "(Ljava/lang/String;)Ljava/lang/String;", 0, ReferenceValue(vm.NewString("unknown")))
	if ref, _ = value.Reference(); ref != 0 {
		t.Fatalf("unknown phone property = 0x%x, want null", ref)
	}
	for _, policy := range []NativePolicy{NativePolicyJ2ME, NativePolicySKT} {
		other := policyRegressionVM(t, policy)
		if other.SupportsNativeReference(lgtPhoneClass, "getProperty", "(Ljava/lang/String;)Ljava/lang/String;") {
			t.Fatalf("LGT Phone native leaked to policy %d", policy)
		}
	}
	_, _, err = vm.InvokeStatic(context.Background(), lgtPhoneClass, "getProperty", "(Ljava/lang/String;)Ljava/lang/String;", ReferenceValue(0))
	if err == nil {
		t.Fatal("null property key accepted")
	}
}

func TestLGTSystemPhoneModelIsNonempty(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	value := invokeTestNative(t, vm, "java/lang/System", "getProperty", "(Ljava/lang/String;)Ljava/lang/String;", 0, ReferenceValue(vm.NewString("microedition.phone.model")))
	reference, err := value.Reference()
	if err != nil || reference == 0 {
		t.Fatalf("phone model reference = %d, %v", reference, err)
	}
	model, err := vm.String(reference)
	if err != nil || model == "" {
		t.Fatalf("phone model = %q, %v", model, err)
	}
}

func TestLGTWallClockProgressesDuringGuestBusyWait(t *testing.T) {
	for _, policy := range []NativePolicy{NativePolicyLGT, NativePolicyJ2ME} {
		vm := policyRegressionVM(t, policy)
		before := invokeTestNative(t, vm, "java/lang/System", "currentTimeMillis", "()J", 0)
		vm.Instructions += 1000
		after := invokeTestNative(t, vm, "java/lang/System", "currentTimeMillis", "()J", 0)
		start, err := before.Long()
		check(t, err)
		end, err := after.Long()
		check(t, err)
		want := int64(0)
		if policy == NativePolicyLGT {
			want = 1
		}
		if end-start != want {
			t.Fatalf("policy %d clock change = %d, want %d", policy, end-start, want)
		}
	}
}
