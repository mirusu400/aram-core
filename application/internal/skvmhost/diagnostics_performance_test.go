package skvmhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

func idleDiagnosticMachine(t *testing.T) *Machine {
	t.Helper()
	m, err := newJavaMachine(context.Background(), machinecore.Source{ProfileID: "j2me-1.0/generic/generic", SHA256: strings.Repeat("0", 64)}, Application{}, nil, image.Pt(240, 320), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	for _, state := range []shared.LifecycleState{shared.LifecycleRunning, shared.LifecyclePaused} {
		if err := m.services.Coordinator.Transition(m.owner, state, 0, m.services.Events); err != nil {
			t.Fatal(err)
		}
	}
	m.state = machinecore.StatePaused
	if err := m.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m
}

// Count allocations rather than elapsed time: diagnostic polling must not
// materialize framebuffer/VM/service state on an unchanged presentation.
func TestJavaIdleDiagnosticsReuseVerifiedFrame(t *testing.T) {
	m := idleDiagnosticMachine(t)
	first := m.DebugSnapshot(1).SKVM.Framebuffer
	if first == nil || !first.SnapshotHashOK || !first.DescriptorValid {
		t.Fatalf("frame = %+v", first)
	}
	before, err := m.vm.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(100, func() {
		got := m.DebugSnapshot(1)
		if got.SKVM.Framebuffer == nil {
			panic("missing frame")
		}
	})
	if allocations > 3 {
		t.Fatalf("unchanged diagnostics allocate %.0f objects, want at most 3 (no pixel/state copies)", allocations)
	}
	after, err := m.vm.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("diagnostics changed serializable state")
	}
	first.RGBASHA256 = "caller mutation"
	if m.DebugSnapshot(1).SKVM.Framebuffer.RGBASHA256 == first.RGBASHA256 {
		t.Fatal("snapshot aliases cache")
	}
	polled := m.DebugSnapshot(1).SKVM.Framebuffer
	polled.RGBASHA256 = "cached caller mutation"
	if m.DebugSnapshot(1).SKVM.Framebuffer.RGBASHA256 == polled.RGBASHA256 {
		t.Fatal("cached snapshot aliases cache")
	}
	if err := m.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	next := m.DebugSnapshot(1).SKVM.Framebuffer
	if next.Sequence != first.Sequence+1 || !next.SnapshotHashOK || !next.DescriptorValid {
		t.Fatalf("clean commit frame = %+v", next)
	}
	allocations = testing.AllocsPerRun(100, func() {
		if err := m.StepFrame(context.Background()); err != nil {
			panic(err)
		}
		_ = m.DebugSnapshot(1)
	})
	if allocations > 3 {
		t.Fatalf("idle step + diagnostics allocate %.0f objects, want at most 3 (commit must not copy discarded pixels)", allocations)
	}
	// Changed pixels must still be copied and independently hashed.
	if err := m.services.Graphics.SetPixel(m.owner, m.vm.ScreenSurface(), 0, 0, shared.RGB(255, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := m.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	changed := m.DebugSnapshot(1).SKVM.Framebuffer
	actual := sha256.Sum256(m.services.Graphics.LastFrame().RGBA)
	if changed.RGBASHA256 == next.RGBASHA256 || changed.RGBASHA256 != fmt.Sprintf("%x", actual) || !changed.SnapshotHashOK || !changed.DescriptorValid {
		t.Fatalf("changed frame = %+v", changed)
	}
}

func TestJavaIdleDiagnosticsAdvanceTimers(t *testing.T) {
	m := idleDiagnosticMachine(t)
	now := m.services.Clock.Monotonic()
	// A service-owned timer is dispatched through the host handler rather than
	// pretending its value is a Java TimerTask reference.
	timerOwner, err := m.services.Coordinator.Register("diagnostic-timer", 1000)
	if err != nil {
		t.Fatal(err)
	}
	timer, err := m.services.Timers.Define(timerOwner, "diagnostic-idle")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.services.Timers.Set(timer, timerOwner, now+time.Millisecond, 0, 7); err != nil {
		t.Fatal(err)
	}
	if err := m.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = m.DebugSnapshot(1)
	fired, err := m.services.Timers.Get(timer, timerOwner)
	if err != nil || fired.Active {
		t.Fatalf("timer not consumed: %+v %v", fired, err)
	}
}

func TestJavaIdleDiagnosticsPreserveServicesAndStateReplay(t *testing.T) {
	m := idleDiagnosticMachine(t)
	ctx := context.Background()
	now := m.services.Clock.Monotonic()
	if err := m.QueueInput(machinecore.InputEvent{Control: "up", Pressed: true, At: now + 2*time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	clip, err := m.services.Media.CreateClip(m.owner, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.services.Media.Append(m.owner, clip, skvmHostTestWave()); err != nil {
		t.Fatal(err)
	}
	if err := m.services.Media.Play(m.owner, clip, -1); err != nil {
		t.Fatal(err)
	}
	var saved bytes.Buffer
	if err := m.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	initialSequence := m.DebugSnapshot(1).SKVM.Framebuffer.Sequence
	for range 8 {
		if err := m.StepFrame(ctx); err != nil {
			t.Fatal(err)
		}
		_ = m.DebugSnapshot(1)
	}
	if got := m.services.Clock.Monotonic(); got != now+8*m.services.Config.FrameDuration {
		t.Fatalf("guest time = %s", got)
	}
	if len(m.input) != 0 {
		t.Fatal("scheduled input not consumed")
	}
	if got := m.DebugSnapshot(1); got.SKVM.CurrentDisplay != 0 || got.SKVM.Framebuffer.Sequence != initialSequence+8 {
		t.Fatalf("idle progress = %+v", got.SKVM)
	}
	var advanced bytes.Buffer
	if err := m.SaveState(&advanced); err != nil {
		t.Fatal(err)
	}
	audio := m.DrainAudio()
	if len(audio.PCM16) == 0 {
		t.Fatal("idle stepping did not advance audio")
	}
	// Reload the identical state first. Its surface ID, pixels and sequence
	// are unchanged, but validation must not retain a pre-restore cache.
	if err := m.LoadState(bytes.NewReader(advanced.Bytes())); err != nil {
		t.Fatal(err)
	}
	if m.debugFramebuffer != nil {
		t.Fatal("same-sequence LoadState retained diagnostic cache")
	}
	_ = m.DebugSnapshot(1)
	if err := m.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	if m.debugFramebuffer != nil {
		t.Fatal("LoadState retained diagnostic cache")
	}
	if got := m.DebugSnapshot(1).SKVM.Framebuffer.Sequence; got != initialSequence {
		t.Fatalf("restore retained stale presentation %d", got)
	}
	for range 8 {
		if err := m.StepFrame(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var replay bytes.Buffer
	if err := m.SaveState(&replay); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(advanced.Bytes(), replay.Bytes()) {
		t.Fatal("diagnostic polling changed state replay")
	}
	if err := m.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if m.debugFramebuffer != nil {
		t.Fatal("Reset retained diagnostic cache")
	}
	if got := m.DebugSnapshot(1).SKVM.Framebuffer; got != nil {
		t.Fatalf("reset retained frame %+v", got)
	}
}
