package core

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateEmpty, "empty"},
		{StateReady, "ready"},
		{StateRunning, "running"},
		{StatePaused, "paused"},
		{StateStopped, "stopped"},
		{StateFaulted, "faulted"},
		{State(255), "unknown"},
	}
	for _, test := range tests {
		if got := test.state.String(); got != test.want {
			t.Fatalf("State(%d).String() = %q, want %q", test.state, got, test.want)
		}
	}
}

func TestSourceValidate(t *testing.T) {
	source := Source{
		Name:     "synthetic.dat",
		SHA256:   strings.Repeat("ab", 32),
		ReaderAt: bytes.NewReader([]byte{1}),
		Size:     1,
	}
	if err := source.Validate(); err != nil {
		t.Fatal(err)
	}
	source.SHA256 = "invalid"
	if err := source.Validate(); err == nil {
		t.Fatal("Source.Validate accepted an invalid hash")
	}
}

func TestInputAndAudioValidation(t *testing.T) {
	if err := (InputEvent{Control: "up", At: 10 * time.Millisecond}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (InputEvent{Control: "up", At: -1}).Validate(); err == nil {
		t.Fatal("InputEvent.Validate accepted a negative timestamp")
	}
	if err := (InputEvent{Control: strings.Repeat("x", MaxControlNameBytes+1)}).Validate(); err == nil {
		t.Fatal("InputEvent.Validate accepted an oversized control")
	}
	if err := (AudioChunk{
		SampleRate:   44100,
		Channels:     2,
		PCM16:        make([]int16, 4),
		StartGuestNS: int64(10 * time.Millisecond),
		StartSample:  441,
		Generation:   2,
	}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (AudioChunk{SampleRate: 44100, Channels: 2, PCM16: make([]int16, 3)}).Validate(); err == nil {
		t.Fatal("AudioChunk.Validate accepted an incomplete sample frame")
	}
	if err := (AudioChunk{
		SampleRate:   44100,
		Channels:     2,
		PCM16:        make([]int16, 4),
		StartGuestNS: -1,
	}).Validate(); err == nil {
		t.Fatal("AudioChunk.Validate accepted a negative guest timestamp")
	}
}

func TestHostEventValidation(t *testing.T) {
	tests := []struct {
		name    string
		event   HostEvent
		wantErr bool
	}{
		{name: "network", event: HostEvent{Kind: HostEventNetworkReceive, Data: []byte{1, 2}}},
		{name: "serial", event: HostEvent{Kind: HostEventSerialReceive, Words: [4]uint32{1, 2, 3, 4}}},
		{name: "sms", event: HostEvent{Kind: HostEventSMSReceive, Words: [4]uint32{5}}},
		{name: "unknown", event: HostEvent{Kind: "carrier-push"}, wantErr: true},
		{name: "network words", event: HostEvent{Kind: HostEventNetworkReceive, Words: [4]uint32{1}}, wantErr: true},
		{name: "serial bytes", event: HostEvent{Kind: HostEventSerialReceive, Data: []byte{1}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.event.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}
