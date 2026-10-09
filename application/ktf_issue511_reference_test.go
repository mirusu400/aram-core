package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The exact package is read in place from ARAM_TEST_DATA. No game assets are
// needed for the synthetic TextComponent field and editing regressions.
func TestIssue511SeoulTycoon2NameInputContinues(t *testing.T) {
	const digest = "9789fec50f39febc2d75f48ecbb2f8e30dcdfbb7221241af99d0aa559218e299"
	path, data := findAuthorizedPackage(t, digest)
	factory := NewFactory()
	var err error
	factory.NewCPU, err = ResolveCPUBackend("fastest")
	check(t, err)
	factory.RunBudget = DefaultHandsetRunBudget
	factory.FrameRunBudget = DefaultHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), Path: path, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.Start(context.Background()))
	var emptyName, enteredName [32]byte
	for frame := 0; frame < 1800; frame++ {
		if frame >= 80 && (frame%80 == 0 || frame%80 == 2) {
			control := "ok"
			if frame == 240 || frame == 242 {
				control = "num1"
			}
			if frame >= 1040 {
				control = []string{"num5", "num1", "num4", "num1", "num2", "ok", "soft-left", "ok", "ok", "ok"}[(frame-1040)/80%10]
			}
			check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: frame%80 == 0}))
		}
		check(t, machine.StepFrame(context.Background()))
		if machine.ktf.FirstJavaThrowName != "" {
			t.Fatalf("frame %d: name input threw %s", frame, machine.ktf.FirstJavaThrowName)
		}
		if frame == 1039 {
			emptyName = sha256.Sum256(machine.frame.Pix)
		}
		if frame == 1359 {
			enteredName = sha256.Sum256(machine.frame.Pix)
		}
	}
	if emptyName == enteredName {
		t.Fatal("keypad composition did not update the name field")
	}
	if sha256.Sum256(machine.frame.Pix) == enteredName {
		t.Fatal("the game did not leave the name dialog")
	}
	for _, task := range machine.DebugSnapshot(1).KTF.Tasks {
		if !task.Done {
			return
		}
	}
	t.Fatal("name input terminated every game task")
}
