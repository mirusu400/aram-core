package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/gnex"
	shared "github.com/mirusu400/aram-core/runtime"
)

func gvmExecutionArchive(t *testing.T, code []byte) []byte {
	t.Helper()
	const entry, ds, ps, dm = 54, 128, 132, 136
	sgs := make([]byte, dm)
	sgs[0], sgs[2], sgs[5], sgs[10] = 2, 12, 1, 'G'
	for offset, value := range map[int]uint16{
		0x1c: entry,
		0x2c: ds,
		0x2e: ps,
		0x30: dm,
		0x32: dm,
	} {
		binary.LittleEndian.PutUint16(sgs[offset:], value)
	}
	copy(sgs[entry:], code)
	copy(sgs[ds:], []byte{1, 2, 1, 0})
	copy(sgs[ps:], []byte{1, 2, 3, 4})
	return testZIP(t, map[string][]byte{"game.sgs": sgs})
}

func gvmOperationalArchive(t *testing.T) []byte {
	t.Helper()
	const entry, event, input, ds, ps, dm = 54, 64, 72, 128, 132, 136
	sgs := make([]byte, dm)
	sgs[0], sgs[2], sgs[5], sgs[10] = 2, 12, 1, 'G'
	for offset, value := range map[int]uint16{
		0x1c: entry, 0x20: event, 0x22: input, 0x2c: ds, 0x2e: ps, 0x30: dm, 0x32: dm,
	} {
		binary.LittleEndian.PutUint16(sgs[offset:], value)
	}
	// Primary dispatch requests a timer and still reaches halt.
	copy(sgs[entry:], []byte{0x06, 0, 10, 0x06, 0x12, 0x34, 0x9a, 0xff})
	// The selected event fills white, presents one frame, and halts.
	copy(sgs[event:], []byte{0x05, 0, 0x57, 0x78, 0xff})
	// Numeric press fills black, presents one frame, and halts.
	copy(sgs[input:], []byte{0x05, 3, 0x57, 0x78, 0xff})
	copy(sgs[ds:], []byte{1, 2, 1, 0})
	copy(sgs[ps:], []byte{1, 2, 3, 4})
	return testZIP(t, map[string][]byte{"game.sgs": sgs})
}

func newOperationalFixture(t *testing.T, data []byte) *gvmMachine {
	t.Helper()
	pkg, err := gnex.Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	m := &gvmMachine{
		state:       machinecore.StateReady,
		source:      gvmSource(data, GVMOperationalProfileID),
		packageSGS:  bytes.Clone(pkg.SGS),
		budget:      100,
		operational: true,
		width:       operationalGVMWidth,
		height:      operationalGVMHeight,
		eventEntry:  uint32(binary.LittleEndian.Uint16(pkg.SGS[0x20:0x22])),
		inputEntry:  uint32(binary.LittleEndian.Uint16(pkg.SGS[0x22:0x24])),
	}
	if err := m.resetVMLocked(); err != nil {
		t.Fatal(err)
	}
	return m
}

func gvmSource(data []byte, profile string) machinecore.Source {
	return machinecore.Source{
		Name:      "game.zip",
		Format:    string(loader.KindGNEX),
		ProfileID: profile,
		ReaderAt:  bytes.NewReader(data),
		Size:      int64(len(data)),
	}
}

func TestGVMFactoryRequiresExplicitDiagnosticProfile(t *testing.T) {
	data := gvmExecutionArchive(t, []byte{0xff})
	_, err := NewFactory().Create(context.Background(), gvmSource(data, ""))
	var unsupported *UnsupportedPlatformError
	if !errors.As(err, &unsupported) || unsupported.Kind != loader.KindGNEX {
		t.Fatalf("default GNEX behavior changed: %v", err)
	}
}

