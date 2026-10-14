package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

func TestIssue528BREWStartsAndPresentsGuestArtwork(t *testing.T) {
	path, data := findAuthorizedPackage(t, "9317430fa24fedeffeee121b5440afae6c446ee4ff737cf94751aef2c18fc018")
	m := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, m, 180)
	if stats, _ := m.BREWFrameStats(); !stats.FrameValid || stats.PresentCount == 0 {
		t.Fatalf("no guest frame: %+v", stats)
	}
	if got := brewColoredPixelCount(m.Framebuffer()); got < 100 {
		t.Fatalf("guest artwork has only %d colored pixels", got)
	}
}

func TestIssue527AnimaUsesCompletePrimarySurface(t *testing.T) {
	path, data := findAuthorizedPackage(t, "3ff5948e235e86e9e6f490666b0389c05a5171aefd36729b73639be138875206")
	factory := NewFactory()
	factory.RunBudget = DefaultHandsetRunBudget
	factory.RaptorFrameRunBudget = DefaultRaptorFrameRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{Name: filepath.Base(path), Path: path, ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
	check(t, err)
	m := created.(*Machine)
	t.Cleanup(func() { _ = m.Close() })
	if got := m.wipi.ScreenDrawingOriginY; got != 0 {
		t.Fatalf("screen origin = %d, want 0", got)
	}
	check(t, m.Start(context.Background()))
	for i := 0; i < 600; i++ {
		check(t, m.StepFrame(context.Background()))
	}
	if m.wipi.Stats.PresentCount == 0 {
		t.Fatal("Anima did not present a guest frame")
	}
}

func TestIssue526BREWHeldDirectionContinuesMoving(t *testing.T) {
	path, data := findAuthorizedPackage(t, "07cf7d875d1e7e12deed532fe9e979583b816efa6a04672bec049776c1b5b850")
	m := newBREWReferenceMachine(t, path, data)
	// Replay the report's short taps to reach the game, then hold one direction.
	queueBREWReportedInputs(t, m, []brewReportedKeyHold{
		{"ok", 1456, 1568},
		{"ok", 3456, 3568},
		{"ok", 4352, 4448},
		{"ok", 4976, 5072},
		{"ok", 5520, 5616},
		{"ok", 6320, 6416},
		{"right", 7184, 7296},
		{"back", 7712, 7808},
		{"back", 8384, 8480},
		{"down", 8768, 8864},
		{"ok", 9040, 9152},
		{"ok", 9584, 9680},
		{"ok", 10352, 10448},
		{"ok", 11104, 11184},
		{"down", 11600, 11712},
		{"back", 12240, 12320},
		{"up", 12768, 12864},
		{"down", 13440, 13520},
		{"ok", 13872, 13968},
		{"ok", 14528, 14624},
		{"ok", 15168, 15264},
		{"ok", 15920, 16016},
		{"ok", 16800, 16896},
		{"right", 17248, 17344},
		{"ok", 17600, 17696},
		{"ok", 17968, 18064},
		{"ok", 19472, 19584},
		{"ok", 20096, 20192},
		{"ok", 20352, 20448},
		{"ok", 20544, 20624},
		{"ok", 20704, 20784},
		{"ok", 20896, 20960},
		{"ok", 21072, 21168},
		{"ok", 21264, 21328},
		{"ok", 21424, 21488},
		{"ok", 21584, 21664},
		{"ok", 21760, 21840},
		{"ok", 21936, 22016},
		{"ok", 22112, 22176},
		{"ok", 22272, 22352},
		{"ok", 22432, 22544},
		{"ok", 22624, 22752},
		{"ok", 22816, 22928},
		{"ok", 23008, 23104},
		{"ok", 23200, 23296},
		{"ok", 23392, 23472},
		{"ok", 23568, 23648},
		{"ok", 23744, 23824},
		{"ok", 23920, 24016},
		{"ok", 24128, 24224},
		{"ok", 24320, 24416},
		{"ok", 24512, 24624},
		{"ok", 24704, 24800},
		{"ok", 24912, 25008},
		{"ok", 25088, 25200},
		{"ok", 25280, 25392},
		{"ok", 25488, 25584},
		{"ok", 25680, 25776},
		{"ok", 25856, 25936},
		{"ok", 26032, 26128},
		{"ok", 26224, 26336},
		{"ok", 26416, 26512},
		{"ok", 26608, 26704},
		{"ok", 29120, 29216},
	})
	stepBREWReference(t, m, 2000)
	check(t, m.QueueInput(machinecore.InputEvent{Control: "right", Pressed: true}))
	before := m.runtime.EventCount(0x100)
	frames := make(map[[32]byte]bool)
	for i := 0; i < 3; i++ {
		stepBREWReference(t, m, 30)
		frames[brewFrameHash(m.Framebuffer())] = true
	}
	if len(frames) != 3 {
		t.Fatalf("held direction produced %d distinct game positions, want 3", len(frames))
	}
	if got := m.runtime.EventCount(0x100) - before; got < 8 {
		t.Fatalf("held direction delivered only %d legacy key events", got)
	}
	check(t, m.QueueInput(machinecore.InputEvent{Control: "right"}))
	stepBREWReference(t, m, 1)
	before = m.runtime.EventCount(0x100)
	stepBREWReference(t, m, 60)
	if got := m.runtime.EventCount(0x100); got != before {
		t.Fatalf("movement kept repeating after release: %d -> %d", before, got)
	}
}
