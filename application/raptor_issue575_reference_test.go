package application

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

// 월드장기체스's main menu opens 환경설정 as a message-box state, in two steps.
// The OK key only switches state. The box then animates open over a few paints
// and only afterwards posts its message. Its key handler reads message 0
// without checking the queue, so a key pressed while the box opens throws
// ArrayIndexOutOfBoundsException out of java/util/Vector.elementAt. That is
// the title's own race. ARAM widened it by running every key queued in one
// frame before the repaint the first key asked for, and it ended the session
// on the exception. aram-fuzz found it by holding several keys at once (issue
// #575).
//
// The key that lands inside the animation is absorbed as an uncaught exception
// and the title keeps running. Once the box is open, keys reach a posted
// message and nothing more is thrown.
func TestRaptorIssue575KeyWhileSettingsBoxOpensKeepsRunning(t *testing.T) {
	path, data := findAuthorizedPackage(t, "70d709c40e10541fe54a43501a5859045a0e6c454706500e6665c74c7c8fd52f")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	factory := NewFactory()
	factory.NewCPU = fastestCPUFactory()
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

	// OK four times through the logos and the title, then left twice turns
	// the main-menu ring to 환경설정. At frame 880 OK opens it and a second key
	// arrives in the same frame. At frame 960 the box is open and takes a key
	// normally.
	presses := map[int][]string{
		300: {"ok"}, 400: {"ok"}, 500: {"ok"}, 600: {"ok"},
		760: {"left"}, 820: {"left"},
		880: {"ok", "left"},
		960: {"down"},
	}
	var presentsAfterBox uint32
	for frame := 0; frame < 1100; frame++ {
		for _, control := range presses[frame] {
			check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
		}
		for _, control := range presses[frame-3] {
			check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}))
		}
		if err := machine.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if frame == 900 {
			presentsAfterBox = machine.wipi.Stats.PresentCount
		}
	}
	count, last := machine.UncaughtGuestExceptions()
	if count != 1 || !strings.Contains(last, "java/lang/ArrayIndexOutOfBoundsException") {
		t.Fatalf("uncaught exceptions = %d (last %q), want the one key inside the box animation", count, last)
	}
	if machine.wipi.Stats.PresentCount <= presentsAfterBox {
		t.Fatal("title stopped presenting after the absorbed exception")
	}
}
