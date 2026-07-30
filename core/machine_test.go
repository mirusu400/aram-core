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
	if err := (AudioChunk{SampleRate: 44100, Channels: 2, PCM16: make([]int16, 4)}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (AudioChunk{SampleRate: 44100, Channels: 2, PCM16: make([]int16, 3)}).Validate(); err == nil {
		t.Fatal("AudioChunk.Validate accepted an incomplete sample frame")
	}
}
