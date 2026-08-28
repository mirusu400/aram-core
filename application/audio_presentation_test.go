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

// TestPublishedAudioStaysContiguousAcrossSubFrameAdvances pins the property the
// host depends on: consecutive chunks join without a gap or an overlap.
//
// The mixer sizes an advance with a remainder accumulator, while the chunk's
// start index used to be an independent truncated guest-time conversion. The
// two answers differ by a sample whenever an advance boundary falls between two
// output frames, which happens constantly once a title splits its presentation
// quantum around a timer. A host that reads the stream as a sample timeline then
// resynchronizes tens of times a second on what is only rounding.
func TestPublishedAudioStaysContiguousAcrossSubFrameAdvances(t *testing.T) {
	const rate = 44_100
	machine := &Machine{audioGeneration: 1}
	// Quantum pieces recorded from a real KTF title that parks on timers, and a
	// start offset that is not a whole output frame - which is the normal case,
	// because playback begins whenever the guest asks for it.
	steps := []time.Duration{
		13_666_667, 16_666_667, 10_000_000, 6_666_667, 3_000_000, 10_000_000,
		3_666_667, 7_000_000, 9_666_667, 16_666_667, 10_000_000, 6_666_667,
		4_000_000, 10_000_000, 2_666_667, 7_000_000, 9_666_668, 10_000_000,
		6_666_667, 4_000_000, 12_666_667, 16_666_667,
	}
	var (
		elapsed   = 4*time.Second + 253*time.Millisecond + 85
		remainder uint64
		next      uint64
	)
	for round := 0; round < 8; round++ {
		for _, step := range steps {
			numerator := remainder + uint64(step)*rate
			frames := numerator / uint64(time.Second)
			remainder = numerator % uint64(time.Second)
			machine.publishAudioBuffer(shared.AudioBuffer{
				SampleRate: rate,
				Channels:   2,
				PCM16:      make([]int16, frames*2),
			}, elapsed)
			elapsed += step
			chunk := machine.DrainPublishedAudio()
			if len(chunk.PCM16) == 0 {
				t.Fatalf("round %d step %s published nothing", round, step)
			}
			if next == 0 {
				next = chunk.StartSample
			}
			if chunk.StartSample != next {
				t.Fatalf(
					"round %d step %s started at sample %d, want %d",
					round, step, chunk.StartSample, next,
				)
			}
			next = chunk.StartSample + uint64(len(chunk.PCM16)/2)
		}
	}
}

// TestPublishedAudioReportsARealGap keeps the contiguity rule from papering over
// silence the guest actually produced: a pause the host has to honour still
// arrives as a forward jump on the sample timeline.
func TestPublishedAudioReportsARealGap(t *testing.T) {
	machine := &Machine{audioGeneration: 1}
	machine.publishAudioBuffer(shared.AudioBuffer{
		SampleRate: 44_100,
		Channels:   2,
		PCM16:      make([]int16, 441*2),
	}, 0)
	if chunk := machine.DrainPublishedAudio(); chunk.StartSample != 0 {
		t.Fatalf("first chunk started at sample %d, want 0", chunk.StartSample)
	}
	machine.publishAudioBuffer(shared.AudioBuffer{
		SampleRate: 44_100,
		Channels:   2,
		PCM16:      make([]int16, 441*2),
	}, time.Second)
	if chunk := machine.DrainPublishedAudio(); chunk.StartSample != 44_100 {
		t.Fatalf("chunk after a pause started at sample %d, want 44100", chunk.StartSample)
	}
}
