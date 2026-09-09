package runtime

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func newRampMedia(t *testing.T) (*Media, *EventBus, ServiceID) {
	t.Helper()
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	media, err := NewMedia(NewRegistry(32), limits)
	check(t, err)
	clip, err := media.CreateClip(1, "audio/wav", 0)
	check(t, err)
	_, err = media.Append(1, clip, pcmWave(8_000, 1, []int16{10, 20, 30, 40}))
	check(t, err)
	return media, NewEventBus(32, 64), clip
}

func TestMediaMixModeKeepsLoopOwnedByClip(t *testing.T) {
	media, bus, clip := newRampMedia(t)
	media.SetAudioMixMode(true)
	check(t, media.Play(1, clip, -1))
	info, err := media.Info(1, clip)
	check(t, err)
	if info.State != ClipPlaying || info.RemainingPlays != -1 {
		t.Fatalf("loop state = (%v, %d), want playing/-1", info.State, info.RemainingPlays)
	}
	if media.MusicVoiceActive() {
		t.Fatal("mix mode created a detached music voice")
	}

	step := 125 * time.Microsecond
	check(t, media.Advance(0, step, bus))
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{10}) {
		t.Fatalf("first frame = %v", got)
	}
	revision := media.OutputRevision()
	check(t, media.Stop(1, clip))
	if media.OutputRevision() == revision {
		t.Fatal("Stop did not invalidate buffered output")
	}
	check(t, media.Advance(step, 4*step, bus))
	if got := media.Drain().PCM16; len(got) != 0 {
		t.Fatalf("audio survived Stop: %v", got)
	}
	check(t, media.DestroyClip(1, clip, bus))
}

func TestMediaMixModeStillMixesRegisteredClips(t *testing.T) {
	media, bus, bgm := newRampMedia(t)
	media.SetAudioMixMode(true)
	fx, err := media.CreateClip(1, "audio/wav", 0)
	check(t, err)
	_, err = media.Append(1, fx, pcmWave(8_000, 1, []int16{1000}))
	check(t, err)
	check(t, media.Play(1, bgm, -1))
	check(t, media.Play(1, fx, 1))
	check(t, media.Advance(0, 125*time.Microsecond, bus))
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{1010}) {
		t.Fatalf("mixed frame = %v, want [1010]", got)
	}
}

func TestMediaInvalidTransitionsDoNotMutatePlayback(t *testing.T) {
	media, _, clip := newRampMedia(t)
	before, err := media.Info(1, clip)
	check(t, err)
	for name, operation := range map[string]func() error{
		"pause":  func() error { return media.Pause(1, clip) },
		"resume": func() error { return media.Resume(1, clip) },
		"stop":   func() error { return media.Stop(1, clip) },
	} {
		if err := operation(); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("%s error = %v, want ErrInvalidState", name, err)
		}
		after, err := media.Info(1, clip)
		check(t, err)
		if after != before {
			t.Fatalf("%s mutated clip: before=%+v after=%+v", name, before, after)
		}
	}

	check(t, media.Play(1, clip, 1))
	playing, err := media.Info(1, clip)
	check(t, err)
	if err := media.Play(1, clip, -1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("duplicate Play error = %v", err)
	}
	after, err := media.Info(1, clip)
	check(t, err)
	if after != playing {
		t.Fatalf("duplicate Play mutated repeat state: before=%+v after=%+v", playing, after)
	}
	if err := media.Clear(1, clip); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Clear while playing error = %v", err)
	}
	if err := media.DestroyClip(1, clip, nil); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Destroy while playing error = %v", err)
	}
}

func TestMediaMixModeSnapshotHasNoDetachedVoice(t *testing.T) {
	media, _, clip := newRampMedia(t)
	media.SetAudioMixMode(true)
	check(t, media.Play(1, clip, -1))
	state := media.Snapshot()
	if state.BGMVoice != nil {
		t.Fatal("snapshot contains a detached BGM voice")
	}

	registry := NewRegistry(32)
	_, err := registry.Create(1, KindClip)
	check(t, err)
	restored, err := NewMedia(registry, state.Limits)
	check(t, err)
	check(t, restored.Restore(state))
	info, err := restored.Info(1, clip)
	check(t, err)
	if info.State != ClipPlaying || info.RemainingPlays != -1 {
		t.Fatalf("restored loop state = (%v, %d)", info.State, info.RemainingPlays)
	}
}
