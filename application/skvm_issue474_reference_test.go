package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// MapleStory Mage reads table.gft through Runtime.class. The old package-relative
// lookup left its table null and threw during Start after 3,926 instructions.
func TestSKVMIssue474MapleStoryMageLoadsRootTable(t *testing.T) {
	const digest = "52edc54e2b231384001c5dbcb9a3334ee14b73bd2f10bef1d633003ecfd1dfc2"
	path, data := findAuthorizedPackage(t, digest)
	ctx := context.Background()
	created, err := NewFactory().Create(ctx, machinecore.Source{
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
	for frame := 0; frame < 800; frame++ {
		if err := created.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	debugger, ok := created.(interface{ DebugSnapshot(int) DebugSnapshot })
	if !ok {
		t.Fatalf("%T has no SKVM debug snapshot", created)
	}
	snapshot := debugger.DebugSnapshot(1)
	if snapshot.SKVM == nil || snapshot.SKVM.Instructions <= 3926 ||
		snapshot.SKVM.Framebuffer == nil || snapshot.SKVM.Framebuffer.Sequence == 0 {
		t.Fatalf("SKVM did not advance past the reported startup fault: %+v", snapshot.SKVM)
	}
	// The title's white transition lasts through frame 600. A live title
	// should render the forest and character rather than stay white.
	framebuffer := created.Framebuffer()
	bounds := framebuffer.Bounds()
	colored := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			red, green, blue, _ := framebuffer.At(x, y).RGBA()
			if red != 0xffff || green != 0xffff || blue != 0xffff {
				colored++
			}
		}
	}
	if colored < 10_000 {
		t.Fatalf("title remained blank after 800 frames: %d colored pixels", colored)
	}
}
