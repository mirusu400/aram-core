package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"reflect"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32SaveDataSurvivesRelaunchAndReset(t *testing.T) {
	first := newApplicationGNEX32PersistenceMachine(t)
	want := [gnex32.UserNVWords]uint32{1, 2, 0x80000000, 0xffffffff, 99}
	first.runtime.RestoreUserNV(want)
	saved, err := first.ExportSaveData()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) == 0 {
		t.Fatal("ExportSaveData returned an empty snapshot")
	}

	second := newApplicationGNEX32PersistenceMachine(t)
	if err := second.ImportSaveData(saved); err != nil {
		t.Fatal(err)
	}
	if got := second.runtime.CaptureUserNV(); got != want {
		t.Fatalf("relaunched UserNV = %#v, want %#v", got, want)
	}
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if second.runtime.AudioTime() == 0 {
		t.Fatal("test did not establish volatile guest time before reset")
	}
	if err := second.Pause(); err != nil {
		t.Fatal(err)
	}
	previousRuntime := second.runtime
	if err := second.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if second.runtime == previousRuntime {
		t.Fatal("Reset retained the volatile runtime")
	}
	if got := second.runtime.CaptureUserNV(); got != want {
		t.Fatalf("reset UserNV = %#v, want %#v", got, want)
	}
	if second.state != machinecore.StateReady {
		t.Fatalf("reset state = %s, want ready", second.state)
	}
	if second.runtime.AudioTime() != 0 || second.runtime.PresentCount() != 0 {
		t.Fatalf("reset retained volatile time/presents: %s/%d",
			second.runtime.AudioTime(), second.runtime.PresentCount())
	}
}

func TestGNEX32SaveDataRejectsInvalidImportAtomically(t *testing.T) {
	machine := newApplicationGNEX32PersistenceMachine(t)
	want := [gnex32.UserNVWords]uint32{7, 8, 9}
	machine.runtime.RestoreUserNV(want)

	wrongMagic := encodeGNEX32SaveDataTestEnvelope(t, gnex32SaveDataEnvelope{
		Magic: "OTHER", Version: gnex32SaveDataVersion,
	})
	wrongVersion := encodeGNEX32SaveDataTestEnvelope(t, gnex32SaveDataEnvelope{
		Magic: gnex32SaveDataMagic, Version: gnex32SaveDataVersion + 1,
	})
	valid := encodeGNEX32SaveDataTestEnvelope(t, gnex32SaveDataEnvelope{
		Magic: gnex32SaveDataMagic, Version: gnex32SaveDataVersion,
	})
	for name, data := range map[string][]byte{
		"malformed":     []byte("not a gob"),
		"oversized":     make([]byte, gnex32MaxSaveDataSize+1),
		"wrong magic":   wrongMagic,
		"wrong version": wrongVersion,
		"trailing data": append(valid, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := machine.ImportSaveData(data); err == nil {
				t.Fatal("ImportSaveData accepted invalid data")
			}
			if got := machine.runtime.CaptureUserNV(); got != want {
				t.Fatalf("invalid import changed UserNV to %#v", got)
			}
		})
	}
}

func TestGNEX32SaveDataHonorsLifecycleBoundaries(t *testing.T) {
	machine := newApplicationGNEX32PersistenceMachine(t)
	saved, err := machine.ExportSaveData()
	if err != nil {
		t.Fatal(err)
	}
	machine.state = machinecore.StateRunning
	if _, err := machine.ExportSaveData(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("running export error = %v", err)
	}
	if err := machine.ImportSaveData(saved); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("running import error = %v", err)
	}
	machine.state = machinecore.StateReady
	if err := machine.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.ExportSaveData(); !errors.Is(err, cpu.ErrClosed) {
		t.Fatalf("closed export error = %v", err)
	}
	if err := machine.ImportSaveData(saved); !errors.Is(err, cpu.ErrClosed) {
		t.Fatalf("closed import error = %v", err)
	}
}

func TestGNEX32ArchiveUserNVRecognizesObservedNames(t *testing.T) {
	want := [gnex32.UserNVWords]uint32{0x01020304, 7, 0x80000000, 0xffffffff}
	raw := encodeGNEX32ArchiveUserNV(want)
	for _, name := range []string{"save.dat", "user.dat", "nested/UserNV.sys"} {
		t.Run(name, func(t *testing.T) {
			got, ok, err := gnex32ArchiveUserNV(gnex.Package{Files: map[string][]byte{
				name:           raw,
				"registry.dat": bytes.Repeat([]byte{1}, 121),
			}})
			if err != nil {
				t.Fatal(err)
			}
			if !ok || got != want {
				t.Fatalf("archive UserNV = %#v, %v; want %#v, true", got, ok, want)
			}
		})
	}
}

func TestGNEX32ArchiveUserNVValidatesAndSelectsExplicitMember(t *testing.T) {
	low := [gnex32.UserNVWords]uint32{1}
	high := [gnex32.UserNVWords]uint32{2}
	got, ok, err := gnex32ArchiveUserNV(gnex.Package{Files: map[string][]byte{
		"save.dat":   encodeGNEX32ArchiveUserNV(low),
		"UserNV.sys": encodeGNEX32ArchiveUserNV(high),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != high {
		t.Fatalf("selected UserNV = %#v, %v; want explicit %#v", got, ok, high)
	}

	if _, _, err := gnex32ArchiveUserNV(gnex.Package{Files: map[string][]byte{
		"UserNV.sys": make([]byte, gnex32UserNVBytes-1),
	}}); err == nil {
		t.Fatal("accepted short UserNV.sys")
	}
	if _, _, err := gnex32ArchiveUserNV(gnex.Package{Files: map[string][]byte{
		"one/UserNV.sys": encodeGNEX32ArchiveUserNV(low),
		"two/UserNV.sys": encodeGNEX32ArchiveUserNV(high),
	}}); err == nil {
		t.Fatal("accepted ambiguous UserNV.sys members")
	}
	if got, ok, err := gnex32ArchiveUserNV(gnex.Package{Files: map[string][]byte{
		"registry.dat": bytes.Repeat([]byte{1}, 121),
	}}); err != nil || ok || !reflect.DeepEqual(got, [gnex32.UserNVWords]uint32{}) {
		t.Fatalf("non-save .dat = %#v, %v, %v", got, ok, err)
	}
}

func newApplicationGNEX32PersistenceMachine(t *testing.T) *gnex32Machine {
	t.Helper()
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	image := gnex.GNEX32Image{
		Header:  gnex.Header{BodyOffset: 160},
		Entry:   160,
		Code:    []byte{0x86, 0},
		Symbols: symbols,
	}
	runtime, err := gnex32.NewRuntime(image, gnex32VirusProfile)
	if err != nil {
		t.Fatal(err)
	}
	machine := &gnex32Machine{
		state:               machinecore.StateReady,
		image:               image,
		profile:             gnex32VirusProfile,
		runtime:             runtime,
		budget:              1,
		audioGeneration:     1,
		mediaOutputRevision: runtime.AudioOutputRevision(),
	}
	t.Cleanup(func() { _ = machine.Close() })
	return machine
}

func encodeGNEX32SaveDataTestEnvelope(t *testing.T, envelope gnex32SaveDataEnvelope) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := gob.NewEncoder(&output).Encode(envelope); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func encodeGNEX32ArchiveUserNV(values [gnex32.UserNVWords]uint32) []byte {
	data := make([]byte, gnex32UserNVBytes)
	for index, value := range values {
		binary.LittleEndian.PutUint32(data[index*4:], value)
	}
	return data
}