func TestGVMFactoryAutomaticallySelectsQualifiedOperationalCorpus(t *testing.T) {
	data := gvmOperationalArchive(t)
	digest := sha256.Sum256(data)
	hash := fmt.Sprintf("%x", digest)
	gvmOperationalCorpora[hash] = gvmOperationalConfig{width: 120, height: 80}
	t.Cleanup(func() { delete(gvmOperationalCorpora, hash) })
	machine, err := NewFactory().Create(context.Background(), gvmSource(data, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	provider, ok := machine.(interface{ SourceInfo() machinecore.Source })
	if !ok {
		t.Fatalf("automatic machine does not expose SourceInfo: %T", machine)
	}
	if provider.SourceInfo().ProfileID != GVMOperationalProfileID || provider.SourceInfo().Format != string(loader.KindGNEX) {
		t.Fatalf("automatic source = %+v, provider=%v", provider.SourceInfo(), ok)
	}
}

func TestGVMOperationalProfileRequiresExactOuterHash(t *testing.T) {
	if GVMOperationalProfileID == GVMKernelProfileID {
		t.Fatal("operational profile aliases diagnostic profile")
	}
	data := gvmOperationalArchive(t)
	_, err := NewFactory().Create(context.Background(), gvmSource(data, GVMOperationalProfileID))
	if err == nil || !strings.Contains(err.Error(), "does not support outer SHA-256") {
		t.Fatalf("hash gate error = %v", err)
	}
}

func TestGVMOperationalLifecyclePresentationAndReset(t *testing.T) {
	machine := newOperationalFixture(t, gvmOperationalArchive(t))
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateRunning || !machine.vm.Halted() {
		t.Fatalf("startup state=%s halted=%v", machine.State(), machine.vm.Halted())
	}
	boundary, ok := machine.GVMDiagnosticBoundary()
	if !ok || boundary.Interval != 10 || boundary.Selector != 0x1234 {
		t.Fatalf("stored timer request=%+v present=%v", boundary, ok)
	}
	if machine.Framebuffer() != nil || machine.GVMPresentCount() != 0 {
		t.Fatal("startup manufactured a frame")
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	frame := machine.Framebuffer()
	if frame == nil || frame.Bounds() != image.Rect(0, 0, int(operationalGVMWidth), int(operationalGVMHeight)) {
		t.Fatalf("frame bounds = %v", frame.Bounds())
	}
	if got := color.RGBAModel.Convert(frame.At(0, 0)).(color.RGBA); got != (color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
		t.Fatalf("frame pixel = %#v", got)
	}
	if _, mutable := frame.(interface{ Set(int, int, color.Color) }); mutable {
		t.Fatal("framebuffer exposed mutable image")
	}
	if machine.GVMPresentCount() != 1 || machine.State() != machinecore.StateRunning || !machine.vm.Halted() {
		t.Fatalf("event state=%s halted=%v presents=%d", machine.State(), machine.vm.Halted(), machine.GVMPresentCount())
	}
	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateReady || machine.Framebuffer() != nil || machine.GVMPresentCount() != 0 {
		t.Fatalf("reset state=%s frame=%v presents=%d", machine.State(), machine.Framebuffer(), machine.GVMPresentCount())
	}
	// Previously published snapshots remain valid and unchanged after reset.
	if got := color.RGBAModel.Convert(frame.At(0, 0)).(color.RGBA); got.R != 0xff || got.A != 0xff {
		t.Fatalf("published snapshot changed after reset: %#v", got)
	}
}

func TestGVMOperationalPauseResumeControlsFrameDispatch(t *testing.T) {
	machine := newOperationalFixture(t, gvmOperationalArchive(t))
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StatePaused {
		t.Fatalf("paused state = %s", machine.State())
	}
	if err := machine.StepFrame(context.Background()); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("step while paused: %v", err)
	}
	if machine.GVMPresentCount() != 0 {
		t.Fatalf("paused step dispatched guest work: presents=%d", machine.GVMPresentCount())
	}
	if err := machine.Resume(); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateRunning || !machine.vm.Halted() {
		t.Fatalf("resumed state=%s halted=%v", machine.State(), machine.vm.Halted())
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.GVMPresentCount() != 1 {
		t.Fatalf("resumed frame presents=%d", machine.GVMPresentCount())
	}
}

func TestGVMDecodedMediaProducesTimestampedPCMAndResetMarker(t *testing.T) {
	clock, err := shared.NewClock(shared.DefaultWallEpochMillis, 9*60, "ko-KR")
	if err != nil {
		t.Fatal(err)
	}
	media, err := newGVMDecodedMediaServices(clock)
	if err != nil {
		t.Fatal(err)
	}
	encoded := append([]byte{2, 0}, presentationTestWave()...)
	if err := media.LoadGVMMedia(7, encoded); err != nil {
		t.Fatal(err)
	}
	if err := media.advance(time.Second / 60); err != nil {
		t.Fatal(err)
	}
	machine := &gvmMachine{
		operational:         true,
		clock:               clock,
		mediaServices:       media,
		audioGeneration:     1,
		mediaOutputRevision: 0,
	}
	chunk := machine.DrainAudio()
	if err := chunk.Validate(); err != nil {
		t.Fatal(err)
	}
	if chunk.SampleRate != 44_100 || chunk.Channels != 1 || len(chunk.PCM16) == 0 {
		t.Fatalf("decoded GVM audio = %+v", chunk)
	}
	if chunk.StartGuestNS != 0 || chunk.StartSample != 0 || chunk.Generation != 2 {
		t.Fatalf("first GVM audio timeline = %+v", chunk)
	}
	if err := media.ResetGVMAudio(5); err != nil {
		t.Fatal(err)
	}
	marker := machine.DrainAudio()
	if err := marker.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(marker.PCM16) != 0 || marker.Generation != 3 ||
		marker.StartGuestNS != int64(time.Second/60) {
		t.Fatalf("GVM reset marker = %+v", marker)
	}
}

func TestGVMDecodedMediaRejectsUnknownPayloadAtomically(t *testing.T) {
	clock, err := shared.NewClock(shared.DefaultWallEpochMillis, 9*60, "ko-KR")
	if err != nil {
		t.Fatal(err)
	}
	media, err := newGVMDecodedMediaServices(clock)
	if err != nil {
		t.Fatal(err)
	}
	encoded := append([]byte{2, 0}, presentationTestWave()...)
	if err := media.LoadGVMMedia(1, encoded); err != nil {
		t.Fatal(err)
	}
	revision := media.revision
	if err := media.LoadGVMMedia(2, []byte{2, 0, 'B', 'A', 'D', '!'}); !errors.Is(err, shared.ErrMediaUnsupported) {
		t.Fatalf("unsupported GVM media: %v", err)
	}
	if media.revision != revision {
		t.Fatalf("failed replacement changed revision: got %d want %d", media.revision, revision)
	}
	if err := media.advance(time.Second / 60); err != nil {
		t.Fatal(err)
	}
	audio, _, _ := media.drainTimed()
	if len(audio.PCM16) == 0 {
		t.Fatal("failed replacement discarded the active clip")
	}
}

func TestGVMAudioStopAndCloseDiscardBufferedOutput(t *testing.T) {
	for _, action := range []struct {
		name string
		run  func(*gvmMachine) error
	}{
		{name: "stop", run: func(machine *gvmMachine) error { return machine.Stop() }},
		{name: "close", run: func(machine *gvmMachine) error { return machine.Close() }},
	} {
		t.Run(action.name, func(t *testing.T) {
			clock, err := shared.NewClock(shared.DefaultWallEpochMillis, 9*60, "ko-KR")
			if err != nil {
				t.Fatal(err)
			}
			media, err := newGVMDecodedMediaServices(clock)
			if err != nil {
				t.Fatal(err)
			}
			if err := media.LoadGVMMedia(1, append([]byte{2, 0}, presentationTestWave()...)); err != nil {
				t.Fatal(err)
			}
			if err := media.advance(time.Second / 60); err != nil {
				t.Fatal(err)
			}
			machine := &gvmMachine{
				state: machinecore.StateRunning, operational: true,
				clock: clock, mediaServices: media, audioGeneration: 1,
			}
			if err := action.run(machine); err != nil {
				t.Fatal(err)
			}
			marker := machine.DrainAudio()
			if err := marker.Validate(); err != nil || marker.Generation != 2 ||
				len(marker.PCM16) != 0 || marker.StartGuestNS != int64(time.Second/60) {
				t.Fatalf("%s audio marker=%+v err=%v", action.name, marker, err)
			}
		})
	}
}

func TestGVMOperationalVolatileStateRoundTrip(t *testing.T) {
	archive := gvmOperationalArchive(t)
	machine := newOperationalFixture(t, archive)
	digest := sha256.Sum256(archive)
	machine.source.SHA256 = fmt.Sprintf("%x", digest)
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := machine.SaveState(&encoded); err != nil {
		t.Fatal(err)
	}
	saved := bytes.Clone(encoded.Bytes())

	if err := machine.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "num5", Pressed: true}); err != nil {
		t.Fatal(err)
	}
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	if got := color.RGBAModel.Convert(machine.Framebuffer().At(0, 0)).(color.RGBA); got.R == 0xff {
		t.Fatalf("mutation did not replace saved white frame: %#v", got)
	}
	if err := machine.LoadState(bytes.NewReader(saved)); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StatePaused || machine.GVMPresentCount() != 1 {
		t.Fatalf("restored lifecycle=%s presents=%d", machine.State(), machine.GVMPresentCount())
	}
	frame := machine.Framebuffer()
	if got := color.RGBAModel.Convert(frame.At(0, 0)).(color.RGBA); got != (color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
		t.Fatalf("restored frame pixel = %#v", got)
	}
	if _, mutable := frame.(interface{ Set(int, int, color.Color) }); mutable {
		t.Fatal("restored framebuffer exposed mutable image")
	}
	marker := machine.DrainAudio()
	if err := marker.Validate(); err != nil || marker.Generation != 2 || len(marker.PCM16) != 0 {
		t.Fatalf("load-state audio marker=%+v err=%v", marker, err)
	}
	if err := machine.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.GVMPresentCount() != 2 {
		t.Fatalf("restored execution did not resume: presents=%d", machine.GVMPresentCount())
	}
}

func TestGVMOperationalStateRestoresActiveDecodedAudio(t *testing.T) {
	archive := gvmOperationalArchive(t)
	machine := newOperationalFixture(t, archive)
	digest := sha256.Sum256(archive)
	machine.source.SHA256 = fmt.Sprintf("%x", digest)
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	encoded := append([]byte{2, 0}, presentationTestWave()...)
	if err := machine.mediaServices.LoadGVMMedia(3, encoded); err != nil {
		t.Fatal(err)
	}
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	var encodedState bytes.Buffer
	if err := machine.SaveState(&encodedState); err != nil {
		t.Fatal(err)
	}
	if err := machine.mediaServices.ResetGVMAudio(5); err != nil {
		t.Fatal(err)
	}
	if err := machine.LoadState(bytes.NewReader(encodedState.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := machine.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	chunk := machine.DrainAudio()
	if err := chunk.Validate(); err != nil {
		t.Fatal(err)
	}
	if chunk.Generation != 2 || chunk.SampleRate != 44_100 || chunk.Channels != 1 ||
		len(chunk.PCM16) == 0 || chunk.StartGuestNS != 0 || chunk.StartSample != 0 {
		t.Fatalf("restored GVM active audio = %+v", chunk)
	}
}

func TestGVMOperationalStateRejectsCorruptionWithoutMutation(t *testing.T) {
	archive := gvmOperationalArchive(t)
	machine := newOperationalFixture(t, archive)
	digest := sha256.Sum256(archive)
	machine.source.SHA256 = fmt.Sprintf("%x", digest)
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := machine.SaveState(&encoded); err != nil {
		t.Fatal(err)
	}
	corrupt := bytes.Clone(encoded.Bytes())
	corrupt[len(corrupt)-1] ^= 0x80
	before := color.RGBAModel.Convert(machine.Framebuffer().At(0, 0)).(color.RGBA)
	if err := machine.LoadState(bytes.NewReader(corrupt)); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("corrupt load error = %v", err)
	}
	after := color.RGBAModel.Convert(machine.Framebuffer().At(0, 0)).(color.RGBA)
	if machine.State() != machinecore.StatePaused || machine.GVMPresentCount() != 1 || after != before {
		t.Fatal("failed state load mutated the live GVM machine")
	}
}

func TestGVMOperationalPressDispatch(t *testing.T) {
	machine := newOperationalFixture(t, gvmOperationalArchive(t))
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "num5", Pressed: true}); err != nil {
		t.Fatal(err)
	}
	frame := machine.Framebuffer()
	if got := color.RGBAModel.Convert(frame.At(0, 0)).(color.RGBA); got == (color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) || got.A != 0xff {
		t.Fatalf("numeric press did not replace the white frame: %#v", got)
	}
	if machine.GVMPresentCount() != 2 || machine.State() != machinecore.StateRunning || !machine.vm.Halted() {
		t.Fatalf("input state=%s halted=%v presents=%d", machine.State(), machine.vm.Halted(), machine.GVMPresentCount())
	}
	input := machine.GVMInputDispatchDiagnostics()
	if input.DispatchCount != 1 || input.GuestCode != 5 || input.Result.Instructions == 0 || input.Result.Reason != cpu.StopExited {
		t.Fatalf("input diagnostics = %+v", input)
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "num5", Pressed: false}); err != nil {
		t.Fatalf("release should clear host state without guest dispatch: %v", err)
	}
	if machine.GVMPresentCount() != 2 {
		t.Fatalf("release manufactured a guest dispatch: presents=%d", machine.GVMPresentCount())
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "up", Pressed: true}); err != nil {
		t.Fatalf("directional press: %v", err)
	}
	input = machine.GVMInputDispatchDiagnostics()
	if input.DispatchCount != 2 || input.GuestCode != 16 || input.Result.Instructions == 0 || input.Result.Reason != cpu.StopExited {
		t.Fatalf("directional input diagnostics = %+v", input)
	}
	if machine.GVMPresentCount() != 3 {
		t.Fatalf("directional press did not dispatch guest frame: presents=%d", machine.GVMPresentCount())
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "up", Pressed: false}); err != nil {
		t.Fatalf("directional release should clear host state without guest dispatch: %v", err)
	}
	if machine.GVMPresentCount() != 3 {
		t.Fatalf("directional release manufactured a guest dispatch: presents=%d", machine.GVMPresentCount())
	}
}

