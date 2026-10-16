package minigame

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func TestModernRandomUsesSignedHalfwordBounds(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	for reg, value := range map[uint32]uint32{cpu.RegisterSP: 0x1000, cpu.RegisterR1: 0xffff, cpu.RegisterR2: 1} {
		if err := backend.WriteRegister(reg, value); err != nil {
			t.Fatal(err)
		}
	}
	r := &Runtime{cpu: backend, rngState: 1}
	seen := make(map[int32]bool)
	for range 64 {
		value, err := r.dispatch(eadsServiceCall{id: 0x153, slot: 0x4c})
		if err != nil {
			t.Fatal(err)
		}
		if n := int32(value); n < -1 || n > 1 {
			t.Fatalf("random(-1,1)=%d", n)
		} else {
			seen[n] = true
		}
	}
	if len(seen) != 3 {
		t.Fatalf("random jitter covered %d values, want all three", len(seen))
	}
}

func TestModernRandomCanPopulateBothOddAndEvenGridRows(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterSP, 0x1000); err != nil {
		t.Fatal(err)
	}
	r := &Runtime{cpu: backend, rngState: 1}
	draw := func(upper uint32) uint32 {
		if err := backend.WriteRegister(cpu.RegisterR2, upper); err != nil {
			t.Fatal(err)
		}
		value, err := r.dispatch(eadsServiceCall{id: 0x153, slot: 0x4c})
		if err != nil {
			t.Fatal(err)
		}
		if value > upper {
			t.Fatalf("random coordinate %d exceeds %d", value, upper)
		}
		return value
	}
	seen := make(map[[2]uint32]bool)
	for range 200 {
		seen[[2]uint32{draw(4), draw(5)}] = true
	}
	// A five-column, six-row board must be able to place more than fifteen
	// unique obstacles; using consecutive LCG low bits reaches only odd rows.
	if len(seen) < 20 {
		t.Fatalf("random grid reached only %d of 30 cells", len(seen))
	}
}
