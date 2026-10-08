package runtime

import (
	"reflect"
	"testing"
	"time"
)

func TestMediaMonoMixPreservesSamplesAtFullGain(t *testing.T) {
	for _, channels := range []uint16{1, 2} {
		t.Run(map[uint16]string{1: "mono_source", 2: "stereo_source"}[channels], func(t *testing.T) {
			want := []int16{1, 3, 5, -1, -3, 32767, -32768}
			source := want
			if channels == 2 {
				source = nil
				for _, sample := range want {
					source = append(source, sample, sample)
				}
			}
			limits := DefaultMediaLimits()
			limits.OutputSampleRate = 8_000
			limits.OutputChannels = 1
			media, err := NewMedia(NewRegistry(8), limits)
			check(t, err)
			clip, err := media.CreateClip(1, "audio/wav", 0)
			check(t, err)
			_, err = media.Append(1, clip, pcmWave(8_000, channels, source))
			check(t, err)
			check(t, media.Play(1, clip, 1))
			check(t, media.Advance(0, time.Duration(len(want))*125*time.Microsecond, NewEventBus(8, 8)))
			if got := media.Drain().PCM16; !reflect.DeepEqual(got, want) {
				t.Fatalf("full-gain mono PCM = %v, want %v", got, want)
			}
		})
	}
}
