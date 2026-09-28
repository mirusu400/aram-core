package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The reported title is kept in the optional external corpus. This exercises
// its Raptor Java adapter after a new game has entered gameplay, including the
// shared Java media state that is absent during startup.
func TestIssue371ReferenceSaveStateReplay(t *testing.T) {
	path := os.Getenv("ARAM_ISSUE371_ZIP")
	if path == "" {
		t.Skip("ARAM_ISSUE371_ZIP is not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	newMachine := func() *Machine {
		t.Helper()
		factory := NewFactory()
		factory.RunBudget = DefaultHandsetRunBudget
		factory.FrameRunBudget = DefaultHandsetRunBudget
		created, err := factory.Create(context.Background(), machinecore.Source{
			Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
		})
		if err != nil {
			t.Fatal(err)
		}
		machine := created.(*Machine)
		if err := machine.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = machine.Close() })
		return machine
	}
	machine := newMachine()
	presses := map[int]bool{100: true, 110: false, 850: true, 860: false,
		1200: true, 1210: false, 1900: true, 1910: false,
		2700: true, 2710: false, 3700: true, 3710: false}
	for frame := 0; frame < 5000; frame++ {
		if pressed, ok := presses[frame]; ok {
			if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: pressed}); err != nil {
				t.Fatal(err)
			}
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	frameAtSave := sha256.Sum256(machine.frame.Pix)
	for range 50 {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	wantFrame := sha256.Sum256(machine.frame.Pix)
	wantCalls := machine.wipi.Stats.APICalls
	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(machine.frame.Pix); got != frameAtSave {
		t.Fatalf("frame after load = %x, want %x", got, frameAtSave)
	}
	for range 50 {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := sha256.Sum256(machine.frame.Pix); got != wantFrame {
		t.Fatalf("replayed frame = %x, want %x", got, wantFrame)
	}
	if got := machine.wipi.Stats.APICalls; got != wantCalls {
		t.Fatalf("replayed API calls = %d, want %d", got, wantCalls)
	}
	reloaded := newMachine()
	if err := reloaded.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if err := reloaded.StepFrame(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := sha256.Sum256(reloaded.frame.Pix); got != wantFrame {
		t.Fatalf("fresh machine replayed frame = %x, want %x", got, wantFrame)
	}
	// Continue through the opening cave and dialogue after restoring. This
	// reaches live game code beyond the point at which the state was captured.
	controls := map[int]machinecore.InputEvent{
		5100: {Control: "soft-left", Pressed: true},
		5110: {Control: "soft-left", Pressed: false},
		5400: {Control: "soft-right", Pressed: true},
		5410: {Control: "soft-right", Pressed: false},
		5700: {Control: "menu", Pressed: true},
		5710: {Control: "menu", Pressed: false},
	}
	for frame := 5050; frame < 6200; frame++ {
		if event, ok := controls[frame]; ok {
			if err := reloaded.QueueInput(event); err != nil {
				t.Fatal(err)
			}
		}
		if err := reloaded.StepFrame(context.Background()); err != nil {
			t.Fatalf("reloaded frame %d: %v", frame, err)
		}
	}
}
