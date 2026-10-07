package core

import "testing"

func TestAudioDiscontinuityMarkerValidation(t *testing.T) {
	marker := AudioChunk{SampleRate: 44_100, Channels: 1, StartGuestNS: 1_000_000, Generation: 2}
	if err := marker.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (AudioChunk{}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (AudioChunk{Generation: 2}).Validate(); err == nil {
		t.Fatal("discontinuity marker accepted no output format")
	}
	marker.StartGuestNS = -1
	if err := marker.Validate(); err == nil {
		t.Fatal("discontinuity marker accepted a negative timestamp")
	}
}
