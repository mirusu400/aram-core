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
