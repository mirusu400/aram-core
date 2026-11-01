package ktf

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

// shortKTFWave is 10 ms of audible 8 kHz mono PCM16: long enough to decode
// and play, short enough that one Advance runs it out.
func shortKTFWave() []byte {
	samples := 80
	data := make([]byte, 44+samples*2)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 8_000)
	binary.LittleEndian.PutUint32(data[28:32], 16_000)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(samples*2))
	for index := range samples {
		binary.LittleEndian.PutUint16(data[44+index*2:], uint16(int16(4_000)))
	}
	return data
}

func takeKTFMediaCallbacks(runtime *Runtime) []ktfPendingMediaCallback {
	callbacks := runtime.pendingMediaCallbacks
	runtime.pendingMediaCallbacks = nil
	return callbacks
}

// TestKTFWIPICMediaStopAfterEndDeliversStopCallback covers 렙업만이살길
// (issue #561). Its media callback answers END with MC_mdaStop and frees the
// clip only when the STOP callback arrives. Stopping a clip that already ran
// out used to fail without a callback, so every sound effect leaked a clip
// and the title faulted on "media clip count reached 256" mid-game.
func TestKTFWIPICMediaStopAfterEndDeliversStopCallback(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	mediaType, err := runtime.allocateBytes([]byte("audio/wav"), true)
	check(t, err)
	const callback = uint32(0x001139c9)
	wave := shortKTFWave()

	// Far more sound effects than the media pool holds.
	limit := int(runtime.Services.Config.Limits.Media.MaxClips)
	var now time.Duration
	for effect := range limit + 8 {
		setKTFWIPICCallArguments(t, runtime, []uint32{mediaType, uint32(len(wave)), callback})
		handle, err := ktfWIPICMediaCreate(context.Background(), runtime)
		check(t, err)
		if handle == 0 {
			t.Fatalf("effect %d: create failed with %d live clips", effect, len(runtime.wipicMediaClips))
		}
		if got := putKTFTestMediaData(t, runtime, handle, wave); got != uint32(len(wave)) {
			t.Fatalf("effect %d: put accepted %d bytes", effect, int32(got))
		}
		setKTFWIPICCallArguments(t, runtime, []uint32{handle, 0})
		if result, err := ktfWIPICMediaPlay(context.Background(), runtime); err != nil || result != 0 {
			t.Fatalf("effect %d: play = %d, %v", effect, int32(result), err)
		}
		takeKTFMediaCallbacks(runtime) // START

		// The sound runs out on its own and END is delivered.
		next := now + 100*time.Millisecond
		check(t, runtime.Services.Media.Advance(now, next, runtime.Services.Events))
		now = next
		check(t, runtime.DrainServiceEvents(now))
		callbacks := takeKTFMediaCallbacks(runtime)
		if len(callbacks) != 1 || callbacks[0].handle != handle ||
			callbacks[0].event != guest.WIPIMediaEnd {
			t.Fatalf("effect %d: callbacks after the sound ran out = %+v, want END", effect, callbacks)
		}

		// The title's END handler stops the clip ...
		setKTFWIPICCallArguments(t, runtime, []uint32{handle})
		if result, err := ktfWIPICMediaStop(context.Background(), runtime); err != nil || result != 0 {
			t.Fatalf("effect %d: stop after END = %d, %v", effect, int32(result), err)
		}
		callbacks = takeKTFMediaCallbacks(runtime)
		if len(callbacks) != 1 || callbacks[0].handle != handle ||
			callbacks[0].event != guest.WIPIMediaStop {
			t.Fatalf("effect %d: callbacks after stop = %+v, want STOP", effect, callbacks)
		}
		// ... and a second stop owes nothing more, so a title that stops
		// again from its STOP handler cannot loop forever.
		setKTFWIPICCallArguments(t, runtime, []uint32{handle})
		if result, err := ktfWIPICMediaStop(context.Background(), runtime); err != nil ||
			result != ktfWIPICErrorInvalid {
			t.Fatalf("effect %d: second stop = %d, %v", effect, int32(result), err)
		}
		if callbacks := takeKTFMediaCallbacks(runtime); len(callbacks) != 0 {
			t.Fatalf("effect %d: second stop queued %+v", effect, callbacks)
		}

		// ... and its STOP handler frees it.
		setKTFWIPICCallArguments(t, runtime, []uint32{handle})
		if result, err := ktfWIPICMediaDestroy(context.Background(), runtime); err != nil || result != 0 {
			t.Fatalf("effect %d: free = %d, %v", effect, int32(result), err)
		}
	}
	if live := len(runtime.wipicMediaClips); live != 0 {
		t.Fatalf("%d clips still live after every effect was freed", live)
	}
}

// A clip that is replayed after running out starts a new playback, so its
// next stop is an ordinary one.
func TestKTFWIPICMediaReplayAfterEndStopsNormally(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	mediaType, err := runtime.allocateBytes([]byte("audio/wav"), true)
	check(t, err)
	wave := shortKTFWave()
	setKTFWIPICCallArguments(t, runtime, []uint32{mediaType, uint32(len(wave)), 0x001139c9})
	handle, err := ktfWIPICMediaCreate(context.Background(), runtime)
	check(t, err)
	putKTFTestMediaData(t, runtime, handle, wave)
	setKTFWIPICCallArguments(t, runtime, []uint32{handle, 0})
	_, err = ktfWIPICMediaPlay(context.Background(), runtime)
	check(t, err)
	check(t, runtime.Services.Media.Advance(0, 100*time.Millisecond, runtime.Services.Events))
	check(t, runtime.DrainServiceEvents(100*time.Millisecond))
	if !runtime.wipicMediaClips[handle].endedUnstopped {
		t.Fatal("a clip that ran out is not marked as owing a stop")
	}

	setKTFWIPICCallArguments(t, runtime, []uint32{handle, 1})
	if result, err := ktfWIPICMediaPlay(context.Background(), runtime); err != nil || result != 0 {
		t.Fatalf("replay = %d, %v", int32(result), err)
	}
	if runtime.wipicMediaClips[handle].endedUnstopped {
		t.Fatal("replay left the earlier playback's stop owed")
	}
	info, err := runtime.Services.Media.Info(runtime.ServiceOwner, runtime.wipicMediaServices[handle])
	check(t, err)
	setKTFWIPICCallArguments(t, runtime, []uint32{handle})
	if result, err := ktfWIPICMediaStop(context.Background(), runtime); err != nil || result != 0 {
		t.Fatalf("stop of the replay (state %v) = %d, %v", info.State, int32(result), err)
	}
	info, err = runtime.Services.Media.Info(runtime.ServiceOwner, runtime.wipicMediaServices[handle])
	check(t, err)
	if info.State != 0 {
		t.Fatalf("replayed clip state after stop = %v, want stopped", info.State)
	}
}
