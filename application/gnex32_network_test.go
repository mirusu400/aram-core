package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32MachineExposesAsynchronousNetworkContract(t *testing.T) {
	machine := newApplicationGNEX32NetworkMachine(t)
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	requests := machine.NetworkRequests()
	if len(requests) != 1 {
		t.Fatalf("connect requests = %+v", requests)
	}
	connect := requests[0]
	if connect.Kind != machinecore.NetworkRequestConnect || connect.Host != "127.0.0.1" ||
		connect.Port != 17002 || connect.MaxReceiveBytes != 1024 {
		t.Fatalf("connect request = %+v", connect)
	}
	if err := machine.CompleteNetworkRequest(connect.Sequence, true); err != nil {
		t.Fatal(err)
	}
	if requests := machine.NetworkRequests(); len(requests) != 0 {
		t.Fatalf("completed requests = %+v", requests)
	}
	if err := machine.ReceiveNetworkData(connect.Connection, []byte("packet")); err != nil {
		t.Fatal(err)
	}
	if err := machine.DisconnectNetwork(connect.Connection); err != nil {
		t.Fatal(err)
	}
	if err := machine.DisconnectNetwork(connect.Connection); !errors.Is(err, gnex32.ErrNetworkRequestUnavailable) {
		t.Fatalf("stale disconnect error = %v", err)
	}
	if machine.State() != machinecore.StateRunning {
		t.Fatalf("provider rejection faulted machine: %s", machine.State())
	}
}

func TestGNEX32MachineStateRejectsLiveNetworkBeforeWriting(t *testing.T) {
	machine := newApplicationGNEX32NetworkMachine(t)
	machine.source.SHA256 = strings.Repeat("a", 64)
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := machine.NetworkRequests()[0]
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	var rejected bytes.Buffer
	if err := machine.SaveState(&rejected); !errors.Is(err, ErrGNEX32NetworkStateActive) {
		t.Fatalf("live network save error = %v", err)
	}
	if rejected.Len() != 0 {
		t.Fatalf("rejected save wrote %d bytes", rejected.Len())
	}
	if err := machine.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := machine.CompleteNetworkRequest(request.Sequence, false); err != nil {
		t.Fatal(err)
	}
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatalf("offline save failed: %v", err)
	}
}

func TestGNEX32MachineStateRejectsRestoredLiveNetworkAtomically(t *testing.T) {
	const sourceSHA = "abababababababababababababababababababababababababababababababab"
	source := newApplicationGNEX32NetworkMachine(t)
	source.source.SHA256 = sourceSHA
	if err := source.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := source.Pause(); err != nil {
		t.Fatal(err)
	}
	runtimeState, err := source.runtime.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	encoded := encodeGNEX32NetworkMachineState(t, source, runtimeState)

	target := newApplicationGNEX32NetworkMachine(t)
	target.source.SHA256 = sourceSHA
	before, err := target.runtime.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := target.LoadState(bytes.NewReader(encoded)); !errors.Is(err, ErrGNEX32NetworkStateActive) {
		t.Fatalf("live network load error = %v", err)
	}
	after, err := target.runtime.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) || target.State() != machinecore.StateReady {
		t.Fatal("rejected live network state mutated target")
	}
}

func encodeGNEX32NetworkMachineState(t *testing.T, machine *gnex32Machine, runtimeState []byte) []byte {
	t.Helper()
	digest, err := hex.DecodeString(machine.source.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	data.WriteString(gnex32MachineStateMagic)
	writeGNEX32StateU32(&data, gnex32MachineStateVersion)
	data.WriteByte(byte(machine.state))
	writeGNEX32StateBool(&data, machine.audioGenerationPending)
	writeGNEX32StateBool(&data, machine.audioCursorValid)
	data.WriteByte(0)
	data.Write(digest)
	writeGNEX32StateU64(&data, machine.audioGeneration)
	writeGNEX32StateU64(&data, uint64(machine.audioEpochGuestNS))
	writeGNEX32StateU64(&data, machine.audioCursorSample)
	writeGNEX32StateU64(&data, machine.mediaOutputRevision)
	writeGNEX32StateU64(&data, uint64(len(runtimeState)))
	data.Write(runtimeState)
	checksum := sha256.Sum256(data.Bytes())
	data.Write(checksum[:])
	return data.Bytes()
}

func newApplicationGNEX32NetworkMachine(t *testing.T) *gnex32Machine {
	t.Helper()
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160,
		Code: []byte{
			0x05, 0x00, 0x00, 0x00, // push media index 0
			0x05, 0x00, 0x6a, 0x42, // push port 17002
			0x92, 0x00, 0x01, 0x01, // NetConnect
			0x86, 0x00,
		},
		Symbols: symbols,
		Media:   []gnex.GNEX32Media{{Flags: 0, Data: []byte("127.0.0.1\x00")}},
	}
	runtime, err := gnex32.NewRuntime(image, gnex32VirusProfile)
	if err != nil {
		t.Fatal(err)
	}
	machine := &gnex32Machine{
		state: machinecore.StateReady, image: image, profile: gnex32VirusProfile,
		runtime: runtime, budget: 32, audioGeneration: 1,
		mediaOutputRevision: runtime.AudioOutputRevision(),
	}
	t.Cleanup(func() { _ = machine.Close() })
	return machine
}

var _ machinecore.NetworkTransportMachine = (*gnex32Machine)(nil)
