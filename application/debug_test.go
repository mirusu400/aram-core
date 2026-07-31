package application

import (
	"bytes"
	"context"
	"errors"
	"image"
	"reflect"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	shared "github.com/mirusu400/aram-core/runtime"
	skengine "github.com/mirusu400/aram-core/skvm"
)

func TestMachineDebugSnapshotIsBoundedAndDetached(t *testing.T) {
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.WriteRegister(cpu.RegisterPC, 0x12345678); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterCPSR, cpu.StatusThumb); err != nil {
		t.Fatal(err)
	}

	machine := &Machine{
		cpu:   backend,
		state: machinecore.StateFaulted,
		lastResult: cpu.Result{
			Reason:       cpu.StopFault,
			Instructions: 77,
			PC:           0x12345678,
			Err:          errors.New("synthetic fault"),
		},
		wipi: &wipiRuntime{
			logs: []string{"guest-1", "guest-2", "guest-3"},
		},
		ktf: &ktfRuntime{
			hostTrace:           []string{"host-1", "host-2", "host-3"},
			lastJavaMethod:      "Game.paint()V",
			lastJavaThrowName:   "java/lang/RuntimeException",
			javaExceptionFrames: []string{"Game.paint(Game.java:10)"},
		},
	}

	snapshot := machine.DebugSnapshot(2)
	if snapshot.Runtime != "ktf" || snapshot.State != "faulted" {
		t.Fatalf("snapshot identity = %+v", snapshot)
	}
	if snapshot.CPU == nil ||
		snapshot.CPU.Mode != "thumb" ||
		len(snapshot.CPU.Registers) != len(debugRegisterNames) {
		t.Fatalf("CPU snapshot = %+v", snapshot.CPU)
	}
	if snapshot.LastResult == nil ||
		snapshot.LastResult.Reason != "fault" ||
		snapshot.LastResult.PCHex != "0x12345678" ||
		snapshot.LastResult.Error != "synthetic fault" {
		t.Fatalf("last result = %+v", snapshot.LastResult)
	}
	if snapshot.GuestLog.Total != 3 ||
		snapshot.GuestLog.Omitted != 1 ||
		!reflect.DeepEqual(snapshot.GuestLog.Entries, []string{"guest-2", "guest-3"}) {
		t.Fatalf("guest log = %+v", snapshot.GuestLog)
	}
	if snapshot.HostTrace.Total != 3 ||
		snapshot.HostTrace.Omitted != 1 ||
		!reflect.DeepEqual(snapshot.HostTrace.Entries, []string{"host-2", "host-3"}) {
		t.Fatalf("host trace = %+v", snapshot.HostTrace)
	}
	if snapshot.KTF == nil ||
		snapshot.KTF.LastJavaMethod != "Game.paint()V" ||
		snapshot.KTF.LastJavaThrow != "java/lang/RuntimeException" {
		t.Fatalf("KTF snapshot = %+v", snapshot.KTF)
	}

	snapshot.GuestLog.Entries[0] = "changed"
	snapshot.HostTrace.Entries[0] = "changed"
	if machine.wipi.logs[1] != "guest-2" ||
		machine.ktf.hostTrace[1] != "host-2" {
		t.Fatal("debug snapshot aliases machine-owned data")
	}
}

func TestSKVMDebugSnapshotReportsInterpreterProgress(t *testing.T) {
	machine := &skvmMachine{
		state:     machinecore.StatePaused,
		mainClass: "example/Game",
		started:   true,
		midlet:    12,
		input:     make([]machinecore.InputEvent, 3),
		vm:        &skengine.VM{Instructions: 987},
	}
	snapshot := machine.DebugSnapshot(10)
	if snapshot.Runtime != "skvm" ||
		snapshot.State != "paused" ||
		snapshot.SKVM == nil ||
		snapshot.SKVM.MainClass != "example/Game" ||
		snapshot.SKVM.Instructions != 987 ||
		snapshot.SKVM.QueuedInput != 3 {
		t.Fatalf("SKVM snapshot = %+v", snapshot)
	}
}

func TestSKVMDebugSnapshotReportsFramebufferIntegrityWithoutPixels(t *testing.T) {
	data := syntheticSKVMPackage(t)
	factory := NewFactory()
	factory.FramebufferSize = image.Pt(17, 19)
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     "game.zip",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*skvmMachine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	snapshot := machine.DebugSnapshot(10)
	framebuffer := snapshot.SKVM.Framebuffer
	if framebuffer == nil ||
		framebuffer.Sequence == 0 ||
		framebuffer.Width != 17 ||
		framebuffer.Height != 19 ||
		framebuffer.Stride != 17*4 ||
		framebuffer.Format != uint8(shared.PixelRGBA8888) ||
		framebuffer.RGBABytes != 17*19*4 ||
		len(framebuffer.RGBASHA256) != 64 ||
		!framebuffer.SnapshotHashOK ||
		!framebuffer.DescriptorValid {
		t.Fatalf("SKVM framebuffer snapshot = %+v", framebuffer)
	}
}

func TestDebugSnapshotLimitIsClamped(t *testing.T) {
	if got := normalizeDebugSnapshotLimit(0); got != DefaultDebugSnapshotEntries {
		t.Fatalf("default limit = %d", got)
	}
	if got := normalizeDebugSnapshotLimit(MaxDebugSnapshotEntries + 1); got != MaxDebugSnapshotEntries {
		t.Fatalf("maximum limit = %d", got)
	}
}
