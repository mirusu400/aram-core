package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorJavaSafepointYieldsHotResumableCallback(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:    public.CPU,
		Public: public,
		Java: &JavaRuntime{Tasks: []*JavaTask{
			{Procedure: 0x1001},
		}},
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterLR, 0x1235); err != nil {
		t.Fatal(err)
	}
	runtime.SetCallbackTaskActive(true)
	runtime.BeginGuestSlice()

	key := raptorImportKey{Module: 100, Ordinal: 85}
	for i := uint32(0); i < raptorJavaSafepointYieldThreshold-1; i++ {
		if err := runtime.dispatchImport(context.Background(), key); err != nil {
			t.Fatalf("safepoint %d: %v", i, err)
		}
	}
	if runtime.TakeJavaYield() {
		t.Fatal("safepoint yielded before the hot-backedge threshold")
	}
	if runtime.TakeJavaSafepointYield() {
		t.Fatal("safepoint reported a scheduler yield before the threshold")
	}

	if err := runtime.dispatchImport(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if !runtime.TakeJavaYield() {
		t.Fatal("hot resumable callback safepoint did not request a CPU-slice yield")
	}
	if !runtime.TakeJavaSafepointYield() {
		t.Fatal("hot resumable callback safepoint did not identify the scheduler yield")
	}
	if got := public.Stats.ImplementedCalls; got != uint64(raptorJavaSafepointYieldThreshold) {
		t.Fatalf("implemented safepoint calls = %d, want %d", got, raptorJavaSafepointYieldThreshold)
	}
	if got := public.Stats.UnimplementedCalls; got != 0 {
		t.Fatalf("unimplemented safepoint calls = %d, want 0", got)
	}
	if got := public.Stats.LastAPI; got != "RAPTOR.Java.safepoint" {
		t.Fatalf("last API = %q, want RAPTOR.Java.safepoint", got)
	}
}

func TestRaptorJavaSafepointDoesNotPreemptUnsafeOrIdleContexts(t *testing.T) {
	for _, tt := range []struct {
		name           string
		callbackActive bool
		tasks          []*JavaTask
	}{
		{
			name:           "synchronous callback",
			callbackActive: false,
			tasks:          []*JavaTask{{Procedure: 0x1001}},
		},
		{
			name:           "no runnable Java task",
			callbackActive: true,
			tasks:          []*JavaTask{{Procedure: 0x1001, Done: true}},
		},
		{
			name:           "sleeping Java task",
			callbackActive: true,
			tasks:          []*JavaTask{{Procedure: 0x1001, WakeAtMS: 100}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			public := newPublicRuntime(t)
			public.TickMS = 10
			runtime := &Runtime{
				CPU:    public.CPU,
				Public: public,
				Java:   &JavaRuntime{Tasks: tt.tasks},
			}
			runtime.SetCallbackTaskActive(tt.callbackActive)
			runtime.BeginGuestSlice()
			for range raptorJavaSafepointYieldThreshold + 8 {
				runtime.observeJavaSafepoint(0x1235)
			}
			if runtime.TakeJavaYield() || runtime.TakeJavaSafepointYield() {
				t.Fatal("safepoint preempted a context that cannot make useful progress")
			}
		})
	}
}

func TestRaptorJavaSafepointRequiresOneHotBackedge(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:    public.CPU,
		Public: public,
		Java: &JavaRuntime{Tasks: []*JavaTask{
			{Procedure: 0x1001},
		}},
	}
	runtime.SetCallbackTaskActive(true)
	runtime.BeginGuestSlice()
	for i := uint32(0); i < raptorJavaSafepointYieldThreshold*2; i++ {
		lr := uint32(0x1235)
		if i&1 != 0 {
			lr = 0x5679
		}
		runtime.observeJavaSafepoint(lr)
	}
	if runtime.TakeJavaYield() || runtime.TakeJavaSafepointYield() {
		t.Fatal("alternating safepoints were mistaken for one blocked backedge")
	}
}
