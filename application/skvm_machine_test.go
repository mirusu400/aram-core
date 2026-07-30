package application

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestFactoryCreatesSKVMMachineWithSharedServices(t *testing.T) {
	data := syntheticSKVMPackage(t)
	cpuCreations := 0
	factory := NewFactory()
	factory.FramebufferSize = image.Pt(17, 19)
	factory.NewCPU = func() cpu.Backend {
		cpuCreations++
		return interpreter.New()
	}
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     "game.zip",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine, ok := created.(*skvmMachine)
	if !ok {
		t.Fatalf("Factory.Create returned %T, want *skvmMachine", created)
	}
	t.Cleanup(func() { _ = machine.Close() })
	if cpuCreations != 0 {
		t.Fatalf("Factory created %d ARM backends for an SKVM package", cpuCreations)
	}
	if machine.State() != machinecore.StateReady {
		t.Fatalf("initial state = %s, want ready", machine.State())
	}
	if bounds := machine.Framebuffer().Bounds(); bounds.Dx() != 17 || bounds.Dy() != 19 {
		t.Fatalf("framebuffer bounds = %v, want 17x19", bounds)
	}
	if got := machine.services.Config.Device.ProfileID; got != skvmProfileID {
		t.Fatalf("profile = %q, want %q", got, skvmProfileID)
	}
}

func TestSKVMMachineLifecycleResetAndStateRoundTrip(t *testing.T) {
	data := syntheticSKVMPackage(t)
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
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
	if machine.State() != machinecore.StatePaused {
		t.Fatalf("state after start = %s, want paused", machine.State())
	}
	if err := machine.QueueInput(machinecore.InputEvent{
		Control: "up",
		Pressed: true,
		At:      5 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	savedClock := machine.services.Clock.Monotonic()
	savedInstructions := machine.vm.Instructions
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}

	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.services.Clock.Monotonic() == savedClock {
		t.Fatal("second frame did not advance virtual time")
	}
	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	if machine.services.Clock.Monotonic() != savedClock ||
		machine.vm.Instructions != savedInstructions ||
		machine.State() != machinecore.StatePaused {
		t.Fatalf(
			"restored state clock=%s instructions=%d lifecycle=%s",
			machine.services.Clock.Monotonic(),
			machine.vm.Instructions,
			machine.State(),
		)
	}
	if err := machine.services.Storage.WriteFile(
		shared.NamespacePrivate,
		"/save.dat",
		[]byte("persistent SKVM file"),
	); err != nil {
		t.Fatal(err)
	}
	if err := machine.services.Storage.WriteFile(
		shared.NamespaceTemporary,
		"/discard.tmp",
		[]byte("temporary"),
	); err != nil {
		t.Fatal(err)
	}
	store, err := machine.services.Storage.CreateRecordStore(
		machine.owner,
		"scores",
	)
	if err != nil {
		t.Fatal(err)
	}
	recordID, err := machine.services.Storage.AddRecord(
		machine.owner,
		store,
		[]byte{9, 8, 7, 6},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateReady ||
		machine.services.Clock.Monotonic() != 0 {
		t.Fatalf(
			"reset state clock=%s lifecycle=%s",
			machine.services.Clock.Monotonic(),
			machine.State(),
		)
	}
	persisted, err := machine.services.Storage.ReadFile(
		shared.NamespacePrivate,
		"/save.dat",
	)
	if err != nil || string(persisted) != "persistent SKVM file" {
		t.Fatalf("reset SKVM persistent file = %q, %v", persisted, err)
	}
	if _, err := machine.services.Storage.ReadFile(
		shared.NamespaceTemporary,
		"/discard.tmp",
	); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("reset SKVM temporary file error = %v", err)
	}
	persistedStore, err := machine.services.Storage.OpenRecordStore(
		machine.owner,
		"scores",
	)
	if err != nil {
		t.Fatal(err)
	}
	persistedRecord, err := machine.services.Storage.Record(
		machine.owner,
		persistedStore,
		recordID,
	)
	if err != nil || !bytes.Equal(persistedRecord, []byte{9, 8, 7, 6}) {
		t.Fatalf(
			"reset SKVM persistent record = %v, %v",
			persistedRecord,
			err,
		)
	}
}

func syntheticSKVMPackage(t *testing.T) []byte {
	t.Helper()
	jar := syntheticSKVMZIP(t, map[string][]byte{
		"Game.class": syntheticSKVMLifecycleClass(t),
		"data.bin":   {1, 2, 3},
	})
	return syntheticSKVMZIP(t, map[string][]byte{
		"game.msd": []byte(
			"MIDlet-Name: Synthetic\n" +
				"MIDlet-Version: 1.0\n" +
				"MIDlet-Vendor: ARAM\n" +
				"MicroEdition-Profile: SKTP-1.0\n" +
				"MIDlet-1: Synthetic,,Game\n",
		),
		"game.jar": jar,
		"game.mod": {1},
		"game.wmr": {2},
	})
}

func syntheticSKVMZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, data := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func syntheticSKVMLifecycleClass(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	u2 := func(value uint16) {
		t.Helper()
		if err := binary.Write(&output, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	u4 := func(value uint32) {
		t.Helper()
		if err := binary.Write(&output, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	utf := func(value string) {
		t.Helper()
		output.WriteByte(1)
		u2(uint16(len(value)))
		output.WriteString(value)
	}
	class := func(name uint16) {
		output.WriteByte(7)
		u2(name)
	}
	method := func(name, descriptor uint16, maxLocals uint16) {
		u2(0x0001)
		u2(name)
		u2(descriptor)
		u2(1)
		u2(7)
		u4(13)
		u2(0)
		u2(maxLocals)
		u4(1)
		output.WriteByte(0xb1)
		u2(0)
		u2(0)
	}

	u4(0xcafebabe)
	u2(3)
	u2(45)
	u2(12)
	utf("Game")             // 1
	class(1)                // 2
	utf("java/lang/Object") // 3
	class(3)                // 4
	utf("<init>")           // 5
	utf("()V")              // 6
	utf("Code")             // 7
	utf("startApp")         // 8
	utf("pauseApp")         // 9
	utf("destroyApp")       // 10
	utf("(Z)V")             // 11
	u2(0x0001)
	u2(2)
	u2(4)
	u2(0)
	u2(0)
	u2(4)
	method(5, 6, 1)
	method(8, 6, 1)
	method(9, 6, 1)
	method(10, 11, 2)
	u2(0)
	return output.Bytes()
}
