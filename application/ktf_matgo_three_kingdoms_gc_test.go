package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

// TestKTFMatgoThreeKingdomsCoalescesExplicitGC is an optional authorized-corpus
// regression for issue #276. Only the package identity is retained here. This
// title requests a full System.gc in its animation loop: honoring every hint
// made 240 unpaced frames take 32 seconds, almost entirely in root marking.
func TestKTFMatgoThreeKingdomsCoalescesExplicitGC(t *testing.T) {
	path, data := findAuthorizedPackage(t, "5267badf20b3ee99b0f45b4d9732507d76727bab233d6d90e994413abba570f0")
	factory := NewFactory()
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.Start(context.Background()))

	start := time.Now()
	for frame := 0; frame < 240; frame++ {
		if frame == 180 {
			check(t, machine.QueueInput(machinecore.InputEvent{Control: "fire", Pressed: true}))
			check(t, machine.QueueInput(machinecore.InputEvent{Control: "fire", Pressed: false}))
		}
		check(t, machine.StepFrame(context.Background()))
	}
	elapsed := time.Since(start)
	t.Logf("240 frames elapsed=%v presents=%d", elapsed, machine.ktf.PresentCount)
	// The fixed path takes about 0.5 seconds on the reference Windows host. A
	// generous ceiling keeps slower builders stable while still catching the
	// former one-full-collection-per-frame behavior by a wide margin.
	if elapsed > 8*time.Second {
		t.Fatalf("240 unpaced frames took %v", elapsed)
	}
}