func TestGVMDiagnosticMachineStopsAtTimerBoundary(t *testing.T) {
	data := gvmExecutionArchive(t, []byte{
		0x06, 0, 10,
		0x06, 0x12, 0x34,
		0x9a,
		0xff,
	})
	factory := NewFactory()
	factory.FrameRunBudget = 100
	machine, err := factory.Create(context.Background(), gvmSource(data, GVMKernelProfileID))
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	if machine.State() != machinecore.StateReady {
		t.Fatalf("initial state %s", machine.State())
	}
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateStopped {
		t.Fatalf("boundary state %s", machine.State())
	}
	provider, ok := machine.(interface{ LastResult() cpu.Result })
	if !ok {
		t.Fatal("missing execution diagnostics")
	}
	result := provider.LastResult()
	if result.Reason != cpu.StopBreakpoint || result.Instructions != 2 || result.PC != 61 || result.Err != nil {
		t.Fatalf("boundary result %+v", result)
	}
	boundary, ok := machine.(interface {
		GVMDiagnosticBoundary() (GVMDiagnosticBoundary, bool)
	}).GVMDiagnosticBoundary()
	if !ok || boundary.Kind != "timer-request" || boundary.Interval != 10 || boundary.Selector != 0x1234 {
		t.Fatalf("service boundary %+v present=%v", boundary, ok)
	}
	if frame := machine.Framebuffer(); frame != nil {
		t.Fatalf("diagnostic profile manufactured a video frame: %v", frame.Bounds())
	}
	identity, ok := machine.(interface{ SourceInfo() machinecore.Source })
	if !ok {
		t.Fatal("missing source identity")
	}
	if got := identity.SourceInfo(); got.Format != string(loader.KindGNEX) || got.ProfileID != GVMKernelProfileID {
		t.Fatalf("identity %+v", got)
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: true}); !errors.Is(err, ErrGVMInputUnavailable) {
		t.Fatalf("input boundary: %v", err)
	}
	if err := machine.SaveState(new(bytes.Buffer)); !errors.Is(err, ErrGVMStateUnavailable) {
		t.Fatalf("state boundary: %v", err)
	}
	if err := machine.Reset(context.Background()); err != nil || machine.State() != machinecore.StateReady {
		t.Fatalf("reset: state=%s err=%v", machine.State(), err)
	}
	if err := machine.Start(context.Background()); err != nil || provider.LastResult().Reason != cpu.StopBreakpoint {
		t.Fatalf("repeat boundary: %+v err=%v", provider.LastResult(), err)
	}
}

