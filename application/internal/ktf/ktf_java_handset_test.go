package ktf

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestKTFJavaLEDReflectsDeviceState(t *testing.T) {
	runtime := newTestRuntime(t)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 1))
	_, err := HostJavaMethod(
		"org/kwis/msp/handset/LED", "set", "(I)V",
	)(context.Background(), runtime)
	check(t, err)
	value, err := HostJavaMethod(
		"org/kwis/msp/handset/LED", "get", "()I",
	)(context.Background(), runtime)
	check(t, err)
	count, err := HostJavaMethod(
		"org/kwis/msp/handset/LED", "getCount", "()I",
	)(context.Background(), runtime)
	check(t, err)
	if value != 1 || count != uint32(runtime.Services.Device.Config().LEDCount) {
		t.Fatalf("LED get/count = %d/%d", value, count)
	}
}
