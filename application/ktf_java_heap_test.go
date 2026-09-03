package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// asphaltFourSHA256 identifies the 아스팔트4 package, which builds strings in
// its draw loop and so allocates a few hundred Java objects a frame.
const asphaltFourSHA256 = "1b3b4868d46e2ffa5a64466f523ac591265840ebde220d035b2563a2e7f478de"

// TestKTFJavaHeapOutlivesAStringBuildingLoop covers issues #131 and #132. KTF
// Java has no collector, so every object a host handler built stayed on the
// guest heap for as long as the title ran: 아스팔트4 allocated ~295 blocks a
// frame and had the whole 32 MiB heap gone by frame 9158, about two and a half
// minutes in, where a handset simply carries on.
func TestKTFJavaHeapOutlivesAStringBuildingLoop(t *testing.T) {
	path, data := findAuthorizedPackage(t, asphaltFourSHA256)

	factory := NewFactory()
	factory.FrameRunBudget = DefaultHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Well past the frame the heap used to run out at.
	for frame := 0; frame < 12000; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d with no input at all: %v", frame, err)
		}
	}
}

// TestKTFForcedCollectionLeavesAHealthyTitleAlone is the safety half. A
// conservative collector is only safe if it never frees something still in
// use, and collections normally happen too rarely to test against, so this
// forces one every fifty frames and checks the title does not notice.
func TestKTFForcedCollectionLeavesAHealthyTitleAlone(t *testing.T) {
	for _, title := range []struct {
		name   string
		digest string
	}{
		{"maple", mapleArcherSHA256},
		{"heroSaga", heroSagaOneSHA256},
	} {
		t.Run(title.name, func(t *testing.T) {
			path, data := findAuthorizedPackage(t, title.digest)
			factory := NewFactory()
			factory.FrameRunBudget = DefaultHandsetRunBudget
			factory.KTFRunBudget = DefaultKTFHandsetRunBudget
			created, err := factory.Create(context.Background(), machinecore.Source{
				Name:     filepath.Base(path),
				ReaderAt: bytes.NewReader(data),
				Size:     int64(len(data)),
			})
			if err != nil {
				t.Fatal(err)
			}
			machine := created.(*Machine)
			t.Cleanup(func() { _ = machine.Close() })
			if err := machine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			collected := 0
			for frame := 0; frame < 1200; frame++ {
				if err := machine.StepFrame(context.Background()); err != nil {
					t.Fatalf("frame %d after collecting %d blocks: %v",
						frame, collected, err)
				}
				if frame%50 == 49 {
					collected += machine.ktf.CollectJavaHeapForTest()
				}
			}
			if collected == 0 {
				t.Fatal("no block was ever collected, so nothing was proven")
			}
			t.Logf("survived 1200 frames with %d blocks collected", collected)
		})
	}
}

// TestKTFCollectionDropsDeadSideTableEntries covers issues #142 and #143. The
// collector walked the runtime for roots by reflection, which marked the key of
// every host table keyed by a guest object - the String texts, the Graphics
// registry, the boxed Integers - so an object the guest had long forgotten was
// still a root, and neither it nor anything it referred to could ever be freed.
// 리얼사커2007 ends with six hundred thousand dead Strings pinned that way and
// 트랜스포머 with two million dead Graphics.
func TestKTFCollectionDropsDeadSideTableEntries(t *testing.T) {
	path, data := findAuthorizedPackage(t, asphaltFourSHA256)

	factory := NewFactory()
	factory.FrameRunBudget = DefaultHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 1200; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	before := machine.ktf.WeakTableEntriesForTest()
	if before < 100 {
		t.Fatalf("only %d weak table entries after 1200 frames, so this title "+
			"no longer reproduces the leak and the test proves nothing", before)
	}
	machine.ktf.CollectJavaHeapForTest()
	after := machine.ktf.WeakTableEntriesForTest()
	if after >= before {
		t.Fatalf("collection left all %d of %d entries registered, so the "+
			"tables are still roots", after, before)
	}
	// The title has to keep running on what is left.
	for frame := 0; frame < 600; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d after dropping %d of %d entries: %v",
				frame, before-after, before, err)
		}
	}
	t.Logf("collection dropped %d of %d dead side table entries", before-after, before)
}
