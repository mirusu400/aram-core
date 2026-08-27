package application

import (
	"bytes"
	"context"
	"errors"
	"github.com/mirusu400/aram-core/application/internal/guest"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"image"
	"reflect"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	shared "github.com/mirusu400/aram-core/runtime"
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
		cpu:                   backend,
		state:                 machinecore.StateFaulted,
		audioGeneration:       4,
		audioEpochGuestNS:     123,
		publishedAudio:        []machinecore.AudioChunk{{PCM16: []int16{1, 2}}},
		publishedAudioSamples: 2,
		publishedAudioDropped: 9,
		lastResult: cpu.Result{
			Reason:       cpu.StopFault,
			Instructions: 77,
			PC:           0x12345678,
			Err:          errors.New("synthetic fault"),
		},
		wipi: &wipirt.Runtime{
			Logs: []string{"guest-1", "guest-2", "guest-3"},
		},
		ktf: &ktfrt.Runtime{
			HostTrace:           []string{"host-1", "host-2", "host-3"},
			LastJavaMethod:      "Game.paint()V",
			LastJavaThrowName:   "java/lang/RuntimeException",
			JavaExceptionFrames: []string{"Game.paint(Game.java:10)"},
		},
	}

	snapshot := machine.DebugSnapshot(2)
	if snapshot.Runtime != "ktf" || snapshot.State != "faulted" {
		t.Fatalf("snapshot identity = %+v", snapshot)
	}
	if snapshot.CPU == nil ||
		snapshot.CPU.Mode != "thumb" ||
		len(snapshot.CPU.Registers) != len(guest.DebugRegisterNames) {
		t.Fatalf("CPU snapshot = %+v", snapshot.CPU)
	}
	if snapshot.Execution == nil {
		t.Fatal("execution statistics are absent")
	}
	if snapshot.Audio == nil || snapshot.Audio.Generation != 4 ||
		snapshot.Audio.EpochGuestNS != 123 || snapshot.Audio.QueuedChunks != 1 ||
		snapshot.Audio.QueuedSamples != 2 || snapshot.Audio.PublishedDropped != 9 {
		t.Fatalf("audio snapshot = %+v", snapshot.Audio)
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
	if machine.wipi.Logs[1] != "guest-2" ||
		machine.ktf.HostTrace[1] != "host-2" {
		t.Fatal("debug snapshot aliases machine-owned data")
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
	machine := created.(*skvmhost.Machine)
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
	if got := guest.NormalizeDebugSnapshotLimit(0); got != DefaultDebugSnapshotEntries {
		t.Fatalf("default limit = %d", got)
	}
	if got := guest.NormalizeDebugSnapshotLimit(MaxDebugSnapshotEntries + 1); got != MaxDebugSnapshotEntries {
		t.Fatalf("maximum limit = %d", got)
	}
}
