package ktf

import (
	"context"
	"testing"
)

// newKTFTestMediaClip creates one MA-3 clip and returns its handle.
func newKTFTestMediaClip(
	t *testing.T,
	runtime *Runtime,
	capacity uint32,
) uint32 {
	t.Helper()
	mediaType, err := runtime.allocateBytes([]byte("Yamaha_MA3"), true)
	if err != nil {
		t.Fatal(err)
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{mediaType, capacity, 0})
	handle, err := ktfWIPICMediaCreate(context.Background(), runtime)
	if err != nil || handle == 0 {
		t.Fatalf("media create handle=%08x err=%v", handle, err)
	}
	return handle
}

func putKTFTestMediaData(
	t *testing.T,
	runtime *Runtime,
	handle uint32,
	payload []byte,
) uint32 {
	t.Helper()
	input, err := runtime.allocateBytes(payload, false)
	if err != nil {
		t.Fatal(err)
	}
	setKTFWIPICCallArguments(t, runtime,
		[]uint32{handle, input, uint32(len(payload))})
	result, err := ktfWIPICMediaPutData(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// 에픽크로니클2 reuses one clip for every sound it plays and never calls
// MC_mdaClipClearData: it stops the current track and writes the whole next
// one. Appending there kept the finished track in front of the new one, so
// the battle music never replaced the field music and the effects that
// followed were never heard (issue #85).
func TestKTFWIPICMediaPutDataReplacesAFinishedSound(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	handle := newKTFTestMediaClip(t, runtime, 4096)
	clip := runtime.wipicMediaClips[handle]

	field := []byte("MMMD-field-track")
	if got := putKTFTestMediaData(t, runtime, handle, field); got != uint32(len(field)) {
		t.Fatalf("first put accepted %d bytes, want %d", int32(got), len(field))
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{handle, 1})
	if _, err := ktfWIPICMediaPlay(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{handle})
	if _, err := ktfWIPICMediaStop(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}

	battle := []byte("MMMD-battle")
	if got := putKTFTestMediaData(t, runtime, handle, battle); got != uint32(len(battle)) {
		t.Fatalf("second put accepted %d bytes, want %d", int32(got), len(battle))
	}
	if len(clip.data) != len(battle) || string(clip.data) != string(battle) {
		t.Fatalf("clip holds %q, want only the new sound", clip.data)
	}
	stored, err := runtime.Services.Media.Source(
		runtime.ServiceOwner,
		runtime.wipicMediaServices[handle],
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(battle) {
		t.Fatalf("media service holds %q, want only the new sound", stored)
	}
}

// A title that loads one sound in several pieces before starting it still
// gets the whole sound: only playback ending opens a new one.
func TestKTFWIPICMediaPutDataStillAppendsBeforeAndDuringPlayback(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	handle := newKTFTestMediaClip(t, runtime, 4096)
	clip := runtime.wipicMediaClips[handle]

	putKTFTestMediaData(t, runtime, handle, []byte("MMMD-head"))
	putKTFTestMediaData(t, runtime, handle, []byte("-tail"))
	if string(clip.data) != "MMMD-head-tail" {
		t.Fatalf("chunked load produced %q", clip.data)
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{handle, 0})
	if _, err := ktfWIPICMediaPlay(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	// Streaming more of the same sound while it plays must keep appending.
	putKTFTestMediaData(t, runtime, handle, []byte("-more"))
	if string(clip.data) != "MMMD-head-tail-more" {
		t.Fatalf("streaming append produced %q", clip.data)
	}
}

// MC_mdaClipClearData empties the clip outright, so the write that follows it
// must not drop anything else.
func TestKTFWIPICMediaClearDataCancelsAPendingRewind(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	handle := newKTFTestMediaClip(t, runtime, 4096)
	clip := runtime.wipicMediaClips[handle]

	putKTFTestMediaData(t, runtime, handle, []byte("MMMD-old"))
	setKTFWIPICCallArguments(t, runtime, []uint32{handle})
	if _, err := ktfWIPICMediaStop(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	setKTFWIPICCallArguments(t, runtime, []uint32{handle})
	if _, err := ktfWIPICMediaClearData(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if clip.rewindPending {
		t.Fatal("an explicit clear left a rewind pending")
	}
	putKTFTestMediaData(t, runtime, handle, []byte("MMMD-a"))
	putKTFTestMediaData(t, runtime, handle, []byte("-b"))
	if string(clip.data) != "MMMD-a-b" {
		t.Fatalf("after an explicit clear the clip holds %q", clip.data)
	}
}

// Replaying what is already buffered stays possible: stopping and starting
// again without writing anything keeps the sound.
func TestKTFWIPICMediaStopKeepsTheSoundForAReplay(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	handle := newKTFTestMediaClip(t, runtime, 4096)
	clip := runtime.wipicMediaClips[handle]

	putKTFTestMediaData(t, runtime, handle, []byte("MMMD-effect"))
	for range 2 {
		setKTFWIPICCallArguments(t, runtime, []uint32{handle, 0})
		if _, err := ktfWIPICMediaPlay(context.Background(), runtime); err != nil {
			t.Fatal(err)
		}
		if string(clip.data) != "MMMD-effect" {
			t.Fatalf("replay lost the sound: %q", clip.data)
		}
		setKTFWIPICCallArguments(t, runtime, []uint32{handle})
		if _, err := ktfWIPICMediaStop(context.Background(), runtime); err != nil {
			t.Fatal(err)
		}
	}
}
