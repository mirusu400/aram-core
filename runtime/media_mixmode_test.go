package runtime

import (
	"reflect"
	"testing"
	"time"
)

// TestMediaMixModeBGMVoicePersistsOverEffects proves the enhanced mixing
// policy: a looping track becomes a persistent voice that keeps playing after
// the guest stops and destroys its clip, one-shot effects mix over it, and
// re-issuing the identical loop continues it instead of restarting from zero.
func TestMediaMixModeBGMVoicePersistsOverEffects(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	registry := NewRegistry(32)
	media, err := NewMedia(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	media.SetAudioMixMode(true)
	bus := NewEventBus(16, 32)

	// A four-frame ramp so each advance step reads a distinguishable sample and
	// a restart (back to 10) is impossible to confuse with continuation.
	ramp := pcmWave(8_000, 1, []int16{10, 20, 30, 40})
	step := 125 * time.Microsecond // exactly one 8 kHz frame per advance.
	var clock time.Duration
	advance := func() []int16 {
		if err := media.Advance(clock, clock+step, bus); err != nil {
			t.Fatalf("advance at %s: %v", clock, err)
		}
		clock += step
		return media.Drain().PCM16
	}

	bgm, err := media.CreateClip(1, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(1, bgm, ramp); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(1, bgm, -1); err != nil {
		t.Fatal(err)
	}
	// The looping clip is delegated to the voice, so the clip itself is no
	// longer a live playing source (that would double the music).
	if info, err := media.Info(1, bgm); err != nil {
		t.Fatal(err)
	} else if info.State != ClipStopped {
		t.Fatalf("delegated BGM clip state = %v, want ClipStopped", info.State)
	}

	if got := advance(); !reflect.DeepEqual(got, []int16{10}) { // frame 0
		t.Fatalf("frame 0 = %v, want [10]", got)
	}
	if got := advance(); !reflect.DeepEqual(got, []int16{20}) { // frame 1
		t.Fatalf("frame 1 = %v, want [20]", got)
	}

	// Destroying the guest clip must NOT silence the music: the voice survives.
	if err := media.DestroyClip(1, bgm, bus); err != nil {
		t.Fatal(err)
	}
	if got := advance(); !reflect.DeepEqual(got, []int16{30}) { // frame 2, post-destroy
		t.Fatalf("BGM after destroy = %v, want [30] (voice must survive)", got)
	}

	// A one-shot effect on its own clip mixes over the still-playing music.
	sfx, err := media.CreateClip(1, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(1, sfx, pcmWave(8_000, 1, []int16{1000})); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(1, sfx, 1); err != nil {
		t.Fatal(err)
	}
	if got := advance(); !reflect.DeepEqual(got, []int16{1040}) { // 40 (BGM) + 1000 (SFX)
		t.Fatalf("SFX-over-BGM = %v, want [1040]", got)
	}
	if got := advance(); !reflect.DeepEqual(got, []int16{10}) { // BGM loops, SFX done
		t.Fatalf("post-SFX = %v, want [10] (BGM loops, effect finished)", got)
	}

	// The guest stops and re-issues the identical loop on a fresh clip (the
	// "restart the music after the effect" dance). The voice must continue from
	// where it is, not jump back to frame 0.
	bgm2, err := media.CreateClip(1, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(1, bgm2, ramp); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(1, bgm2, -1); err != nil {
		t.Fatal(err)
	}
	if got := advance(); !reflect.DeepEqual(got, []int16{20}) { // frame 1, continued
		t.Fatalf("re-issued identical loop = %v, want [20] (continue, not restart)", got)
	}
}

// TestMediaMixModeVoiceSurvivesSnapshotRestore proves the persistent voice is
// part of the deterministic save state: a machine restored mid-music continues
// the loop identically to one that never saved.
func TestMediaMixModeVoiceSurvivesSnapshotRestore(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	ramp := pcmWave(8_000, 1, []int16{10, 20, 30, 40})

	build := func() (*Media, *EventBus) {
		registry := NewRegistry(32)
		media, err := NewMedia(registry, limits)
		if err != nil {
			t.Fatal(err)
		}
		media.SetAudioMixMode(true)
		bus := NewEventBus(16, 32)
		clip, err := media.CreateClip(1, "audio/wav", 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := media.Append(1, clip, ramp); err != nil {
			t.Fatal(err)
		}
		if err := media.Play(1, clip, -1); err != nil {
			t.Fatal(err)
		}
		// Destroy the source clip (as a title does): only the persistent voice
		// remains, so the snapshot carries no registry-bound clip and restores
		// into a fresh instance cleanly.
		if err := media.DestroyClip(1, clip, bus); err != nil {
			t.Fatal(err)
		}
		return media, bus
	}

	live, liveBus := build()
	// Advance the reference machine two frames (voice now at frame 2).
	for i := 0; i < 2; i++ {
		if err := live.Advance(time.Duration(i)*125*time.Microsecond, time.Duration(i+1)*125*time.Microsecond, liveBus); err != nil {
			t.Fatal(err)
		}
		live.Drain()
	}

	// A second machine advances two frames, saves, and is reloaded into a fresh
	// instance before continuing.
	saved, savedBus := build()
	for i := 0; i < 2; i++ {
		if err := saved.Advance(time.Duration(i)*125*time.Microsecond, time.Duration(i+1)*125*time.Microsecond, savedBus); err != nil {
			t.Fatal(err)
		}
		saved.Drain()
	}
	state := saved.Snapshot()
	if state.BGMVoice == nil || !state.AudioMixMode {
		t.Fatalf("snapshot dropped the music voice: mix=%v voice=%v", state.AudioMixMode, state.BGMVoice)
	}
	registry := NewRegistry(32)
	restored, err := NewMedia(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Restore(state); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !restored.AudioMixMode() {
		t.Fatal("restored media lost mix mode")
	}

	// Both continue two more frames; the restored machine must match the one
	// that never round-tripped.
	restoredBus := NewEventBus(16, 32)
	for i := 2; i < 4; i++ {
		start := time.Duration(i) * 125 * time.Microsecond
		end := time.Duration(i+1) * 125 * time.Microsecond
		if err := live.Advance(start, end, liveBus); err != nil {
			t.Fatal(err)
		}
		if err := restored.Advance(start, end, restoredBus); err != nil {
			t.Fatal(err)
		}
		want := live.Drain().PCM16
		got := restored.Drain().PCM16
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("frame %d after restore = %v, want %v", i, got, want)
		}
	}
}

// TestMediaMixModeVoicesLongOneShotBGM proves the mixing policy still rescues a
// title that loops its music by hand (a long non-repeat clip it replays the
// moment it ends): the replay is promoted to the persistent voice and survives
// destroy, while the first play and a short one-shot effect stay ordinary clips.
func TestMediaMixModeVoicesLongOneShotBGM(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	registry := NewRegistry(32)
	media, err := NewMedia(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	media.SetAudioMixMode(true)
	bus := NewEventBus(16, 32)

	// A ~1.3s clip (> musicVoiceMinDuration) played with plays=1 (repeat=false).
	longSamples := make([]int16, 10_400) // 10400 / 8000 Hz = 1.3s
	for i := range longSamples {
		longSamples[i] = 100
	}
	bgm, err := media.CreateClip(1, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(1, bgm, pcmWave(8_000, 1, longSamples)); err != nil {
		t.Fatal(err)
	}
	// The first play is an ordinary clip: nothing has shown this track loops.
	if err := media.Play(1, bgm, 1); err != nil {
		t.Fatal(err)
	}
	if info, err := media.Info(1, bgm); err != nil {
		t.Fatal(err)
	} else if info.State != ClipPlaying {
		t.Fatalf("first long one-shot state = %v, want ClipPlaying (not voiced)", info.State)
	}
	// Run it out. The title sees the completion and replays the same track,
	// which is the hand-written loop the mixing policy takes over.
	if err := media.Advance(0, 1300*time.Millisecond, bus); err != nil {
		t.Fatal(err)
	}
	if info, err := media.Info(1, bgm); err != nil {
		t.Fatal(err)
	} else if info.State != ClipStopped {
		t.Fatalf("long one-shot after its end = %v, want ClipStopped", info.State)
	}
	media.Drain()
	if err := media.Play(1, bgm, 1); err != nil {
		t.Fatal(err)
	}
	if info, err := media.Info(1, bgm); err != nil {
		t.Fatal(err)
	} else if info.State != ClipStopped {
		t.Fatalf("replayed long BGM clip state = %v, want ClipStopped (voiced)", info.State)
	}
	if err := media.DestroyClip(1, bgm, bus); err != nil {
		t.Fatal(err)
	}
	if err := media.Advance(
		1300*time.Millisecond,
		1300*time.Millisecond+125*time.Microsecond,
		bus,
	); err != nil {
		t.Fatal(err)
	}
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{100}) {
		t.Fatalf("long BGM after destroy = %v, want [100] (voice survives)", got)
	}

	// A short one-shot effect is NOT voiced: it stays a normal clip.
	sfx, err := media.CreateClip(1, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(1, sfx, pcmWave(8_000, 1, []int16{500, 500})); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(1, sfx, 1); err != nil {
		t.Fatal(err)
	}
	if info, err := media.Info(1, sfx); err != nil {
		t.Fatal(err)
	} else if info.State != ClipPlaying {
		t.Fatalf("short effect clip state = %v, want ClipPlaying (not voiced)", info.State)
	}
}

// TestMediaMixModeLeavesLongOneShotCueAlone is the 추억의달고나 report: the
// title plays several one-shot effects longer than a second - the longest a
// 4.06s game-over sting - and the mixing policy used to promote every one of
// them to the persistent music voice on length alone. The voice loops forever
// and outlives the title's own stop, so the sting played over and over and took
// the real background music's place. A cue the title never replays must stay an
// ordinary clip that stops when it ends and stays stopped when it is stopped.
func TestMediaMixModeLeavesLongOneShotCueAlone(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	registry := NewRegistry(32)
	media, err := NewMedia(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	media.SetAudioMixMode(true)
	bus := NewEventBus(16, 32)

	music := make([]int16, 16_000) // 2s of looping background music
	for i := range music {
		music[i] = 40
	}
	bgm, err := media.CreateClip(1, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(1, bgm, pcmWave(8_000, 1, music)); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(1, bgm, -1); err != nil {
		t.Fatal(err)
	}
	if !media.MusicVoiceActive() {
		t.Fatal("looping track was not promoted to the music voice")
	}

	sting := make([]int16, 32_480) // 4.06s, the game-over cue
	for i := range sting {
		sting[i] = 700
	}
	cue, err := media.CreateClip(1, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(1, cue, pcmWave(8_000, 1, sting)); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(1, cue, 1); err != nil {
		t.Fatal(err)
	}
	if info, err := media.Info(1, cue); err != nil {
		t.Fatal(err)
	} else if info.State != ClipPlaying {
		t.Fatalf("long one-shot cue state = %v, want ClipPlaying (not voiced)", info.State)
	}
	// The music the title asked to loop is still the voice, not the cue.
	if media.bgmVoiceSig != bgmSignature(pcmWave(8_000, 1, music)) {
		t.Fatal("the one-shot cue displaced the looping music voice")
	}

	// It ends on its own and stays silent: nothing loops it.
	if err := media.Advance(0, 4100*time.Millisecond, bus); err != nil {
		t.Fatal(err)
	}
	if info, err := media.Info(1, cue); err != nil {
		t.Fatal(err)
	} else if info.State != ClipStopped {
		t.Fatalf("cue state after its end = %v, want ClipStopped", info.State)
	}
	media.Drain()
	if err := media.Advance(
		4100*time.Millisecond,
		4100*time.Millisecond+125*time.Microsecond,
		bus,
	); err != nil {
		t.Fatal(err)
	}
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{40}) {
		t.Fatalf("mix after the cue ended = %v, want [40] (music alone)", got)
	}

	// Replaying it much later is a fresh cue, not a loop the title is driving.
	if err := media.Advance(
		4100*time.Millisecond+125*time.Microsecond,
		9*time.Second,
		bus,
	); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(1, cue, 1); err != nil {
		t.Fatal(err)
	}
	if info, err := media.Info(1, cue); err != nil {
		t.Fatal(err)
	} else if info.State != ClipPlaying {
		t.Fatalf("replayed cue state = %v, want ClipPlaying (not voiced)", info.State)
	}
	if err := media.Stop(1, cue); err != nil {
		t.Fatal(err)
	}
	media.Drain()
	if err := media.Advance(
		9*time.Second,
		9*time.Second+125*time.Microsecond,
		bus,
	); err != nil {
		t.Fatal(err)
	}
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{40}) {
		t.Fatalf("mix after stopping the cue = %v, want [40] (cue silenced)", got)
	}
}

// TestMediaFaithfulModeHonoursStopAndDestroy pins the default policy: a looping
// clip is a normal live source and destroying it silences the timeline.
func TestMediaFaithfulModeHonoursStopAndDestroy(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	registry := NewRegistry(32)
	media, err := NewMedia(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(16, 32)
	clip, err := media.CreateClip(1, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(1, clip, pcmWave(8_000, 1, []int16{50, 60})); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(1, clip, -1); err != nil {
		t.Fatal(err)
	}
	if info, err := media.Info(1, clip); err != nil {
		t.Fatal(err)
	} else if info.State != ClipPlaying {
		t.Fatalf("faithful looping clip state = %v, want ClipPlaying", info.State)
	}
	if err := media.Advance(0, 125*time.Microsecond, bus); err != nil {
		t.Fatal(err)
	}
	if got := media.Drain().PCM16; !reflect.DeepEqual(got, []int16{50}) {
		t.Fatalf("faithful frame 0 = %v, want [50]", got)
	}
	if err := media.DestroyClip(1, clip, bus); err != nil {
		t.Fatal(err)
	}
	if err := media.Advance(125*time.Microsecond, 250*time.Microsecond, bus); err != nil {
		t.Fatal(err)
	}
	if got := media.Drain().PCM16; len(got) != 0 {
		t.Fatalf("faithful post-destroy audio = %v, want silence", got)
	}
}
