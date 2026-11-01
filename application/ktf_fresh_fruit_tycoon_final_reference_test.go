package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// freshFruitTycoonFinalKTFDigest identifies the KTF build of
// 생과일타이쿤파이널 named in issue #206.
const freshFruitTycoonFinalKTFDigest = "fe481fc815ab38865e8a33e45e63783c154fdff4126e3dadba558f2e93440698"

// TestReferenceKTFFreshFruitTycoonFinalPaints covers the title's first paint.
// Its card calls one of its own methods by name, and the call-by-name helper
// hands the object header's decoded class to get_java_method. While objects of
// title-compiled classes carried a packed JvmContext vtable slot as their
// header, that lookup searched a different class, the title threw
// java/lang/Error from its first paint, and it never presented a frame.
func TestReferenceKTFFreshFruitTycoonFinalPaints(t *testing.T) {
	path, data := findAuthorizedPackage(t, freshFruitTycoonFinalKTFDigest)
	factory := NewFactory()
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		Path:     path,
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
		SHA256:   freshFruitTycoonFinalKTFDigest,
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.Start(context.Background()))
	for frame := 0; frame < 1200; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	snapshot := machine.DebugSnapshot(1)
	if snapshot.KTF.LastJavaThrow == "java/lang/Error" {
		t.Fatalf("title threw java/lang/Error (last Java method %s)",
			snapshot.KTF.LastJavaMethod)
	}
	if snapshot.KTF.PresentCount < 100 {
		t.Fatalf("presented %d frames in 1200", snapshot.KTF.PresentCount)
	}
}
