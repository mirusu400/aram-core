package application

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// TestAuthPCTrace runs a raptor auth title with ARAM_PC_TRACE and reports the
// hottest guest PCs plus whether specific auth functions ever execute.
func TestAuthPCTrace(t *testing.T) {
	path := os.Getenv("AUTH_ZIP")
	if path == "" {
		t.Skip("AUTH_ZIP not set")
	}
	if os.Getenv("ARAM_PC_TRACE") == "" {
		t.Skip("ARAM_PC_TRACE not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	factory := NewFactory()
	factory.RunBudget = DefaultHandsetRunBudget
	factory.FrameRunBudget = DefaultHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	m := created.(*Machine)
	t.Cleanup(func() { _ = m.Close() })
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 300; frame++ {
		if frame > 0 && frame%70 == 0 {
			for _, p := range []bool{true, false} {
				_ = m.QueueInput(machinecore.InputEvent{Control: "num1", Pressed: p})
			}
		}
		if err := m.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	t.Logf("Clet: %+v", m.raptor.Clet)
	be, ok := m.cpu.(*interpreter.Backend)
	if !ok {
		t.Fatalf("cpu is %T, not *interpreter.Backend", m.cpu)
	}
	hits := be.PCHits()
	t.Logf("distinct PCs executed: %d", len(hits))

	type pc struct {
		addr  uint32
		count uint64
	}
	var all []pc
	var total uint64
	for a, c := range hits {
		all = append(all, pc{a, c})
		total += c
	}
	sort.Slice(all, func(i, j int) bool { return all[i].count > all[j].count })
	t.Logf("total instructions: %d", total)
	t.Logf("hottest PCs:")
	for i := 0; i < len(all) && i < 20; i++ {
		t.Logf("  0x%08x x%d", all[i].addr, all[i].count)
	}
	// Did specific auth functions ever run? Report hit-count within each range.
	ranges := []struct {
		name   string
		lo, hi uint32
	}{
		{"sub_3A430 auth-tick", 0x3a430, 0x3a618},
		{"sub_3A21C redraw", 0x3a21c, 0x3a2b8},
		{"sub_2F684 poll-wrapper", 0x2f684, 0x2f6a8},
		{"sub_5764C receive", 0x5764c, 0x57690},
		{"sub_5ADE0 connected?", 0x5ade0, 0x5adf8},
	}
	for _, r := range ranges {
		var c uint64
		for a, n := range hits {
			if a >= r.lo && a < r.hi {
				c += n
			}
		}
		t.Logf("range %-24s [0x%06x,0x%06x): %d insns executed", r.name, r.lo, r.hi, c)
	}
}
