package wipi

import (
	"fmt"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

// Raptor titles interrupt short attack sounds before loading the next sound.
// Retracting the whole mix at stop makes a sound shorter than host prebuffering
// inaudible, and also discards any other clip's already rendered samples.
func TestRaptorStopRetainsRenderedAttackAudio(t *testing.T) {
	for _, free := range []bool{false, true} {
		for _, mix := range []bool{false, true} {
			t.Run(fmt.Sprintf("free_%t/mix_%t", free, mix), func(t *testing.T) {
				runtime := newPublicRuntime(t)
				runtime.Services.Media.SetAudioMixMode(mix)
				handle, err := runtime.RaptorCreateClip("audio/wav", 20000, 0x02000001)
				check(t, err)
				pcm := make([]int16, 8000)
				for i := range pcm {
					pcm[i] = 10000
				}
				wave := wipiTestWave(pcm)
				address, err := runtime.Heap.Allocate(uint32(len(wave)), true)
				check(t, err)
				check(t, runtime.CPU.WriteMemory(address, wave))
				if !runtime.RaptorPutClipData(handle, address, int32(len(wave))) || !runtime.RaptorPlayClip(handle, false) {
					t.Fatal("could not start attack sound")
				}
				service := runtime.MediaServices[handle]
				check(t, runtime.Services.Media.Advance(time.Second, time.Second+80*time.Millisecond, runtime.Services.Events))
				revision := runtime.Services.Media.OutputRevision()
				runtime.RaptorStopClip(handle, free)
				if got := runtime.Services.Media.OutputRevision(); got != revision {
					t.Fatalf("attack stop invalidated rendered audio: revision %d -> %d", revision, got)
				}
				audio, start := runtime.Services.Media.DrainTimed()
				if start != time.Second || len(audio.PCM16) != 3528 || audio.PCM16[100] == 0 {
					t.Fatalf("stopped attack audio: start=%v samples=%d", start, len(audio.PCM16))
				}
				if free {
					if _, err := runtime.Services.Media.Info(runtime.ServiceOwner, service); err == nil {
						t.Fatal("freed clip still registered")
					}
					if len(runtime.PendingCallbacks) != 0 {
						t.Fatal("free queued a callback")
					}
				} else {
					info, err := runtime.Services.Media.Info(runtime.ServiceOwner, service)
					check(t, err)
					if info.State != shared.ClipStopped || len(runtime.PendingCallbacks) != 1 || runtime.PendingCallbacks[0].Args[1] != RaptorClipStoppedCode {
						t.Fatalf("stop state=%v callbacks=%+v", info.State, runtime.PendingCallbacks)
					}
					runtime.PendingCallbacks = nil
					runtime.RaptorStopClip(handle, false)
					if !runtime.RaptorClearClipData(handle) {
						t.Fatal("clear failed")
					}
					runtime.RaptorStopClip(handle, true)
					if len(runtime.PendingCallbacks) != 0 || runtime.Services.Media.OutputRevision() != revision {
						t.Fatal("cleanup retracted the stopped sound")
					}
				}
				check(t, runtime.Services.Media.Advance(time.Second+80*time.Millisecond, time.Second+160*time.Millisecond, runtime.Services.Events))
				if got := runtime.Services.Media.Drain().PCM16; len(got) != 0 {
					t.Fatalf("stopped attack generated %d more samples", len(got))
				}
			})
		}
	}
}
