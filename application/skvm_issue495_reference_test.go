package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The title's offline network notice selects DrawNotice after filling notice[]
// but before filling the separate text array and date that DrawNotice reads.
func TestSKVMIssue495OfflineNoticeUsesErrorText(t *testing.T) {
	const digest = "0262a4fe3389fc957b2597e92f71a1c17db91e0728b656042b8a14f7f345c2af"
	path, data := findAuthorizedPackage(t, digest)
	ctx := context.Background()
	factory := NewFactory()
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	if err := created.Start(ctx); err != nil {
		t.Fatal(err)
	}
	events := map[int]machinecore.InputEvent{
		8: {Control: "back", Pressed: true},
		15: {Control: "num3", Pressed: true},
		76: {Control: "num5", Pressed: true},
		111: {Control: "back", Pressed: false},
		114: {Control: "num4", Pressed: true},
		125: {Control: "num1", Pressed: true},
		480: {Control: "back", Pressed: true},
		499: {Control: "num2", Pressed: true},
		548: {Control: "back", Pressed: false},
		550: {Control: "num3", Pressed: false},
		741: {Control: "back", Pressed: true},
		750: {Control: "num1", Pressed: false},
		771: {Control: "num3", Pressed: true},
		789: {Control: "num1", Pressed: true},
	}
	for frame := 0; frame < 900; frame++ {
		if event, ok := events[frame]; ok {
			if err := created.QueueInput(event); err != nil {
				t.Fatalf("input at frame %d: %v", frame, err)
			}
		}
		if err := created.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
}
