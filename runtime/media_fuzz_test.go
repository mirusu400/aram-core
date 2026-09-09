package runtime

import (
	"errors"
	"testing"
	"time"
)

// FuzzMediaStateMachine drives lifecycle, buffering, time, output, and
// save/restore through one byte-command stream. It intentionally includes
// invalid transitions: those must return a typed error without mutating the
// guest-visible clip state.
func FuzzMediaStateMachine(f *testing.F) {
	f.Add([]byte{0, 1, 3, 9, 9, 5, 9, 6, 10, 11, 12})
	f.Add([]byte{0, 1, 4, 9, 9, 9, 7, 9, 13, 0, 1, 3})
	f.Add([]byte{0, 2, 3, 3, 5, 5, 6, 6, 7, 7, 8, 12})
	f.Fuzz(func(t *testing.T, commands []byte) {
		if len(commands) > 256 {
			commands = commands[:256]
		}
		limits := DefaultMediaLimits()
		limits.OutputSampleRate = 8_000
		limits.OutputChannels = 1
		registry := NewRegistry(64)
		media, err := NewMedia(registry, limits)
		if err != nil {
			t.Fatal(err)
		}
		bus := NewEventBus(512, 64)
		wave := pcmWave(8_000, 1, []int16{10, 20, 30, 40, 50, 60, 70, 80})
		const owner OwnerID = 1
		var id ServiceID
		var now time.Duration

		create := func() {
			if id != 0 {
				return
			}
			id, err = media.CreateClip(owner, "audio/wav", uint64(len(wave)*4))
			if err != nil {
				t.Fatal(err)
			}
		}
		for _, command := range commands {
			operation := command % 14
			if operation != 0 {
				create()
			}
			var before ClipInfo
			if id != 0 {
				before, err = media.Info(owner, id)
				if err != nil {
					t.Fatal(err)
				}
			}
			switch operation {
			case 0: // Create
				create()
			case 1: // Append
				_, _ = media.Append(owner, id, wave)
			case 2: // Clear
				err = media.Clear(owner, id)
			case 3: // Play once
				err = media.Play(owner, id, 1)
			case 4: // Loop
				err = media.Play(owner, id, -1)
			case 5: // Pause
				err = media.Pause(owner, id)
				if err == nil {
					position := before.Position
					if advanceErr := media.Advance(now, now+time.Millisecond, bus); advanceErr != nil {
						t.Fatal(advanceErr)
					}
					now += time.Millisecond
					after, _ := media.Info(owner, id)
					if after.Position != position || len(media.Drain().PCM16) != 0 {
						t.Fatalf("paused clip advanced: before=%s after=%s", position, after.Position)
					}
				}
			case 6: // Resume
				err = media.Resume(owner, id)
			case 7: // Stop
				err = media.Stop(owner, id)
				if err == nil {
					if len(media.Drain().PCM16) != 0 {
						t.Fatal("Stop retained queued PCM")
					}
					if advanceErr := media.Advance(now, now+time.Millisecond, bus); advanceErr != nil {
						t.Fatal(advanceErr)
					}
					now += time.Millisecond
					if len(media.Drain().PCM16) != 0 {
						t.Fatal("stopped clip generated PCM")
					}
				}
			case 8: // Seek
				err = media.Seek(owner, id, time.Duration(command%2)*125*time.Microsecond)
			case 9: // Advance
				delta := time.Duration(command%5+1) * 125 * time.Microsecond
				err = media.Advance(now, now+delta, bus)
				now += delta
			case 10: // Drain
				audio := media.Drain()
				if len(audio.PCM16)%audio.Channels != 0 {
					t.Fatalf("unaligned PCM length %d/%d", len(audio.PCM16), audio.Channels)
				}
			case 11: // Gain
				err = media.SetClipGain(owner, id, command%101, false, int8(command%201)-100)
			case 12: // Snapshot/restore
				state := media.Snapshot()
				err = media.Restore(state)
			case 13: // Destroy
				err = media.DestroyClip(owner, id, bus)
				if err == nil {
					id = 0
				}
			}

			if id == 0 {
				continue
			}
			after, infoErr := media.Info(owner, id)
			if infoErr != nil {
				t.Fatal(infoErr)
			}
			if errors.Is(err, ErrInvalidState) && operation != 12 && after != before {
				t.Fatalf("invalid operation %d mutated clip: before=%+v after=%+v", operation, before, after)
			}
			if !after.State.Valid() || after.Volume > 100 ||
				(after.State == ClipStopped && after.RemainingPlays != 0) ||
				((after.State == ClipPlaying || after.State == ClipPaused) &&
					after.RemainingPlays != -1 && after.RemainingPlays <= 0) {
				t.Fatalf("invalid clip invariant: %+v", after)
			}
			if after.Decoded && after.WaitingForData {
				t.Fatalf("decoded clip is waiting for data: %+v", after)
			}
			if media.MusicVoiceActive() {
				t.Fatal("state machine created an unowned music voice")
			}
		}
	})
}

