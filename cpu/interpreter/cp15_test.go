package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestARMCP15ControlWriteReadAndStateRoundTrip(t *testing.T) {
	backend := New()
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
		0xee070f17, // MCR p15, 0, r0, c7, c7, 0: invalidate unified caches
		0xee080f17, // MCR p15, 0, r0, c8, c7, 0: invalidate unified TLB
	)
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 3)
	if result.Err != nil || result.Instructions != 3 {
		t.Fatalf("Run result = %+v", result)
	}
}
