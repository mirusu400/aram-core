package wipi

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

func TestRaptorFreeStopsPreservedLoop(t *testing.T) {
	runtime := newPublicRuntime(t)
	runtime.Services.Media.SetStoppedLoopPreservation(true)
	handle, err := runtime.RaptorCreateClip("audio/wav", 128, 0)
	if err != nil || handle == 0 {
		t.Fatalf("create: handle=%d err=%v", handle, err)
	}
	wave := wipiTestWave([]int16{1, 2})
	address, err := runtime.Heap.Allocate(uint32(len(wave)), true)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(address, wave))
	if !runtime.RaptorPutClipData(handle, address, int32(len(wave))) {
		t.Fatal("put failed")
	}
	if !runtime.RaptorPlayClip(handle, true) {
		t.Fatal("play failed")
	}
	runtime.RaptorStopClip(handle, true)
	if runtime.Services.Media.MusicVoiceActive() {
		t.Fatal("freed loop kept sounding")
	}
}
func TestRaptorFreeEffectKeepsPreservedLoop(t *testing.T) {
	runtime := newPublicRuntime(t)
	runtime.Services.Media.SetStoppedLoopPreservation(true)
	handle, err := runtime.RaptorCreateClip("audio/wav", 128, 0)
	if err != nil || handle == 0 {
		t.Fatalf("create: handle=%d err=%v", handle, err)
	}
	wave := wipiTestWave([]int16{1, 2})
	address, err := runtime.Heap.Allocate(uint32(len(wave)), true)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(address, wave))
	if !runtime.RaptorPutClipData(handle, address, int32(len(wave))) {
		t.Fatal("put loop failed")
	}
	if !runtime.RaptorPlayClip(handle, true) {
		t.Fatal("play loop failed")
	}
	runtime.RaptorStopClip(handle, false)
	if !runtime.Services.Media.MusicVoiceActive() {
		t.Fatal("loop not preserved for effect")
	}
	if !runtime.RaptorClearClipData(handle) {
		t.Fatal("clear failed")
	}
	if !runtime.RaptorPutClipData(handle, address, int32(len(wave))) {
		t.Fatal("put effect failed")
	}
	if !runtime.RaptorPlayClip(handle, false) {
		t.Fatal("play effect failed")
	}
	runtime.RaptorStopClip(handle, true)
	if !runtime.Services.Media.MusicVoiceActive() {
		t.Fatal("freeing effect dropped background music")
	}
}

func TestRaptorStoppedLoopSurvivesWIPIStateRestore(t *testing.T) {
	runtime := newPublicRuntime(t)
	runtime.Services.Media.SetStoppedLoopPreservation(true)
	handle, err := runtime.RaptorCreateClip("audio/wav", 128, 0)
	check(t, err)
	wave := wipiTestWave([]int16{1, 2})
	address, err := runtime.Heap.Allocate(uint32(len(wave)), true)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(address, wave))
	if !runtime.RaptorPutClipData(handle, address, int32(len(wave))) ||
		!runtime.RaptorPlayClip(handle, true) {
		t.Fatal("could not start background loop")
	}
	runtime.RaptorStopClip(handle, false)
	if !runtime.Services.Media.MusicVoiceActive() {
		t.Fatal("test setup did not preserve the background loop")
	}

	var buffer bytes.Buffer
	check(t, WriteState(runtime, runtime.CPU, guest.NewStateWriter(&buffer)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	check(t, err)
	if !saved.validatedServices.Media.MusicVoiceActive() {
		t.Fatal("state validation dropped the background loop")
	}
	check(t, runtime.RestoreState(saved))
	if !runtime.Services.Media.MusicVoiceActive() {
		t.Fatal("state restore dropped the background loop")
	}
	saved.validatedServices = nil
	check(t, runtime.RestoreState(saved))
	if !runtime.Services.Media.MusicVoiceActive() {
		t.Fatal("direct state restore dropped the background loop")
	}
}
