package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
)

const issue584PackageSHA256 = "8dbc95dc1712f0ce086fb0284bd137fe9236c9646204cbb5e0507895d7321a98"

// TestReferenceSKVMIssue584ExposesFocusedNameInput covers the exact LGT
// 삼국쟁패 패왕전기 package from issue #584. Its editable field is a game subclass
// of TextFieldX, so the host hint must follow instanceof semantics rather than
// requiring the heap object's concrete class to be TextFieldX itself.
func TestReferenceSKVMIssue584ExposesFocusedNameInput(t *testing.T) {
	path, data := findAuthorizedPackage(t, issue584PackageSHA256)
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	check(t, err)
	t.Cleanup(func() { _ = created.Close() })
	machine, ok := created.(*skvmhost.Machine)
	if !ok {
		t.Fatalf("issue #584 machine = %T, want *skvmhost.Machine", created)
	}
	check(t, machine.Start(context.Background()))

	for frame := 0; frame < 1000 && !machine.TextInputActive(); frame++ {
		if frame >= 100 && frame%100 == 0 {
			check(t, machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true}))
		}
		if frame >= 102 && frame%100 == 2 {
			check(t, machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: false}))
		}
		check(t, machine.StepFrame(context.Background()))
	}
	if !machine.TextInputActive() {
		t.Fatal("name-entry TextFieldX subclass did not expose active text input")
	}
	for frame := 0; frame < 100; frame++ {
		check(t, machine.StepFrame(context.Background()))
	}

	for _, control := range []string{"num5", "num1"} {
		check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
		check(t, machine.StepFrame(context.Background()))
		check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}))
		check(t, machine.StepFrame(context.Background()))
	}
	for _, control := range []string{"select"} {
		check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
		check(t, machine.StepFrame(context.Background()))
		check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}))
		check(t, machine.StepFrame(context.Background()))
	}
	for frame := 0; frame < 30 && machine.TextInputActive(); frame++ {
		check(t, machine.StepFrame(context.Background()))
	}
	if machine.TextInputActive() {
		t.Fatal("accepted Korean name left the text-input hint active")
	}
}
