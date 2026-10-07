package runtime

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMediaTimedDrainKeepsFractionalAdvancesContinuous(t *testing.T) {
	for _, channels := range []uint8{1, 2} {
		limits := DefaultMediaLimits()
		limits.OutputChannels = channels
		media, err := NewMedia(NewRegistry(4), limits)
		check(t, err)
		bus := NewEventBus(4, 32)
		clip, err := media.CreateClip(1, "audio/wav", 0)
		check(t, err)
		_, err = media.Append(1, clip, pcmWave(8_000, 1, []int16{10, 20, 30, 40}))
		check(t, err)
		check(t, media.Play(1, clip, -1))
		check(t, media.Advance(0, time.Millisecond, bus))
		for step := range 200 {
			start := time.Millisecond + time.Duration(step)*time.Microsecond
			check(t, media.Advance(start, start+time.Microsecond, bus))
		}
		audio, start := media.DrainTimed()
		if start != 0 || audio.Channels != int(channels) || len(audio.PCM16) != 52*int(channels) {
			t.Fatalf("fractional mono/stereo output: start=%v channels=%d samples=%d", start, audio.Channels, len(audio.PCM16))
		}
		if extra, _ := media.DrainTimed(); len(extra.PCM16) != 0 {
			t.Fatal("fractional advances split an uninterrupted stream")
		}
		if len(media.Drain().PCM16) != 0 {
			t.Fatal("legacy drain duplicated timed output")
		}
	}
}

func TestMediaTimedRetentionKeepsTailAnchorAndBoundsFragmentCount(t *testing.T) {
	for _, channels := range []uint8{1, 2} {
		limits := DefaultMediaLimits()
		limits.OutputSampleRate = 8_000
		limits.OutputChannels = channels
		limits.MaxQueuedSamples = uint32(4*channels) + 1
		media, err := NewMedia(NewRegistry(4), limits)
		check(t, err)
		bus := NewEventBus(4, 32)
		clip, err := media.CreateClip(1, "audio/wav", 0)
		check(t, err)
		_, err = media.Append(1, clip, pcmWave(8_000, 1, []int16{10}))
		check(t, err)
		step := 125 * time.Microsecond
		for index := range 200 {
			start := time.Duration(index) * 2 * step
			check(t, media.Play(1, clip, 1))
			check(t, media.Advance(start, start+step, bus))
			_, _ = bus.PopReady(start + step)
			check(t, media.Advance(start+step, start+2*step, bus))
			if len(media.queuedAudioSpans) > int(media.retainedFrames()) {
				t.Fatal("audio metadata exceeded PCM retention")
			}
		}
		frames := int(media.retainedFrames())
		for index := range frames {
			audio, start := media.DrainTimed()
			wantStart := time.Duration(200-frames+index) * 2 * step
			if len(audio.PCM16) != int(channels) || start != wantStart {
				t.Fatalf("retained fragment %d: start=%v samples=%d, want %v/%d", index, start, len(audio.PCM16), wantStart, channels)
			}
		}
		check(t, media.Play(1, clip, -1))
		check(t, media.Advance(time.Second, time.Second+time.Millisecond, bus))
		audio, start := media.DrainTimed()
		wantStart := time.Second + durationForFrame(uint64(8-frames), 8_000)
		if len(audio.PCM16) != frames*int(channels) || start != wantStart {
			t.Fatalf("retained tail: start=%v samples=%d, want %v/%d", start, len(audio.PCM16), wantStart, frames*int(channels))
		}
	}
}

func TestMediaTimedPublicationRollsBackFailedAdvances(t *testing.T) {
	t.Run("media", func(t *testing.T) {
		media, bus, clip := newRampMedia(t)
		check(t, media.Play(1, clip, 1))
		check(t, media.Advance(time.Second, time.Second+125*time.Microsecond, bus))
		bus = NewEventBus(1, 32)
		_, err := bus.Enqueue(Event{Kind: EventApplication})
		check(t, err)
		revision := media.OutputRevision()
		if err := media.Advance(time.Second+125*time.Microsecond, time.Second+500*time.Microsecond, bus); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("failed completion = %v", err)
		}
		audio, start := media.DrainTimed()
		if start != time.Second || !reflect.DeepEqual(audio.PCM16, []int16{10}) || media.OutputRevision() != revision {
			t.Fatalf("failed advance changed publication: start=%v samples=%v revision=%d", start, audio.PCM16, media.OutputRevision())
		}
	})
	t.Run("services", func(t *testing.T) {
		config := DefaultConfig()
		config.Limits.MaxEvents = 1
		services, err := NewServices(config)
		check(t, err)
		clip, err := services.Media.CreateClip(1, "audio/wav", 0)
		check(t, err)
		_, err = services.Media.Append(1, clip, pcmWave(8_000, 1, []int16{10, 20}))
		check(t, err)
		check(t, services.Media.Play(1, clip, -1))
		check(t, services.Advance(1, time.Millisecond))
		timer, err := services.Timers.Define(1, "audio-rollback")
		check(t, err)
		check(t, services.Timers.Set(timer, 1, time.Millisecond, 0, 0))
		_, err = services.Events.Enqueue(Event{Kind: EventApplication})
		check(t, err)
		if err := services.Advance(1, time.Millisecond); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("failed timer tick = %v", err)
		}
		audio, start := services.Media.DrainTimed()
		if start != 0 || len(audio.PCM16) != 44 || services.Clock.Monotonic() != time.Millisecond {
			t.Fatalf("failed service tick changed publication: start=%v samples=%d clock=%v", start, len(audio.PCM16), services.Clock.Monotonic())
		}
	})
}

func TestMediaTimedRestoreKeepsLegacySnapshotPCMAndStartsNewPresentation(t *testing.T) {
	media, bus, clip := newRampMedia(t)
	check(t, media.Play(1, clip, -1))
	check(t, media.Advance(0, 250*time.Microsecond, bus))
	state := media.Snapshot()
	check(t, media.Restore(state))
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, state.QueuedPCM16) {
		t.Fatal("restore changed the legacy snapshot PCM contract")
	}
	check(t, media.Restore(state))
	check(t, media.Advance(time.Second, time.Second+250*time.Microsecond, bus))
	audio, start := media.DrainTimed()
	if start != time.Second || !reflect.DeepEqual(audio.PCM16, []int16{30, 40}) {
		t.Fatalf("restored presentation reused an unknown anchor: start=%v samples=%v", start, audio.PCM16)
	}
	// A restored output format cannot reuse the previous format's anchors.
	state.QueuedPCM16 = nil
	state.Limits.OutputSampleRate = 44_100
	state.Limits.OutputChannels = 2
	check(t, media.Restore(state))
	check(t, media.Advance(2*time.Second, 2*time.Second+time.Millisecond, bus))
	audio, start = media.DrainTimed()
	if start != 2*time.Second || audio.SampleRate != 44_100 || audio.Channels != 2 || len(audio.PCM16) != 88 {
		t.Fatalf("restored output format: start=%v rate=%d channels=%d samples=%d", start, audio.SampleRate, audio.Channels, len(audio.PCM16))
	}
}
