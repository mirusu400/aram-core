package raptor

import (
	"context"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorClockPollingYieldsOnlyResumableCallbacks(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	poll := func(module, lr uint32) {
		t.Helper()
		check(t, public.CPU.WriteRegister(cpu.RegisterLR, lr))
		check(t, runtime.dispatchImport(context.Background(), raptorImportKey{Module: module, Ordinal: 125}))
		low, err := public.CPU.ReadRegister(cpu.RegisterR0)
		check(t, err)
		high, err := public.CPU.ReadRegister(cpu.RegisterR1)
		check(t, err)
		if got, want := uint64(low)|uint64(high)<<32, uint64(public.Services.Clock.WallMillis()); got != want {
			t.Fatalf("current time = %d, want %d", got, want)
		}
	}
	// A synchronous invocation cannot be suspended halfway through its stack.
	for range 2 * raptorClockPollYieldThreshold {
		poll(1, 0x04000011)
		if runtime.TakeClockPollYield() {
			t.Fatal("synchronous clock read requested a yield")
		}
	}
	runtime.SetCallbackTaskActive(true)
	runtime.BeginGuestSlice()
	before := public.Services.Clock.Snapshot()
	for i := uint32(1); i <= raptorClockPollYieldThreshold; i++ {
		poll(1, 0x04000011)
		if got, want := runtime.TakeClockPollYield(), i == raptorClockPollYieldThreshold; got != want {
			t.Fatalf("poll %d: yield=%t, want %t", i, got, want)
		}
	}
	if runtime.TakeClockPollYield() {
		t.Fatal("clock-poll yield was not consumed")
	}
	if after := public.Services.Clock.Snapshot(); after != before {
		t.Fatal("polling advanced virtual time inside the callback")
	}
	// A resumed slice starts a fresh polling window, including after restore.
	runtime.BeginGuestSlice()
	poll(507, 0x04000011)
	if runtime.TakeClockPollYield() {
		t.Fatal("poll count leaked into the next slice")
	}
	// Reads at changing call sites, or of an advancing clock, are not a spin.
	for i := uint32(0); i < 2*raptorClockPollYieldThreshold; i++ {
		poll(507, 0x04000011+2*(i%2))
		if runtime.TakeClockPollYield() {
			t.Fatal("changing clock-read sites requested a yield")
		}
	}
	for range 2 * raptorClockPollYieldThreshold {
		check(t, public.Services.Advance(public.ServiceOwner, time.Millisecond))
		poll(507, 0x04000011)
		if runtime.TakeClockPollYield() {
			t.Fatal("an advancing clock requested a yield")
		}
	}
}
