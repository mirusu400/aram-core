package runtime

import (
	"reflect"
	"testing"
	"time"
)

// Completing and releasing a one-shot clip must not stop or rewind another
// clip's loop. An explicit stop of the loop must still silence its voice.
func TestMediaEffectCompletionAndReleasePreserveLoop(t *testing.T) {
	for _, mix := range []bool{false, true} {
		name := "mix_off"
		if mix {
			name = "mix_on"
		}
		t.Run(name, func(t *testing.T) {
			media, bus, bgm := newRampMedia(t)
			media.SetAudioMixMode(mix)
			fx, err := media.CreateClip(1, "audio/wav", 0)
			check(t, err)
			_, err = media.Append(1, fx, pcmWave(8000, 1, []int16{1000}))
			check(t, err)
			check(t, media.Play(1, bgm, -1))
			step := 125 * time.Microsecond
			advance := func(start, end time.Duration, want []int16) {
				t.Helper()
				check(t, media.Advance(start, end, bus))
				if got := media.Drain().PCM16; !reflect.DeepEqual(got, want) {
					t.Fatalf("PCM at %v..%v = %v, want %v", start, end, got, want)
				}
			}
			advance(0, step, []int16{10})
			check(t, media.Play(1, fx, 1))
			advance(step, 2*step, []int16{1020})
			effect, err := media.Info(1, fx)
			check(t, err)
			if effect.State != ClipStopped {
				t.Fatalf("effect state = %v", effect.State)
			}
			advance(2*step, 3*step, []int16{30})
			check(t, media.DestroyClip(1, fx, bus))
			advance(3*step, 6*step, []int16{40, 10, 20})
			loop, err := media.Info(1, bgm)
			check(t, err)
			if loop.State != ClipPlaying || loop.RemainingPlays != -1 {
				t.Fatalf("effect changed loop state: %+v", loop)
			}
			// An explicit stop is different from another clip completing. Never
			// preserve a detached background voice after the owner asks it to stop.
			check(t, media.Stop(1, bgm))
			advance(6*step, 7*step, nil)
		})
	}
}
