package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

func TestRaptorIssue525LoadsLevelData(t *testing.T) {
	path, data := findAuthorizedPackage(t, "acf6863fc84ce512f30917ab9a9bb0760db8d4b628153844ab99d1ac0ec1f6ac")
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
			for frame := 1; frame <= 5200; frame++ {
				if frame%200 == 0 || frame%200 == 8 {
					control := "ok"
					if frame == 1800 || frame == 1808 {
						control = "num1"
					}
					if frame >= 2800 {
						control = "hash"
					}
					check(t, machine.QueueInput(machinecore.InputEvent{
						Control: control, Pressed: frame%200 == 0,
					}))
				}
				if err := machine.StepFrame(ctx); err != nil {
					t.Fatalf("frame %d: %v", frame, err)
				}
				if frame == 400 {
					// The game requests one restart after creating its options
					// file on a fresh install. Preserve that file through Reset.
					check(t, machine.Reset(ctx))
					check(t, machine.Start(ctx))
				}
			}
			if machine.wipi.Observed["atoi"] < 1000 || machine.wipi.Stats.PresentCount < 100 {
				t.Fatalf("level replay: atoi=%d presents=%d",
					machine.wipi.Observed["atoi"], machine.wipi.Stats.PresentCount)
			}
			if got := machine.wipi.Unimplemented["RAPTOR.module1#1019"]; got != 0 {
				t.Fatalf("decimal parsing fell through to an unimplemented import %d times", got)
			}
			// Pin the first loaded map, rather than accepting an intro or a
			// black frame as success. These globals belong to this exact hash.
			for address, want := range map[uint32]uint32{
				0x01503e44: 7,  // map number parsed from the packaged level data
				0x01503e10: 25, // map columns
				0x01503e14: 20, // map rows
			} {
				got, err := machine.wipi.ReadU32(address)
				check(t, err)
				if got != want {
					t.Fatalf("map field 0x%08x = %d, want %d", address, got, want)
				}
			}
			mapData, err := machine.wipi.ReadU32(0x01503e0c)
			check(t, err)
			var dimensions [2]byte
			check(t, machine.cpu.ReadMemory(mapData, dimensions[:]))
			if dimensions != [2]byte{25, 20} {
				t.Fatalf("map resource dimensions = %v, want [25 20]", dimensions)
			}
		})
	}
}
