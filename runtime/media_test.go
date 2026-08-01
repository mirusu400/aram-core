package runtime

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"time"
)

func pcmWave(sampleRate uint32, channels uint16, samples []int16) []byte {
	dataSize := len(samples) * 2
	result := make([]byte, 44+dataSize)
	copy(result[0:4], "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(result)-8))
	copy(result[8:12], "WAVE")
	copy(result[12:16], "fmt ")
	binary.LittleEndian.PutUint32(result[16:20], 16)
	binary.LittleEndian.PutUint16(result[20:22], 1)
	binary.LittleEndian.PutUint16(result[22:24], channels)
	binary.LittleEndian.PutUint32(result[24:28], sampleRate)
	binary.LittleEndian.PutUint32(
		result[28:32],
		sampleRate*uint32(channels)*2,
	)
	binary.LittleEndian.PutUint16(result[32:34], channels*2)
	binary.LittleEndian.PutUint16(result[34:36], 16)
	copy(result[36:40], "data")
	binary.LittleEndian.PutUint32(result[40:44], uint32(dataSize))
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(result[44+index*2:], uint16(sample))
	}
	return result
}

func TestMediaTimelineMixesAndCompletesDeterministically(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	registry := NewRegistry(32)
	media, err := NewMedia(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(16, 32)
	clip, err := media.CreateClip(3, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	source := pcmWave(8_000, 1, []int16{1000, -1000, 2000, -2000})
	if _, err := media.Append(3, clip, source); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(3, clip, 1); err != nil {
		t.Fatal(err)
	}
	if err := media.Advance(0, 500*time.Microsecond, bus); err != nil {
		t.Fatal(err)
	}
	audio := media.Drain()
	if audio.SampleRate != 8_000 || audio.Channels != 1 ||
		!reflect.DeepEqual(audio.PCM16, []int16{1000, -1000, 2000, -2000}) {
		t.Fatalf("mixed audio = %+v", audio)
	}
	event, ok := bus.PopReady(time.Millisecond)
	if !ok || event.Kind != EventAudioComplete ||
		event.ServiceID != clip || event.At != 500*time.Microsecond {
		t.Fatalf("completion event = %+v, %v", event, ok)
	}
	info, err := media.Info(3, clip)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != ClipStopped || info.Position != 500*time.Microsecond {
		t.Fatalf("completed clip = %+v", info)
	}
}

func TestMediaStateRoundTripPreservesQueuedAudio(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	clip, err := services.Media.CreateClip(1, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	source := pcmWave(44_100, 1, make([]int16, 441))
	if _, err := services.Media.Append(1, clip, source); err != nil {
		t.Fatal(err)
	}
	if err := services.Media.Play(1, clip, -1); err != nil {
		t.Fatal(err)
	}
	if err := services.Advance(1, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	state := services.Snapshot()
	clone, err := NewServices(state.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.Restore(state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clone.Media.Snapshot(), services.Media.Snapshot()) {
		t.Fatal("media state did not round-trip")
	}
}

func TestMutedByZeroGainAdvancesWithoutQueuingAudio(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	registry := NewRegistry(4)
	media, err := NewMedia(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	clip, err := media.CreateClip(1, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(
		1,
		clip,
		pcmWave(8_000, 1, []int16{1, 2, 3, 4}),
	); err != nil {
		t.Fatal(err)
	}
	if err := media.SetClipGain(1, clip, 0, false, 0); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(1, clip, 1); err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(4, 32)
	if err := media.Advance(0, 500*time.Microsecond, bus); err != nil {
		t.Fatal(err)
	}
	if audio := media.Drain(); len(audio.PCM16) != 0 {
		t.Fatalf("zero-gain clip queued %d samples", len(audio.PCM16))
	}
	info, err := media.Info(1, clip)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != ClipStopped || info.Position != 500*time.Microsecond {
		t.Fatalf("zero-gain clip timeline = %+v", info)
	}
}

func TestDirectMediaAdvanceQueueFailureIsAtomic(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	registry := NewRegistry(4)
	media, err := NewMedia(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	clip, err := media.CreateClip(1, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(
		1,
		clip,
		pcmWave(8_000, 1, []int16{1, 2, 3, 4}),
	); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(1, clip, 1); err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(1, 32)
	if _, err := bus.Enqueue(Event{Kind: EventApplication}); err != nil {
		t.Fatal(err)
	}
	mediaBefore, busBefore := media.Snapshot(), bus.Snapshot()
	if err := media.Advance(
		0,
		500*time.Microsecond,
		bus,
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Media.Advance with full queue error = %v", err)
	}
	if !reflect.DeepEqual(media.Snapshot(), mediaBefore) ||
		!reflect.DeepEqual(bus.Snapshot(), busBefore) {
		t.Fatal("failed direct media advance was not atomic")
	}
}

func TestMediaRetainsNewestSamplesWhenTheHostNeverDrains(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	limits.MaxQueuedSamples = 8
	registry := NewRegistry(32)
	media, err := NewMedia(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(16, 32)
	clip, err := media.CreateClip(3, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(
		3,
		clip,
		pcmWave(8_000, 1, []int16{2, 4, 6, 8, 10, 12}),
	); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(3, clip, -1); err != nil {
		t.Fatal(err)
	}
	// Six one-sample advances produce twelve samples into an eight-sample
	// window without the host ever draining. The guest must keep running.
	for step := range 6 {
		start := time.Duration(step) * 250 * time.Microsecond
		if err := media.Advance(start, start+250*time.Microsecond, bus); err != nil {
			t.Fatalf("advance %d: %v", step, err)
		}
	}
	audio := media.Drain()
	if !reflect.DeepEqual(audio.PCM16, []int16{10, 12, 2, 4, 6, 8, 10, 12}) {
		t.Fatalf("retained audio = %v", audio.PCM16)
	}
	if media.DroppedSamples() != 4 {
		t.Fatalf("dropped samples = %d, want 4", media.DroppedSamples())
	}
}

func TestMediaAdvanceLongerThanRetentionKeepsOnlyTheTail(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	limits.MaxQueuedSamples = 4
	registry := NewRegistry(32)
	media, err := NewMedia(registry, limits)
	if err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(16, 32)
	clip, err := media.CreateClip(3, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(
		3,
		clip,
		pcmWave(8_000, 1, []int16{2, 4, 6, 8, 10, 12, 14, 16}),
	); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(3, clip, -1); err != nil {
		t.Fatal(err)
	}
	if err := media.Advance(0, time.Millisecond, bus); err != nil {
		t.Fatal(err)
	}
	audio := media.Drain()
	if !reflect.DeepEqual(audio.PCM16, []int16{10, 12, 14, 16}) {
		t.Fatalf("tail audio = %v", audio.PCM16)
	}
	if media.DroppedSamples() != 4 {
		t.Fatalf("dropped samples = %d, want 4", media.DroppedSamples())
	}
}
