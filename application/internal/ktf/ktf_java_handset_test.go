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

func TestKTFWIPI2JavaLEDColorReflectsDeviceState(t *testing.T) {
	runtime := newTestRuntime(t)
	parameters := allocWords(t, runtime, 3)
	runtime.NativeParameterBase = parameters
	t.Cleanup(func() { runtime.NativeParameterBase = 0 })
	check(t, runtime.writeWords(parameters, []uint32{0, 0x12abef, 0}))
	set, err := HostJavaMethod(
		"org/kwis/msp/handset/LED", "setColor", "(II)I",
	)(context.Background(), runtime)
	check(t, err)
	check(t, runtime.writeWords(parameters, []uint32{0, 0, 0}))
	got, err := HostJavaMethod(
		"org/kwis/msp/handset/LED", "getColor", "(I)I",
	)(context.Background(), runtime)
	check(t, err)
	if set != 0x12abef || got != set {
		t.Fatalf("LED set/get color = 0x%06x/0x%06x", set, got)
	}
}