func TestMediaAvailableBytesDecreaseWithPlayback(t *testing.T) {
	media, bus, clip := newRampMedia(t)
	before, err := media.AvailableBytes(1, clip)
	check(t, err)
	check(t, media.Play(1, clip, 1))
	check(t, media.Advance(0, 250*time.Microsecond, bus))
	after, err := media.AvailableBytes(1, clip)
	check(t, err)
	if after >= before {
		t.Fatalf("available bytes did not decrease: before=%d after=%d", before, after)
	}
}

func TestMediaRejectsSealedUnsupportedSource(t *testing.T) {
	media, _, clip := newRampMedia(t)
	check(t, media.Clear(1, clip))
	_, err := media.Append(1, clip, []byte("not an audio container"))
	check(t, err)
	if err := media.Play(1, clip, 1); !errors.Is(err, ErrMediaUnsupported) {
		t.Fatalf("unsupported Play error = %v", err)
	}
	info, err := media.Info(1, clip)
	check(t, err)
	if info.State != ClipStopped || info.Position != 0 {
		t.Fatalf("unsupported Play mutated clip: %+v", info)
	}
}

func TestMediaAdvancePartitionAndRestoreEquivalence(t *testing.T) {
	build := func() (*Media, *EventBus, ServiceID) {
		media, bus, clip := newRampMedia(t)
		check(t, media.Play(1, clip, -1))
		return media, bus, clip
	}
	one, oneBus, oneClip := build()
	partitioned, partitionedBus, partitionedClip := build()
	check(t, one.Advance(0, time.Millisecond, oneBus))
	var now time.Duration
	for _, delta := range []time.Duration{125 * time.Microsecond, 375 * time.Microsecond, 500 * time.Microsecond} {
		check(t, partitioned.Advance(now, now+delta, partitionedBus))
		now += delta
	}
	oneAudio := one.Drain().PCM16
	partitionedAudio := partitioned.Drain().PCM16
	if string(int16Bytes(oneAudio)) != string(int16Bytes(partitionedAudio)) {
		t.Fatalf("partitioned PCM differs: one=%v partitioned=%v", oneAudio, partitionedAudio)
	}
	oneInfo, _ := one.Info(1, oneClip)
	partitionedInfo, _ := partitioned.Info(1, partitionedClip)
	if oneInfo.Position != partitionedInfo.Position ||
		oneInfo.RemainingPlays != partitionedInfo.RemainingPlays {
		t.Fatalf("partitioned state differs: one=%+v partitioned=%+v", oneInfo, partitionedInfo)
	}

	state := partitioned.Snapshot()
	registry := NewRegistry(64)
	created, err := registry.Create(1, KindClip)
	check(t, err)
	if created != partitionedClip {
		t.Fatalf("restored registry id = %s, want %s", created, partitionedClip)
	}
	restored, err := NewMedia(registry, state.Limits)
	check(t, err)
	check(t, restored.Restore(state))
	restoredBus := NewEventBus(512, 64)
	check(t, partitioned.Advance(now, now+500*time.Microsecond, partitionedBus))
	check(t, restored.Advance(now, now+500*time.Microsecond, restoredBus))
	if got, want := restored.Drain().PCM16, partitioned.Drain().PCM16; string(int16Bytes(got)) != string(int16Bytes(want)) {
		t.Fatalf("restored PCM differs: got=%v want=%v", got, want)
	}
}

func int16Bytes(samples []int16) []byte {
	result := make([]byte, len(samples)*2)
	for index, sample := range samples {
		result[index*2] = byte(sample)
		result[index*2+1] = byte(uint16(sample) >> 8)
	}
	return result
}
