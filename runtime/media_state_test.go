package runtime

import (
	"reflect"
	"testing"
	"time"
)

func TestServicesStoppedLoopPolicySurvivesStateRoundTrip(t *testing.T) {
	for _, format := range []string{"snapshot", "binary"} {
		for _, scenario := range []struct {
			name  string
			voice bool
			muted bool
		}{
			{name: "before_loop"},
			{name: "preserved_loop", voice: true},
			{name: "muted_loop", voice: true, muted: true},
		} {
			t.Run(format+"/"+scenario.name, func(t *testing.T) {
				config := DefaultConfig()
				config.Limits.Media.OutputSampleRate = 8_000
				config.Limits.Media.OutputChannels = 1
				services, err := NewServices(config)
				check(t, err)
				owner, err := services.Coordinator.Register("stopped-loop", 0)
				check(t, err)
				clip, err := services.Media.CreateClip(owner, "audio/wav", 0)
				check(t, err)
				_, err = services.Media.Append(owner, clip, pcmWave(8_000, 1, []int16{10, 20, 30, 40}))
				check(t, err)
				services.Media.SetStoppedLoopPreservation(true)
				check(t, services.Media.SetClipGain(owner, clip, 100, scenario.muted, 0))
				const step = 125 * time.Microsecond
				if scenario.voice {
					check(t, services.Media.Play(owner, clip, -1))
					check(t, services.Advance(owner, step))
					services.Media.Drain()
					check(t, services.Media.Stop(owner, clip))
				}
				state := services.Snapshot()
				var restored *Services
				if format == "snapshot" {
					restored = services
					check(t, restored.Restore(state))
				} else {
					encoded, err := services.MarshalBinary()
					check(t, err)
					restored, err = NewServices(config)
					check(t, err)
					check(t, restored.UnmarshalBinary(encoded))
				}
				if !reflect.DeepEqual(restored.Snapshot(), state) {
					t.Fatal("stopped-loop state did not round-trip")
				}
				if !scenario.voice {
					check(t, restored.Media.Play(owner, clip, -1))
					check(t, restored.Media.Stop(owner, clip))
				}
				if !restored.Media.MusicVoiceActive() {
					t.Fatal("restore lost the stopped-loop policy or its music voice")
				}
				check(t, restored.Advance(owner, step))
				var want []int16
				if !scenario.muted {
					want = []int16{10}
					if scenario.voice {
						want = []int16{20}
					}
				}
				if got := restored.Media.Drain().PCM16; !reflect.DeepEqual(got, want) {
					t.Fatalf("restored BGM = %v, want %v", got, want)
				}
			})
		}
	}
}

func TestMediaStoppedLoopMutedVoiceSurvivesSnapshot(t *testing.T) {
	media, bus, clip := newRampMedia(t)
	media.SetStoppedLoopPreservation(true)
	check(t, media.SetClipGain(1, clip, 100, true, 0))
	check(t, media.Play(1, clip, -1))
	check(t, media.Stop(1, clip))
	state := media.Snapshot()
	const step = 125 * time.Microsecond
	check(t, media.Advance(0, step, bus))
	if got := media.Drain().PCM16; len(got) != 0 {
		t.Fatalf("muted BGM before restore = %v", got)
	}
	check(t, media.Restore(state))
	check(t, media.Advance(0, step, bus))
	if got := media.Drain().PCM16; len(got) != 0 {
		t.Fatalf("restore made muted BGM audible: %v", got)
	}
}
