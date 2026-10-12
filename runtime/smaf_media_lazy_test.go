package runtime

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func mixedATRMediaScore(channels int) []byte {
	fm := sustainedSMAFScore(2)
	waveType := byte(1) // Signed 16-bit PCM at 8 kHz.
	if channels == 2 {
		waveType |= 0x80
	}
	atr := smafTestATR(1, []byte{0, 0, waveType, 0x30, 2, 2},
		[]byte{0, 1, 125, 0, 0, 0, 0}, smafTestPCM16(512, channels))
	return smafStreamTestFile(fm[8:], atr)
}

// The original runtime has already rendered a prefix, whereas the restored
// runtime must reconstruct it from the source. Exercise both an active ATR
// sample and the natural FM tail before crossing the registered clip's loop.
func TestSMAFMixedATRMediaSeekRestoreLoopAndCompletion(t *testing.T) {
	for _, format := range []struct {
		rate     uint32
		channels int
	}{{8000, 1}, {48000, 2}, {192000, 2}} {
		t.Run(fmt.Sprintf("%dHz/%dch", format.rate, format.channels), func(t *testing.T) {
			score := mixedATRMediaScore(format.channels)
			full := decodeSMAFPCM16(score, format.rate)
			if full == nil || len(full.samples) == 0 {
				t.Fatal("mixed ATR/FM fixture did not render")
			}
			for _, restoreAt := range []string{"active_atr", "loop_tail"} {
				t.Run(restoreAt, func(t *testing.T) {
					limits := DefaultMediaLimits()
					limits.OutputSampleRate, limits.OutputChannels = format.rate, 2
					media, err := NewMedia(NewRegistry(32), limits)
					check(t, err)
					const owner OwnerID = 3
					clip, err := media.CreateClip(owner, "audio/mmf", 0)
					check(t, err)
					_, err = media.Append(owner, clip, score)
					check(t, err)
					check(t, media.Play(owner, clip, 2))
					decoded := media.clips[clip].decoded
					if decoded == nil || decoded.smaf == nil || decoded.smaf.cursor != 0 || len(decoded.samples) != 0 {
						t.Fatal("mixed score did not start with an untouched lazy stream")
					}
					if decoded.duration != full.duration {
						t.Fatalf("lazy duration %s, whole render %s", decoded.duration, full.duration)
					}

					// These frame counts have integral nanosecond durations at every
					// tested rate, so the timeline oracle needs no rounding tolerance.
					const tailFrames, headFrames, stepFrames = uint64(6), uint64(12), uint64(18)
					warmup := durationForFrame(headFrames, format.rate)
					step := durationForFrame(stepFrames, format.rate)
					bus := NewEventBus(32, 32)
					check(t, media.Advance(0, warmup, bus))
					if got := media.Drain().PCM16; !slices.Equal(got, full.samples[:headFrames*2]) {
						t.Fatal("initial mixed PCM differs from whole render")
					}
					loopTail := full.duration - durationForFrame(tailFrames, format.rate)
					seekTo := loopTail
					if restoreAt == "active_atr" {
						seekTo = durationForFrame(uint64(format.rate)/32, format.rate)
					}
					check(t, media.Seek(owner, clip, seekTo))
					saved := media.Snapshot()
					registry := NewRegistry(32)
					check(t, registry.Restore(media.registry.Snapshot()))
					restored, err := NewMedia(registry, limits)
					check(t, err)
					check(t, restored.Restore(saved))
					restoredDecoded := restored.clips[clip].decoded
					if restoredDecoded == decoded || restoredDecoded == nil || restoredDecoded.smaf == nil ||
						restoredDecoded.smaf.cursor != 0 || len(restoredDecoded.samples) != 0 {
						t.Fatal("fresh restore reused rendered host state instead of rebuilding the lazy score")
					}
					restoredBus := NewEventBus(32, 32)
					assertTimeline := func() ClipInfo {
						t.Helper()
						info, err := media.Info(owner, clip)
						check(t, err)
						restoredInfo, err := restored.Info(owner, clip)
						check(t, err)
						if info != restoredInfo {
							t.Fatalf("restored timeline %+v differs from original %+v", restoredInfo, info)
						}
						wantAvailable := uint64(len(score)) - uint64(len(score))*uint64(info.Position)/uint64(full.duration)
						for _, current := range []*Media{media, restored} {
							available, err := current.AvailableBytes(owner, clip)
							check(t, err)
							if available != wantAvailable {
								t.Fatalf("available bytes %d at %s, want %d", available, info.Position, wantAvailable)
							}
						}
						if !reflect.DeepEqual(bus.Snapshot(), restoredBus.Snapshot()) {
							t.Fatal("restored completion events differ from original")
						}
						return info
					}
					assertTimeline()
					advance := func(start uint64, want []int16) {
						t.Helper()
						at := warmup + durationForFrame(start, format.rate)
						check(t, media.Advance(at, at+step, bus))
						check(t, restored.Advance(at, at+step, restoredBus))
						got, restoredPCM := media.Drain().PCM16, restored.Drain().PCM16
						if !slices.Equal(got, want) || !slices.Equal(restoredPCM, want) {
							t.Fatalf("mixed PCM after restore/seek differs: original %v, restored %v, want %v", got, restoredPCM, want)
						}
						assertTimeline()
					}
					elapsedFrames := uint64(0)
					if restoreAt == "active_atr" {
						first := uint64(format.rate) / 32
						want := full.samples[first*2 : (first+stepFrames)*2]
						if format.channels == 2 {
							distinct := false
							for frame := 0; frame < len(want); frame += 2 {
								distinct = distinct || want[frame] != want[frame+1]
							}
							if !distinct {
								t.Fatal("stereo fixture has no distinct ATR channels at restore")
							}
						}
						advance(elapsedFrames, want)
						elapsedFrames += stepFrames
						check(t, media.Seek(owner, clip, loopTail))
						check(t, restored.Seek(owner, clip, loopTail))
						assertTimeline()
					}
					wantLoop := append(slices.Clone(full.samples[len(full.samples)-int(tailFrames)*2:]), full.samples[:headFrames*2]...)
					advance(elapsedFrames, wantLoop)
					elapsedFrames += stepFrames
					info := assertTimeline()
					if info.State != ClipPlaying || info.RemainingPlays != 1 ||
						info.Position != durationForFrame(headFrames, format.rate) || bus.Len() != 0 {
						t.Fatalf("loop boundary timeline %+v, events %+v", info, bus.Snapshot().Events)
					}
					check(t, media.Seek(owner, clip, loopTail))
					check(t, restored.Seek(owner, clip, loopTail))
					assertTimeline()
					wantFinal := make([]int16, stepFrames*2)
					copy(wantFinal, full.samples[len(full.samples)-int(tailFrames)*2:])
					advance(elapsedFrames, wantFinal)
					info = assertTimeline()
					if info.State != ClipStopped || info.RemainingPlays != 0 || info.Position != full.duration || bus.Len() != 1 {
						t.Fatalf("final completion timeline %+v, events %+v", info, bus.Snapshot().Events)
					}
					event := bus.Snapshot().Events[0]
					wantAt := warmup + durationForFrame(elapsedFrames+tailFrames, format.rate)
					if event.At != wantAt || event.Kind != EventAudioComplete || event.Owner != owner ||
						event.ServiceID != clip || event.Name != "complete" {
						t.Fatalf("completion %+v, want clip %s at %s", event, clip, wantAt)
					}
				})
			}
		})
	}
}
