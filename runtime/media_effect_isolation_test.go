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

func TestMediaStoppedLoopCompatibilityPreservesBGMWhileClipIsReused(t *testing.T) {
	media, bus, clip := newRampMedia(t)
	media.SetStoppedLoopPreservation(true)
	check(t, media.Play(1, clip, -1))
	step := 125 * time.Microsecond
	check(t, media.Advance(0, step, bus))
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{10}) {
		t.Fatalf("first BGM frame = %v, want [10]", got)
	}

	check(t, media.Stop(1, clip))
	check(t, media.Clear(1, clip))
	_, err := media.Append(1, clip, pcmWave(8_000, 1, []int16{1000}))
	check(t, err)
	check(t, media.Play(1, clip, 1))
	check(t, media.Advance(step, 2*step, bus))
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{1020}) {
		t.Fatalf("BGM/effect frame = %v, want [1020]", got)
	}
	if !media.MusicVoiceActive() {
		t.Fatal("stopped BGM loop was not preserved")
	}

	check(t, media.Advance(2*step, 3*step, bus))
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{30}) {
		t.Fatalf("post-effect BGM frame = %v, want [30]", got)
	}

	// Selecting another loop replaces the compatibility voice instead of
	// mixing two background tracks.
	check(t, media.Clear(1, clip))
	_, err = media.Append(1, clip, pcmWave(8_000, 1, []int16{8, 10}))
	check(t, err)
	check(t, media.Play(1, clip, -1))
	check(t, media.Advance(3*step, 4*step, bus))
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{8}) {
		t.Fatalf("replacement BGM frame = %v, want [8]", got)
	}
	if media.MusicVoiceActive() {
		t.Fatal("previous BGM voice survived replacement loop")
	}
}

func TestMediaStoppedLoopCompatibilityDoesNotResumePausedMusic(t *testing.T) {
	media, bus, clip := newRampMedia(t)
	media.SetStoppedLoopPreservation(true)
	check(t, media.Play(1, clip, -1))
	check(t, media.Pause(1, clip))
	check(t, media.Stop(1, clip))
	if media.MusicVoiceActive() {
		t.Fatal("stopping a paused loop resumed it as background music")
	}
	check(t, media.Advance(0, 125*time.Microsecond, bus))
	if got := media.Drain().PCM16; len(got) != 0 {
		t.Fatalf("paused and stopped loop still produced audio: %v", got)
	}
}

func TestMediaStoppedLoopReuseKeepsBufferedMusic(t *testing.T) {
	media, bus, clip := newRampMedia(t)
	media.SetStoppedLoopPreservation(true)
	check(t, media.Play(1, clip, -1))
	step := 125 * time.Microsecond
	check(t, media.Advance(0, step, bus))
	revision := media.OutputRevision()

	check(t, media.Stop(1, clip))
	if media.OutputRevision() != revision {
		t.Fatal("preserved loop changed the output generation")
	}
	check(t, media.Clear(1, clip))
	if media.OutputRevision() != revision {
		t.Fatal("clearing the stopped source interrupted the preserved loop")
	}
	_, err := media.Append(1, clip, pcmWave(8_000, 1, []int16{1000}))
	check(t, err)
	check(t, media.Play(1, clip, 1))
	check(t, media.Advance(step, 2*step, bus))
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{10, 1020}) {
		t.Fatalf("buffered music and effect = %v, want [10 1020]", got)
	}
}

func TestMediaReplaceStoppedEffectKeepsOtherClipOutput(t *testing.T) {
	media, bus, bgm := newRampMedia(t)
	fx, err := media.CreateClip(1, "audio/wav", 0)
	check(t, err)
	check(t, media.Play(1, bgm, -1))
	step := 125 * time.Microsecond
	check(t, media.Advance(0, step, bus))
	revision := media.OutputRevision()
	check(t, media.ReplaceSource(1, fx, pcmWave(8_000, 1, []int16{1000})))
	if media.OutputRevision() != revision {
		t.Fatal("replacing a stopped effect changed the output generation")
	}
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{10}) {
		t.Fatalf("buffered music = %v, want [10]", got)
	}
}

func TestMediaStoppedLoopCompatibilitySurvivesSnapshot(t *testing.T) {
	media, bus, clip := newRampMedia(t)
	media.SetStoppedLoopPreservation(true)
	check(t, media.Play(1, clip, -1))
	check(t, media.Advance(0, 125*time.Microsecond, bus))
	_ = media.Drain()
	check(t, media.Stop(1, clip))
	state := media.Snapshot()
	if state.BGMVoice == nil {
		t.Fatal("snapshot omitted compatibility BGM voice")
	}

	registry := NewRegistry(32)
	_, err := registry.Create(1, KindClip)
	check(t, err)
	restored, err := NewMedia(registry, state.Limits)
	check(t, err)
	check(t, restored.Restore(state))
	check(t, restored.Advance(
		125*time.Microsecond,
		250*time.Microsecond,
		NewEventBus(32, 64),
	))
	if got := restored.Drain().PCM16; !reflect.DeepEqual(got, []int16{20}) {
		t.Fatalf("restored BGM frame = %v, want [20]", got)
	}
}

func TestServicesRestoreKeepsStoppedLoopCompatibilityVoice(t *testing.T) {
	services, err := NewServices(DefaultConfig())
	check(t, err)
	services.Media.SetStoppedLoopPreservation(true)
	clip, err := services.Media.CreateClip(1, "audio/wav", 0)
	check(t, err)
	_, err = services.Media.Append(1, clip, pcmWave(8_000, 1, []int16{10, 20}))
	check(t, err)
	check(t, services.Media.Play(1, clip, -1))
	check(t, services.Media.Stop(1, clip))
	if !services.Media.MusicVoiceActive() {
		t.Fatal("test setup did not preserve the stopped loop")
	}

	state := services.Snapshot()
	check(t, services.Restore(state))
	if !services.Media.StoppedLoopPreservation() || !services.Media.MusicVoiceActive() {
		t.Fatal("Services.Restore lost the title's background voice")
	}

	encoded, err := services.MarshalBinary()
	check(t, err)
	restored, err := NewServices(DefaultConfig())
	check(t, err)
	restored.Media.SetStoppedLoopPreservation(true)
	check(t, restored.UnmarshalBinary(encoded))
	if !restored.Media.StoppedLoopPreservation() || !restored.Media.MusicVoiceActive() {
		t.Fatal("Services.UnmarshalBinary lost the title's background voice")
	}
}
