package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// TestHybridSkipsEmptyDrawSlot exercises the package and default DRM bypass in
// issue #469. Without the null-draw compatibility, frame 143 calls address zero
// from the game's 0x31c16 virtual draw dispatch.
func TestHybridSkipsEmptyDrawSlot(t *testing.T) {
	const packageSHA256 = "e68b1c8aef85c584dc6e5e225f0c226641b23f6c523bf0f59eb662151f9cf954"
	path, data := findAuthorizedPackage(t, packageSHA256)
	ctx := context.Background()
	factory := NewFactory()
	factory.FrameRunBudget = DefaultHandsetRunBudget
	factory.RaptorFrameRunBudget = DefaultRaptorFrameRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if got := machine.raptor.Pkg.Descriptor; got.AID != "0002996E" || got.MainClass != "Clet" {
		t.Fatalf("unexpected Raptor title: AID=%s class=%s", got.AID, got.MainClass)
	}
	// These guarded bytes are the published, default-enabled Hybrid DRM bypass.
	// Apply them here because application tests do not depend on the product's
	// external cheat catalog.
	for _, patch := range []struct {
		address  uint32
		expected []byte
		value    []byte
	}{
		{0x00001a4c, []byte{0x01, 0x50, 0x00, 0x00}, []byte{0x00, 0xa1, 0x00, 0x00}},
		{0x0000193a, []byte{0x0c, 0x1c}, []byte{0x03, 0x24}},
	} {
		original := make([]byte, len(patch.expected))
		if err := machine.cpu.ReadMemory(patch.address, original); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original, patch.expected) {
			t.Fatalf("patch at 0x%08x: original %x, want %x", patch.address, original, patch.expected)
		}
		if err := machine.cpu.WriteMemory(patch.address, patch.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := machine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 430; frame++ {
		if frame == 80 || frame == 200 || frame == 350 {
			for _, pressed := range []bool{true, false} {
				if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: pressed}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := machine.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	if got := machine.wipi.Stats.PresentCount; got <= 130 {
		t.Fatalf("only %d frames presented after the draw fault", got)
	}
	// Issue 512's white authentication wait can still present frames. Require
	// an actual coloured game scene rather than accepting presentation alone.
	coloured := 0
	for y := 24; y < machine.frame.Bounds().Dy(); y += 4 {
		for x := 0; x < machine.frame.Bounds().Dx(); x += 4 {
			pixel := machine.frame.RGBAAt(x, y)
			if pixel.R != pixel.G || pixel.G != pixel.B {
				coloured++
			}
		}
	}
	if coloured < 1000 {
		t.Fatalf("only %d coloured scene samples; the title remained at authentication", coloured)
	}
}
