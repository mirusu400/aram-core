package brewrt

import (
	"context"
	"testing"
	"time"
)

func TestBREWDeferredAudioDrainKeepsSoundAnchorsAndSilentGap(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	samples := make([]int16, 800)
	for index := range samples {
		samples[index] = int16(200 + index%31)
	}
	if err := runtime.replaceSoundPlayerSource("audio/wav", brewTestWave(samples)); err != nil {
		t.Fatal(err)
	}
	revision := runtime.AudioOutputRevision()
	for _, step := range []struct {
		play  bool
		delta time.Duration
	}{{true, 100 * time.Millisecond}, {false, time.Second}, {true, 100 * time.Millisecond}, {false, time.Second}} {
		if step.play {
			if err := runtime.media.Play(brewMediaOwner, runtime.soundPlayer.clip, 1); err != nil {
				t.Fatal(err)
			}
		}
		if err := runtime.RunCallbacks(context.Background(), step.delta); err != nil {
			t.Fatal(err)
		}
	}
	for _, wantStart := range []time.Duration{0, 1100 * time.Millisecond} {
		audio, start, outputRevision := runtime.DrainTimedAudio()
		if start != wantStart || len(audio.PCM16) != 4410 || outputRevision != revision {
			t.Fatalf("deferred BREW audio: start=%v samples=%d revision=%d, want %v/4410/%d", start, len(audio.PCM16), outputRevision, wantStart, revision)
		}
	}
	if audio, start, _ := runtime.DrainTimedAudio(); len(audio.PCM16) != 0 || start != 2200*time.Millisecond {
		t.Fatalf("empty drain: start=%v samples=%d", start, len(audio.PCM16))
	}
	if audio, _, _ := runtime.DrainAudio(); len(audio.PCM16) != 0 {
		t.Fatal("legacy drain duplicated timed BREW audio")
	}
}
