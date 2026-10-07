package runtime

import (
	"reflect"
	"testing"
	"time"
)

func TestStoppedClipGainPreservesOtherQueuedAudio(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		media, bus, bgm := newRampMedia(t)
		media.SetStoppedLoopPreservation(preserve)
		check(t, media.Play(1, bgm, -1))
		fx := bgm
		if preserve {
			check(t, media.Stop(1, bgm))
		} else {
			var err error
			fx, err = media.CreateClip(1, "audio/wav", 0)
			check(t, err)
		}
		check(t, media.Advance(0, 250*time.Microsecond, bus))
		revision := media.OutputRevision()
		check(t, media.SetClipGain(1, fx, 50, false, 25))
		if media.OutputRevision() != revision {
			t.Fatalf("stopped gain changed revision with preservation=%t", preserve)
		}
		if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{10, 20}) {
			t.Fatalf("stopped gain cleared BGM with preservation=%t: %v", preserve, got)
		}
	}
}

func TestPlayingClipGainInvalidatesQueuedAudio(t *testing.T) {
	media, bus, clip := newRampMedia(t)
	check(t, media.Play(1, clip, -1))
	check(t, media.Advance(0, 250*time.Microsecond, bus))
	revision := media.OutputRevision()
	check(t, media.SetClipGain(1, clip, 100, true, 0))
	if media.OutputRevision() == revision || len(media.Drain().PCM16) != 0 {
		t.Fatal("live mute retained old output")
	}
	check(t, media.SetClipGain(1, clip, 50, false, 0))
	check(t, media.Advance(250*time.Microsecond, 375*time.Microsecond, bus))
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{14}) {
		t.Fatalf("new gain output = %v, want [14]", got)
	}
}

func TestInaudibleClipGainPreservesOtherQueuedAudio(t *testing.T) {
	for _, test := range []struct {
		name      string
		waiting   bool
		volume    uint8
		muted     bool
		newVolume uint8
	}{{"waiting-for-data", true, 100, false, 50}, {"muted", false, 100, true, 50}, {"zero-volume", false, 0, false, 0}} {
		t.Run(test.name, func(t *testing.T) {
			media, bus, bgm := newRampMedia(t)
			fx, err := media.CreateClip(1, "audio/wav", 0)
			check(t, err)
			source := pcmWave(8_000, 1, []int16{1000})
			if test.waiting {
				source = []byte("MMMD")
			}
			_, err = media.Append(1, fx, source)
			check(t, err)
			check(t, media.SetClipGain(1, fx, test.volume, test.muted, 0))
			check(t, media.Play(1, fx, -1))
			check(t, media.Play(1, bgm, -1))
			check(t, media.Advance(0, 250*time.Microsecond, bus))
			revision := media.OutputRevision()
			check(t, media.SetClipGain(1, fx, test.newVolume, test.muted, 25))
			if media.OutputRevision() != revision || !reflect.DeepEqual(media.Drain().PCM16, []int16{10, 20}) {
				t.Fatal("inaudible effect gain invalidated queued BGM")
			}
		})
	}
}