func TestGVMDiagnosticMachineReportsGuestExit(t *testing.T) {
	data := gvmExecutionArchive(t, []byte{0xff})
	factory := NewFactory()
	factory.FrameRunBudget = 10
	machine, err := factory.Create(context.Background(), gvmSource(data, GVMKernelProfileID))
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := machine.(interface{ LastResult() cpu.Result }).LastResult()
	if machine.State() != machinecore.StateStopped || result.Reason != cpu.StopExited || result.Instructions != 1 || result.PC != 55 {
		t.Fatalf("exit state=%s result=%+v", machine.State(), result)
	}
}

func TestGVMDiagnosticMachineReportsUnsupportedOpcode(t *testing.T) {
	data := gvmExecutionArchive(t, []byte{0x7d})
	machine, err := NewFactory().Create(context.Background(), gvmSource(data, GVMKernelProfileID))
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	err = machine.Start(context.Background())
	if err == nil || machine.State() != machinecore.StateFaulted {
		t.Fatalf("fault state=%s err=%v", machine.State(), err)
	}
	result := machine.(interface{ LastResult() cpu.Result }).LastResult()
	if result.Reason != cpu.StopFault || result.Instructions != 0 || result.PC != 55 || result.Err == nil {
		t.Fatalf("fault result %+v", result)
	}
}
