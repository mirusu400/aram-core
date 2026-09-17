package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/gnex"
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
	const entry, event, ds, ps, dm = 54, 64, 128, 132, 136
	sgs := make([]byte, dm)
	sgs[0], sgs[2], sgs[5], sgs[10] = 2, 12, 1, 'G'
	for offset, value := range map[int]uint16{
		0x1c: entry, 0x20: event, 0x2c: ds, 0x2e: ps, 0x30: dm, 0x32: dm,
	} {
		binary.LittleEndian.PutUint16(sgs[offset:], value)
	}
	// Primary dispatch requests a timer and still reaches halt.
	copy(sgs[entry:], []byte{0x06, 0, 10, 0x06, 0x12, 0x34, 0x9a, 0xff})
	// The selected event fills white, presents one frame, and halts.
	copy(sgs[event:], []byte{0x05, 0, 0x57, 0x78, 0xff})
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
		eventEntry:  uint32(binary.LittleEndian.Uint16(pkg.SGS[0x20:0x22])),
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

func TestGVMOperationalProfileRequiresExactOuterHash(t *testing.T) {
	if GVMOperationalProfileID == GVMKernelProfileID {
		t.Fatal("operational profile aliases diagnostic profile")
	}
	data := gvmOperationalArchive(t)
	_, err := NewFactory().Create(context.Background(), gvmSource(data, GVMOperationalProfileID))
	if err == nil || !strings.Contains(err.Error(), GVMOperationalSHA256) || !strings.Contains(err.Error(), "got ") {
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
	if frame == nil || frame.Bounds() != image.Rect(0, 0, 120, 80) {
		t.Fatalf("frame bounds = %v", frame)
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
