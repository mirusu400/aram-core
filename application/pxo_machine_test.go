package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"testing"

	pxort "github.com/mirusu400/aram-core/application/internal/pxo"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader"
	pxoloader "github.com/mirusu400/aram-core/loader/pxo"
	"github.com/mirusu400/aram-core/profile"
)

func TestPXOMachineLauncherLifecycle(t *testing.T) {
	pkg := syntheticPXOPackage()
	backend := interpreter.New()
	host := pxort.NewHost(backend)
	host.Handlers[1] = pkg.Descriptor.Entry
	runtime, err := pxort.NewRuntime(backend, pkg, host)
	if err != nil {
		t.Fatal(err)
	}
	machine := &pxoMachine{
		state:   machinecore.StateReady,
		pkg:     pkg,
		newCPU:  func() cpu.Backend { return interpreter.New() },
		backend: backend,
		runtime: runtime,
		host:    host,
		budget:  1024,
	}
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateRunning {
		t.Fatalf("state = %s, want running", machine.State())
	}
	frame := machine.Framebuffer()
	if frame == nil || frame.Bounds() != image.Rect(0, 0, 176, 220) {
		t.Fatalf("launcher frame = %v", frame)
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "up", Pressed: false}); err != nil {
		t.Fatalf("release event: %v", err)
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := machine.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := machine.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestPXOFactoryClaimsMalformedPXO1(t *testing.T) {
	data := []byte("PXO1malformed")
	_, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "malformed.pxo",
		Format:   string(loader.KindPXO),
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err == nil {
		t.Fatal("malformed PXO1 source was accepted")
	}
}

func TestPXOGuestCode(t *testing.T) {
	for key, want := range map[profile.KeyCode]uint32{
		profile.KeyLeft:   0,
		profile.KeyRight:  1,
		profile.KeyUp:     2,
		profile.KeyDown:   3,
		profile.KeySelect: 0x14,
		profile.KeyClear:  0x0f,
		profile.KeySoft1:  0x1c,
		profile.Key1:      '1',
	} {
		got, ok := pxoGuestCode(key)
		if !ok || got != want {
			t.Errorf("pxoGuestCode(%d) = (%#x, %t), want (%#x, true)", key, got, ok, want)
		}
	}
	if _, ok := pxoGuestCode(profile.KeySideUp); ok {
		t.Fatal("unsupported side key was accepted")
	}
}

func syntheticPXOPackage() *pxoloader.Package {
	image := make([]byte, 0x80)
	descriptor := pxoloader.Descriptor{
		DataStart: 0x40,
		DataSize:  0x40,
		DataEnd:   0x80,
		ZeroSize:  0x20,
		DataLoad:  0x40,
		Entry:     0x21,
		GOT:       0x40,
	}
	for index, value := range []uint32{
		descriptor.DataStart,
		descriptor.DataSize,
		descriptor.DataEnd,
		descriptor.ZeroSize,
		descriptor.DataLoad,
		descriptor.Entry,
		descriptor.GOT,
	} {
		binary.LittleEndian.PutUint32(image[index*4:], value)
	}
	copy(image[0x20:], []byte{0x70, 0x47}) // bx lr
	return &pxoloader.Package{Descriptor: descriptor, Image: image}
}
