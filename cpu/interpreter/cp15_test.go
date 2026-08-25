package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestARMCP15ControlWriteReadAndStateRoundTrip(t *testing.T) {
	backend := New()
	if err := backend.SetCP15ControlHistoryLimit(2); err != nil {
		t.Fatal(err)
	}
	mapARMInstructions(t, backend,
		0xee010f10, // MCR p15, 0, r0, c1, c0, 0
		0xee111f10, // MRC p15, 0, r1, c1, c0, 0
	)
	if err := backend.WriteRegister(cpu.RegisterR0, 0x0005207a); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 2)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if got := register(t, backend, cpu.RegisterR1); got != 0x0005207a {
		t.Fatalf("CP15 control read = %#x", got)
	}
	saved, err := backend.SaveContext()
	if err != nil {
		t.Fatal(err)
	}
	backend.cp15.control = 0
	if err := backend.RestoreContext(saved); err != nil {
		t.Fatal(err)
	}
	if backend.cp15.control != 0x0005207a {
		t.Fatalf("restored CP15 control = %#x", backend.cp15.control)
	}
	if history := backend.CP15ControlHistory(); len(history) != 2 ||
		history[0] != (CP15ControlAccess{InstructionAddress: 0x1000, Value: 0x0005207a, Write: true}) ||
		history[1] != (CP15ControlAccess{InstructionAddress: 0x1004, Value: 0x0005207a}) {
		t.Fatalf("CP15 control history = %+v", history)
	}
}

func TestARMCP15ControlHistoryIsBoundedAndOptional(t *testing.T) {
	backend := New()
	if history := backend.CP15ControlHistory(); len(history) != 0 {
		t.Fatalf("default CP15 control history = %+v", history)
	}
	if err := backend.SetCP15ControlHistoryLimit(1); err != nil {
		t.Fatal(err)
	}
	backend.recordCP15ControlAccess(0x1000, 1, true)
	backend.recordCP15ControlAccess(0x1004, 2, false)
	if history := backend.CP15ControlHistory(); len(history) != 1 ||
		history[0] != (CP15ControlAccess{InstructionAddress: 0x1004, Value: 2}) {
		t.Fatalf("bounded CP15 control history = %+v", history)
	}
	if err := backend.SetCP15ControlHistoryLimit(0); err != nil {
		t.Fatal(err)
	}
	if history := backend.CP15ControlHistory(); len(history) != 0 {
		t.Fatalf("disabled CP15 control history = %+v", history)
	}
}

func TestARMCP15InstructionCachePrefetchHistoryIsBoundedAndOptional(t *testing.T) {
	backend := New()
	if history := backend.InstructionCachePrefetchHistory(); len(history) != 0 {
		t.Fatalf("default instruction prefetch history = %+v", history)
	}
	if err := backend.SetInstructionCachePrefetchHistoryLimit(1); err != nil {
		t.Fatal(err)
	}
	backend.recordInstructionCachePrefetch(0x1000, 0x2004)
	backend.recordInstructionCachePrefetch(0x1004, 0x3008)
	if history := backend.InstructionCachePrefetchHistory(); len(history) != 1 ||
		history[0] != (InstructionCachePrefetchAccess{
			InstructionAddress: 0x1004, ModifiedVirtualAddress: 0x3008,
		}) {
		t.Fatalf("bounded instruction prefetch history = %+v", history)
	}
}

func TestARMCP15EnablesImplementedMMUTranslation(t *testing.T) {
	backend := New()
	mapARMInstructions(t, backend, 0xee010f10)
	if err := backend.WriteRegister(cpu.RegisterR0, 1); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 1)
	if result.Err != nil || result.Instructions != 1 {
		t.Fatalf("MMU enable result = %+v", result)
	}
	if backend.cp15.control != 1 {
		t.Fatalf("MMU control = %#x", backend.cp15.control)
	}
}

func TestARMCP15AcceptsExplicitCacheAndTLBMaintenance(t *testing.T) {
	backend := New()
	mapARMInstructions(t, backend,
		0xee070f15, // MCR p15, 0, r0, c7, c5, 0: invalidate I-cache
		0xee070f35, // MCR p15, 0, r0, c7, c5, 1: invalidate I-cache line by MVA
		0xee070f36, // MCR p15, 0, r0, c7, c6, 1: invalidate D-cache line by MVA
		0xee070f3a, // MCR p15, 0, r0, c7, c10,1: clean D-cache line by MVA
		0xee070f9a, // MCR p15, 0, r0, c7, c10,4: drain write buffer
		0xee070f3e, // MCR p15, 0, r0, c7, c14,1: clean and invalidate D-cache line
		0xee070f17, // MCR p15, 0, r0, c7, c7, 0: invalidate unified caches
		0xee080f17, // MCR p15, 0, r0, c8, c7, 0: invalidate unified TLB
	)
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Instructions != 8 {
		t.Fatalf("Run result = %+v", result)
	}
}
