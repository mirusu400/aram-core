package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorStrcmpImportRunsInGuest(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU: public.CPU, Public: public,
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	stub, err := runtime.resolvedImportStub(raptorStrcmpImport)
	check(t, err)
	if stub != raptorStrcmpStub {
		t.Fatalf("resolved strcmp helper = 0x%08x, want 0x%08x", stub, raptorStrcmpStub)
	}

	for _, test := range []struct {
		name        string
		left, right string
		wantSign    int
	}{
		{name: "equal", left: "texture", right: "texture"},
		{name: "less", left: "alpha", right: "beta", wantSign: -1},
		{name: "greater", left: "sprite2", right: "sprite1", wantSign: 1},
		{name: "prefix", left: "devil", right: "devil-may-cry", wantSign: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			left, err := public.Heap.Allocate(uint32(len(test.left)+1), true)
			check(t, err)
			right, err := public.Heap.Allocate(uint32(len(test.right)+1), true)
			check(t, err)
			check(t, public.CPU.WriteMemory(left, append([]byte(test.left), 0)))
			check(t, public.CPU.WriteMemory(right, append([]byte(test.right), 0)))
			check(t, public.CPU.WriteRegister(cpu.RegisterR0, left))
			check(t, public.CPU.WriteRegister(cpu.RegisterR1, right))
			check(t, public.CPU.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1))
			result := public.CPU.Run(context.Background(), stub, cpu.ModeThumb, 256)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.PC != guest.ReturnSentinel+2 {
				t.Fatalf("strcmp helper stopped at %#v", result)
			}
			word, err := public.CPU.ReadRegister(cpu.RegisterR0)
			check(t, err)
			got := int32(word)
			if got != int32(test.wantSign) {
				t.Fatalf("strcmp(%q, %q) = %d, want %d", test.left, test.right, got, test.wantSign)
			}
		})
	}
}

func TestRaptorStrcmpHelperIsReinstalledWithSavedImportSlots(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	state := &SavedState{
		resolvedImports: map[raptorImportKey]uint64{raptorStrcmpImport: 1},
		importSlots:     []raptorImportKey{raptorStrcmpImport},
	}
	check(t, public.CPU.WriteMemory(raptorStrcmpStub, make([]byte, 32)))
	check(t, RestoreState(runtime, public.CPU, state))
	var code [2]byte
	check(t, public.CPU.ReadMemory(raptorStrcmpStub, code[:]))
	if code != [2]byte{0x02, 0x78} {
		t.Fatalf("restored strcmp helper starts %x", code)
	}
}
