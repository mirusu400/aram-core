package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The dialogue renderer obtains char arrays through Vector's Object API and
// casts them back to char[]. Raptor arrays use a zero class-holder vtable slot,
// so rejecting that primitive-array cast left both dialogue rows null and made
// the first non-empty drawChars call fault.
func TestRaptorIssue532DrawsOpeningDialogue(t *testing.T) {
	path, data := findAuthorizedPackage(t, "735a579d82ac53bb205b04250ce44586c6d9375e64c2d6f3324796b3ae24d031")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	factory := NewFactory()
	factory.NewCPU = fastestCPUFactory()
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

	inputs := map[int]bool{
		300: true, 2200: true, 3500: true, 4800: true, 6200: true, 7200: true,
	}
	for frame := 0; frame < 10000; frame++ {
		if inputs[frame] {
			for _, pressed := range []bool{true, false} {
				check(t, machine.QueueInput(machinecore.InputEvent{
					Control: "ok", Pressed: pressed,
				}))
			}
		}
		if err := machine.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}

	if got, want := fmt.Sprintf("%x", sha256.Sum256(machine.frame.Pix)), "492dc204954cc9d69d417df2213d9d3c62477752f139fae9da50091e1932ea21"; got != want {
		t.Fatalf("opening dialogue frame hash = %s, want %s", got, want)
	}
}
