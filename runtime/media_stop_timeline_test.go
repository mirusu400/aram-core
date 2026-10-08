package runtime

import (
	"reflect"
	"testing"
	"time"
)

func TestMediaTimelineStopKeepsOtherClipOutput(t *testing.T) {
	for _, release := range []bool{false, true} {
		name := "stop"
		if release {
			name = "release"
		}
		t.Run(name, func(t *testing.T) {
			media, bus, bgm := newRampMedia(t)
			effect, err := media.CreateClip(1, "audio/wav", 0)
			check(t, err)
			_, err = media.Append(1, effect, pcmWave(8000, 1, []int16{1000, 1000, 1000, 1000}))
			check(t, err)
			check(t, media.Play(1, bgm, -1))
			check(t, media.Play(1, effect, 1))
			step := 125 * time.Microsecond
			check(t, media.Advance(time.Second, time.Second+step, bus))
			revision := media.OutputRevision()
			if release {
				check(t, media.StopForRelease(1, effect))
				check(t, media.DestroyClip(1, effect, bus))
			} else {
				check(t, media.StopOnTimeline(1, effect))
			}
			check(t, media.Advance(time.Second+step, time.Second+2*step, bus))
			audio, start := media.DrainTimed()
			if start != time.Second || !reflect.DeepEqual(audio.PCM16, []int16{1010, 20}) || media.OutputRevision() != revision {
				t.Fatalf("clip stop retracted the mix: start=%v samples=%v revision=%d", start, audio.PCM16, media.OutputRevision())
			}
			info, err := media.Info(1, bgm)
			check(t, err)
			if info.State != ClipPlaying || info.Position != 2*step {
				t.Fatalf("background clip changed: %+v", info)
			}
		})
	}
}

func TestMediaTimelineStopHonorsLoopPolicy(t *testing.T) {
	for _, release := range []bool{false, true} {
		media, bus, clip := newRampMedia(t)
		media.SetStoppedLoopPreservation(true)
		check(t, media.Play(1, clip, -1))
		step := 125 * time.Microsecond
		check(t, media.Advance(0, step, bus))
		if release {
			check(t, media.StopForRelease(1, clip))
		} else {
			check(t, media.StopOnTimeline(1, clip))
		}
		if media.MusicVoiceActive() == release {
			t.Fatalf("release=%t background voice=%t", release, media.MusicVoiceActive())
		}
		check(t, media.Advance(step, 2*step, bus))
		want := []int16{10, 20}
		if release {
			want = []int16{10}
		}
		if got := media.Drain().PCM16; !reflect.DeepEqual(got, want) {
			t.Fatalf("release=%t samples=%v want=%v", release, got, want)
		}
	}
}
