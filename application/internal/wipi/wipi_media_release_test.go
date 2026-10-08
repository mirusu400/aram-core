package wipi

import (
	"testing"
	"time"
)

// If an adapter mirror says stopped while its shared clip is still playing,
// free must release the actual voice before dropping the guest handle. Otherwise
// the next track mixes with an unreachable copy of the old BGM.
func TestRaptorFreeStopsPlaybackWhenAdapterStateIsStale(t *testing.T) {
	for _, test := range []struct {
		state    uint8
		preserve bool
	}{
		{state: 0},
		{state: 0, preserve: true},
		{state: 1},
		{state: 1, preserve: true},
	} {
		name := "standard"
		if test.preserve {
			name = "preserved_loop"
		}
		if test.state == 0 {
			name += "/stopped_mirror"
		} else {
			name += "/non_looping_mirror"
		}
		t.Run(name, func(t *testing.T) {
			runtime := newPublicRuntime(t)
			runtime.Services.Media.SetStoppedLoopPreservation(test.preserve)
			handle, err := runtime.RaptorCreateClip("audio/wav", 128, 0x02000001)
			check(t, err)
			wave := wipiTestWave([]int16{1000, 1000})
			address, err := runtime.Heap.Allocate(uint32(len(wave)), true)
			check(t, err)
			check(t, runtime.CPU.WriteMemory(address, wave))
			if !runtime.RaptorPutClipData(handle, address, int32(len(wave))) ||
				!runtime.RaptorPlayClip(handle, true) {
				t.Fatal("could not start background loop")
			}
			serviceID := runtime.MediaServices[handle]
			// A stale state must not skip the stop, and a stale repeat flag must
			// not capture a freed loop as a detached background voice.
			runtime.MediaClips[handle].State = test.state
			runtime.MediaClips[handle].Repeat = false
			runtime.RaptorStopClip(handle, true)
			check(t, runtime.Services.Media.Advance(0, time.Millisecond, runtime.Services.Events))
			if got := runtime.Services.Media.Drain().PCM16; len(got) != 0 {
				t.Fatalf("freed background loop still produced %d samples", len(got))
			}
			if _, err := runtime.Services.Media.Info(runtime.ServiceOwner, serviceID); err == nil {
				t.Fatal("freed background loop remains registered")
			}
			if runtime.Services.Media.MusicVoiceActive() {
				t.Fatal("free captured the old BGM as a preserved voice")
			}
			if len(runtime.MediaClips) != 0 || len(runtime.MediaServices) != 0 || len(runtime.PendingCallbacks) != 0 {
				t.Fatal("free left adapter handles or callbacks behind")
			}
		})
	}
}
