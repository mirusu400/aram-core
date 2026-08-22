package runtime

import (
	"encoding/binary"
	"testing"
)

func TestDecodeSMAFMobileScoreProducesStereoPCM(t *testing.T) {
	sequence := []byte{
		0x00, 0xb0, 0x07, 0x7f, // channel volume
		0x00, 0xc0, 0x00, // program 0
		0x00, 0x90, 60, 110, 125, // C4, 500 ms gate
		0x7d, 0x80, 64, 125, // E4 after 500 ms
		0x7d, 0xff, 0x2f, // end after another 500 ms
	}
	trackBody := []byte{0x02, 0x00, 0x02, 0x02}
	trackBody = append(trackBody, make([]byte, 16)...)
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsq"), sequence)
	file := appendSMAFChunk(nil, []byte{'M', 'T', 'R', 0}, trackBody)
	data := make([]byte, 8)
	copy(data, "MMMD")
	binary.BigEndian.PutUint32(data[4:], uint32(len(file)))
	data = append(data, file...)

	decoded := decodeSMAFPCM16(data, 44_100)
	if decoded == nil {
		t.Fatal("decodeSMAFPCM16 returned nil")
	}
	if decoded.channels != 2 || decoded.sampleRate != 44_100 {
		t.Fatalf(
			"format = %d ch at %d Hz",
			decoded.channels,
			decoded.sampleRate,
		)
	}
	if decoded.duration < 900_000_000 {
		t.Fatalf("duration = %s, want at least 900ms", decoded.duration)
	}
	var peak int16
	for _, sample := range decoded.samples {
		if sample < 0 {
			sample = -sample
		}
		if sample > peak {
			peak = sample
		}
	}
	if peak < 100 {
		t.Fatalf("PCM peak = %d, want audible output", peak)
	}
}

func TestMediaDecodesSMAFEagerlyOnPlay(t *testing.T) {
	registry := NewRegistry(32)
	const owner OwnerID = 3
	media, err := NewMedia(registry, DefaultMediaLimits())
	if err != nil {
		t.Fatal(err)
	}
	clip, err := media.CreateClip(owner, "audio/mmf", 0)
	if err != nil {
		t.Fatal(err)
	}
	sequence := []byte{0, 0x90, 60, 100, 25, 25, 0xff, 0x2f}
	trackBody := []byte{2, 0, 2, 2}
	trackBody = append(trackBody, make([]byte, 16)...)
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsq"), sequence)
	payload := appendSMAFChunk(nil, []byte{'M', 'T', 'R', 0}, trackBody)
	data := make([]byte, 8)
	copy(data, "MMMD")
	binary.BigEndian.PutUint32(data[4:], uint32(len(payload)))
	data = append(data, payload...)
	if _, err := media.Append(owner, clip, data); err != nil {
		t.Fatal(err)
	}
	before, err := media.Info(owner, clip)
	if err != nil {
		t.Fatal(err)
	}
	if before.Decoded {
		t.Fatal("SMAF decoded during append; want decode on play, not append")
	}
	if err := media.Play(owner, clip, 1); err != nil {
		t.Fatal(err)
	}
	after, err := media.Info(owner, clip)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Decoded || after.Duration <= 0 {
		t.Fatalf("decoded = %t, duration = %s", after.Decoded, after.Duration)
	}
	internal := media.clips[clip]
	if len(internal.decoded.samples) == 0 || internal.decoded.smaf != nil {
		t.Fatalf(
			"eager SMAF has %d samples and stream=%v; want fully rendered PCM, no stream",
			len(internal.decoded.samples),
			internal.decoded.smaf != nil,
		)
	}
	bus := NewEventBus(16, 32)
	if err := media.Advance(0, 20_000_000, bus); err != nil {
		t.Fatal(err)
	}
	audio := media.Drain()
	if len(audio.PCM16) != 44_100*2*20/1000 {
		t.Fatalf("drained samples = %d", len(audio.PCM16))
	}
	var peak int16
	for _, sample := range audio.PCM16 {
		if sample < 0 {
			sample = -sample
		}
		if sample > peak {
			peak = sample
		}
	}
	if peak < 100 {
		t.Fatalf("eager mixed PCM peak = %d, want audible output", peak)
	}
}

func appendSMAFChunk(destination, id, body []byte) []byte {
	destination = append(destination, id...)
	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, uint32(len(body)))
	destination = append(destination, size...)
	return append(destination, body...)
}
