package application

import (
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestPublishedAudioCarriesGuestTimeline(t *testing.T) {
	machine := &Machine{audioGeneration: 3}
	pcm := []int16{1, -1, 2, -2}
	machine.publishAudioBuffer(shared.AudioBuffer{
		SampleRate: 44_100,
		Channels:   2,
		PCM16:      pcm,
	}, 10*time.Millisecond)

	chunk := machine.DrainPublishedAudio()
	if chunk.Generation != 3 ||
		chunk.StartGuestNS != int64(10*time.Millisecond) ||
		chunk.StartSample != 441 {
		t.Fatalf("published timeline = %+v", chunk)
	}
	if len(chunk.PCM16) != len(pcm) || &chunk.PCM16[0] != &pcm[0] {
		t.Fatal("published PCM ownership was copied instead of transferred")
	}
}

func TestAudioGenerationDropsPreviouslyPublishedPCM(t *testing.T) {
	machine := &Machine{audioGeneration: 4}
	machine.publishAudioBuffer(shared.AudioBuffer{
		SampleRate: 44_100,
		Channels:   2,
		PCM16:      []int16{1, 1, 1, 1},
	}, 10*time.Millisecond)

	machine.beginAudioGeneration(25 * time.Millisecond)
	if stale := machine.DrainPublishedAudio(); len(stale.PCM16) != 0 {
		t.Fatalf("new generation retained stale PCM: %+v", stale)
	}
	machine.publishAudioBuffer(shared.AudioBuffer{
		SampleRate: 44_100,
		Channels:   2,
		PCM16:      []int16{2, 2, 2, 2},
	}, 25*time.Millisecond)
	chunk := machine.DrainPublishedAudio()
	if chunk.Generation != 5 || chunk.StartSample != 0 {
		t.Fatalf("new generation timeline = %+v, want generation 5 sample 0", chunk)
	}
}

func TestDrainPublishedAudioDoesNotWaitForMachineLock(t *testing.T) {
	machine := &Machine{audioGeneration: 1}
	machine.publishAudioBuffer(shared.AudioBuffer{
		SampleRate: 44_100,
		Channels:   2,
		PCM16:      []int16{1, -1, 2, -2},
	}, 0)

	machine.mu.Lock()
	defer machine.mu.Unlock()
	drained := make(chan struct{}, 1)
	go func() {
		_ = machine.DrainPublishedAudio()
		drained <- struct{}{}
	}()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("published audio drain waited for the machine lifecycle lock")
	}
}
