package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader"
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
