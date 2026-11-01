package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// bigiOZKTFDigest identifies the large-screen KTF WIPI 1.2 build of
// 천공의 기사단 (Bigi OZ) the user reported as not running.
const bigiOZKTFDigest = "1379b56de66ee4f90ae53aea9a7a43117d19ab61eb1c015d1fc16c2e304251b4"

// TestReferenceKTFBigiOZReachesTheField drives the title from launch through
// its rating and logo screens into the save-slot menu and onto the field.
// Three separate defects each stopped it on the way:
//
//   - its putfield helper walks the receiver's class chain decoded from the
//     object header, which pointed into the packed JVM vtable table, and
//     faulted before the first frame;
//   - it looks scenes up in a Hashtable with a fresh new Byte(n), which the
//     host matched by identity, so the lookup came back null and the next
//     putfield threw NullPointerException;
//   - its paint posts the next loading step with Display.callSerially and
//     then sleeps; the Runnable started during that sleep instead of after
//     the paint, its repaint() was coalesced into the paint still in flight,
//     and the loading bar froze.
func TestReferenceKTFBigiOZReachesTheField(t *testing.T) {
	path, data := findAuthorizedPackage(t, bigiOZKTFDigest)
	factory := NewFactory()
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		Path:     path,
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
		SHA256:   bigiOZKTFDigest,
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.Start(context.Background()))

	for frame := 0; frame < 4000; frame++ {
		if frame >= 600 && frame <= 3300 && frame%300 == 0 {
			check(t, machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: true}))
		}
		if frame >= 603 && frame <= 3303 && frame%300 == 3 {
			check(t, machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: false}))
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	snapshot := machine.DebugSnapshot(1)
	if snapshot.KTF.FirstJavaThrow != "" {
		t.Fatalf("title threw %s (last Java method %s)",
			snapshot.KTF.FirstJavaThrow, snapshot.KTF.LastJavaMethod)
	}
	// The loading screen alone presents once; the menus and the field
	// present continuously.
	if snapshot.KTF.PresentCount < 1000 {
		t.Fatalf("presented %d frames in 4000, want the title past its loading screen",
			snapshot.KTF.PresentCount)
	}
}
