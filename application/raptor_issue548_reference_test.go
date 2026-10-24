package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Real Soccer Manager 2009 constructs its native C++ objects through the LGT
// runtime's module-1 ordinal 1062. When that allocator import returned the
// unresolved-import zero, Clet startup entered the allocation-failure unwinder
// and faulted while reading 0xa0001338.
func TestRaptorIssue548StartsAndPresents(t *testing.T) {
	path, data := findAuthorizedPackage(t, "b7699c10dfd1992814d26294e32c1376820802532eb5f70ba959a71ebd31ea45")

	for _, backend := range []struct {
		name   string
		create CPUFactory
	}{
		{"fastest", fastestCPUFactory()}, {"jit", newJITCPU}, {"precise", newPreciseCPU},
	} {
		t.Run(backend.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			factory := NewFactory()
			factory.NewCPU = backend.create
			factory.RunBudget = DefaultHandsetRunBudget
			factory.FrameRunBudget = DefaultHandsetRunBudget
			factory.RaptorFrameRunBudget = DefaultRaptorFrameRunBudget
			created, err := factory.Create(ctx, machinecore.Source{
				Name: filepath.Base(path), Path: path,
				ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
			})
			check(t, err)
			machine := created.(*Machine)
			t.Cleanup(func() { _ = machine.Close() })
			check(t, machine.Start(ctx))

			for frame := 0; frame < 2400; frame++ {
				if err := machine.StepFrame(ctx); err != nil {
					snapshot := machine.DebugSnapshot(32)
					t.Fatalf(
						"frame %d: %v; CPU=%+v last=%+v Raptor=%+v APIs=%+v unimplemented=%+v",
						frame, err, snapshot.CPU, snapshot.LastResult, snapshot.Raptor,
						machine.wipi.Stats, machine.wipi.Unimplemented,
					)
				}
			}
			for name, count := range machine.wipi.Unimplemented {
				if count != 0 {
					t.Errorf("unimplemented API %s called %d times", name, count)
				}
			}
			if got := machine.wipi.Stats.PresentCount; got == 0 {
				t.Fatal("game started but never presented a frame")
			}
			nonBlack := 0
			for offset := 0; offset+3 < len(machine.frame.Pix); offset += 4 {
				if machine.frame.Pix[offset] != 0 || machine.frame.Pix[offset+1] != 0 || machine.frame.Pix[offset+2] != 0 {
					nonBlack++
				}
			}
			if nonBlack == 0 {
				t.Fatal("game presented only a black frame")
			}
		})
	}
}
