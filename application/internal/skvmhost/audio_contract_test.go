package skvmhost

import (
	"encoding/binary"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestSKVMAudioCarriesTimelineAndStopGeneration(t *testing.T) {
	services, err := shared.NewServices(shared.Config{})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := services.Coordinator.Register("skvm-audio-test", 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	clip, err := services.Media.CreateClip(owner, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := services.Media.Append(owner, clip, skvmHostTestWave()); err != nil {
		t.Fatal(err)
	}
	if err := services.Media.Play(owner, clip, -1); err != nil {
		t.Fatal(err)
	}
	machine := &Machine{
		services:            services,
		owner:               owner,
		audioGeneration:     1,
		mediaOutputRevision: services.Media.OutputRevision(),
	}
	if err := services.Advance(owner, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	first := machine.DrainAudio()
	if len(first.PCM16) == 0 || first.Generation != 1 || first.StartGuestNS < 0 {
		t.Fatalf("first SKVM audio chunk = %+v", first)
	}
	if err := services.Advance(owner, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	second := machine.DrainAudio()
	wantStart := first.StartSample + uint64(len(first.PCM16)/first.Channels)
	if second.Generation != first.Generation || second.StartSample != wantStart {
		t.Fatalf("SKVM timeline discontinuity: first=%+v second=%+v", first, second)
	}
	if err := services.Media.Stop(owner, clip); err != nil {
		t.Fatal(err)
	}
	if stale := machine.DrainAudio(); len(stale.PCM16) != 0 {
		t.Fatalf("SKVM Stop retained %d samples", len(stale.PCM16))
	}
	if machine.audioGeneration == first.Generation {
		t.Fatal("SKVM Stop did not advance the audio generation")
	}
}

func skvmHostTestWave() []byte {
	samples := make([]int16, 800)
	for index := range samples {
		samples[index] = int16(index + 1)
	}
	data := make([]byte, 44+len(samples)*2)
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
	binary.LittleEndian.PutUint32(data[40:44], uint32(len(samples)*2))
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(data[44+index*2:], uint16(sample))
	}
	return data
}
