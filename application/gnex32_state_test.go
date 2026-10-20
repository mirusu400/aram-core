package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/gnex32"
)

func TestGNEX32MachineStateRoundTripAcrossInstances(t *testing.T) {
	const sourceSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first := newApplicationGNEX32PersistenceMachine(t)
	first.source.SHA256 = sourceSHA
	wantNV := [gnex32.UserNVWords]uint32{1, 2, 3, 0x80000000, 0xffffffff}
	first.runtime.RestoreUserNV(wantNV)
	first.state = machinecore.StatePaused
	first.audioGeneration = 41
	first.audioGenerationPending = false
	first.audioEpochGuestNS = 123
	first.audioCursorSample = 456
	first.audioCursorValid = true
	first.mediaOutputRevision = first.runtime.AudioOutputRevision()

	var saved bytes.Buffer
	if err := first.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	first.runtime.RestoreUserNV([gnex32.UserNVWords]uint32{99})
	first.state = machinecore.StateStopped
	if err := first.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	if first.state != machinecore.StatePaused || first.runtime.CaptureUserNV() != wantNV {
		t.Fatalf("same-instance restore = state %s NV %#v", first.state, first.runtime.CaptureUserNV())
	}
	if first.audioGeneration != 42 || !first.audioGenerationPending || first.audioCursorValid {
		t.Fatalf("post-load audio = generation %d pending %v cursor-valid %v",
			first.audioGeneration, first.audioGenerationPending, first.audioCursorValid)
	}
	if first.audioEpochGuestNS != int64(first.runtime.AudioTime()) ||
		first.mediaOutputRevision != first.runtime.AudioOutputRevision() {
		t.Fatal("post-load audio timeline does not match restored runtime")
	}

	second := newApplicationGNEX32PersistenceMachine(t)
	second.source.SHA256 = sourceSHA
	if err := second.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	if second.state != machinecore.StatePaused || second.runtime.CaptureUserNV() != wantNV {
		t.Fatalf("cross-instance restore = state %s NV %#v", second.state, second.runtime.CaptureUserNV())
	}
}

func TestGNEX32MachineStateRejectsInvalidInputAtomically(t *testing.T) {
	machine := newApplicationGNEX32PersistenceMachine(t)
	machine.source.SHA256 = strings.Repeat("b", sha256.Size*2)
	machine.runtime.RestoreUserNV([gnex32.UserNVWords]uint32{7, 8, 9})
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	valid := saved.Bytes()
	corrupt := bytes.Clone(valid)
	corrupt[len(gnex32MachineStateMagic)+6] ^= 0x40
	wrongVersion := bytes.Clone(valid)
	binary.LittleEndian.PutUint32(wrongVersion[len(gnex32MachineStateMagic):], gnex32MachineStateVersion+1)
	checksum := sha256.Sum256(wrongVersion[:len(wrongVersion)-sha256.Size])
	copy(wrongVersion[len(wrongVersion)-sha256.Size:], checksum[:])

	for name, state := range map[string][]byte{
		"truncated":     valid[:len(valid)-1],
		"checksum":      corrupt,
		"wrong version": wrongVersion,
		"trailing":      append(bytes.Clone(valid), 0),
	} {
		t.Run(name, func(t *testing.T) {
			before, err := machine.runtime.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			beforeState, beforeGeneration := machine.state, machine.audioGeneration
			if err := machine.LoadState(bytes.NewReader(state)); err == nil {
				t.Fatal("accepted invalid machine state")
			}
			after, err := machine.runtime.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) || machine.state != beforeState || machine.audioGeneration != beforeGeneration {
				t.Fatal("failed load mutated machine state")
			}
		})
	}

	other := newApplicationGNEX32PersistenceMachine(t)
	other.source.SHA256 = strings.Repeat("c", sha256.Size*2)
	if err := other.LoadState(bytes.NewReader(valid)); err == nil {
		t.Fatal("accepted state from another source")
	}
}

func TestGNEX32MachineStateHonorsLifecycleAndClosedBoundaries(t *testing.T) {
	machine := newApplicationGNEX32PersistenceMachine(t)
	machine.source.SHA256 = strings.Repeat("d", sha256.Size*2)
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.SaveState(new(bytes.Buffer)); err == nil {
		t.Fatal("saved a running GNEX32 machine")
	}
	if err := machine.LoadState(bytes.NewReader(nil)); err == nil {
		t.Fatal("loaded a running GNEX32 machine")
	}
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	if err := machine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := machine.SaveState(new(bytes.Buffer)); err == nil {
		t.Fatal("saved a closed GNEX32 machine")
	}
	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err == nil {
		t.Fatal("loaded a closed GNEX32 machine")
	}
}

func TestGNEX32MachineStateCrossProcess(t *testing.T) {
	const (
		helperEnv = "ARAM_GNEX32_STATE_HELPER"
		sourceSHA = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	)
	path := filepath.Join(t.TempDir(), "gnex32.state")
	if os.Getenv(helperEnv) == "1" {
		data, err := os.ReadFile(os.Getenv("ARAM_GNEX32_STATE_PATH"))
		if err != nil {
			t.Fatal(err)
		}
		machine := newApplicationGNEX32PersistenceMachine(t)
		machine.source.SHA256 = sourceSHA
		if err := machine.LoadState(bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		want := [gnex32.UserNVWords]uint32{0x10203040, 55, 0xffffffff}
		if got := machine.runtime.CaptureUserNV(); got != want {
			t.Fatalf("cross-process UserNV = %#v, want %#v", got, want)
		}
		return
	}

	machine := newApplicationGNEX32PersistenceMachine(t)
	machine.source.SHA256 = sourceSHA
	machine.runtime.RestoreUserNV([gnex32.UserNVWords]uint32{0x10203040, 55, 0xffffffff})
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, saved.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestGNEX32MachineStateCrossProcess$")
	command.Env = append(os.Environ(), helperEnv+"=1", "ARAM_GNEX32_STATE_PATH="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("cross-process restore: %v\n%s", err, output)
	}
}
