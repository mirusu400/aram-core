package gnex32

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestGNEX32SoundPayload(t *testing.T) {
	wave := gnex32TestWave()
	wrapped := append([]byte{2, 0}, wave...)
	mediaType, payload, err := gnex32SoundPayload(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "audio/wav" || string(payload[:4]) != "RIFF" {
		t.Fatalf("wrapped payload = %q %q", mediaType, payload[:4])
	}

	mediaType, payload, err = gnex32SoundPayload([]byte("MMMDscore"))
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "audio/x-smaf" || string(payload[:4]) != "MMMD" {
		t.Fatalf("direct payload = %q %q", mediaType, payload[:4])
	}

	if _, _, err := gnex32SoundPayload([]byte("NOPE")); !errors.Is(err, shared.ErrMediaUnsupported) {
		t.Fatalf("unsupported payload error = %v", err)
	}
}

func TestGNEX32PlaySoundProducesTimedAudio(t *testing.T) {
	runtime, err := NewRuntime(
		gnex32AudioTestImage([][]byte{gnex32TestWave()}),
		Identity{MIN: "0111234567", UserID: "98B4970F"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.RunMain(3); err != nil {
		t.Fatal(err)
	}
	if runtime.soundClip == 0 {
		t.Fatal("PlaySound did not retain an active clip")
	}
	if err := runtime.StepFrame(1); err != nil {
		t.Fatal(err)
	}
	audio, start, revision := runtime.DrainTimedAudio()
	if audio.SampleRate != 44_100 || audio.Channels != 1 || len(audio.PCM16) == 0 {
		t.Fatalf("audio = rate %d channels %d samples %d", audio.SampleRate, audio.Channels, len(audio.PCM16))
	}
	if start != 0 || revision != 0 {
		t.Fatalf("audio timeline = start %s revision %d", start, revision)
	}
	for _, sample := range audio.PCM16 {
		if sample != 0 {
			return
		}
	}
	t.Fatal("decoded audio is silent")
}

func TestGNEX32StopSoundPreservesRenderedAudioAndStopsFutureMixing(t *testing.T) {
	runtime, err := NewRuntime(
		gnex32AudioTestImage([][]byte{gnex32TestWave()}),
		Identity{MIN: "0111234567", UserID: "98B4970F"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.RunMain(3); err != nil {
		t.Fatal(err)
	}
	if err := runtime.StepFrame(1); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.service(0x83, nil); err != nil {
		t.Fatal(err)
	}
	if runtime.soundClip != 0 {
		t.Fatalf("sound clip survived StopSound: %d", runtime.soundClip)
	}
	if audio, _, _ := runtime.DrainTimedAudio(); len(audio.PCM16) == 0 {
		t.Fatal("StopSound discarded audio rendered before the stop time")
	}
	if err := runtime.StepFrame(1); err != nil {
		t.Fatal(err)
	}
	if audio, _, _ := runtime.DrainTimedAudio(); len(audio.PCM16) != 0 {
		t.Fatalf("StopSound mixed %d future samples", len(audio.PCM16))
	}
}

func TestGNEX32InvalidReplacementKeepsCurrentSound(t *testing.T) {
	runtime, err := NewRuntime(
		gnex32AudioTestImage([][]byte{
			gnex32TestWave(),
			[]byte("NOPE"),
		}),
		Identity{MIN: "0111234567", UserID: "98B4970F"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.playSound(0); err != nil {
		t.Fatal(err)
	}
	clip := runtime.soundClip
	if err := runtime.playSound(1); !errors.Is(err, shared.ErrMediaUnsupported) {
		t.Fatalf("invalid replacement error = %v", err)
	}
	if runtime.soundClip != clip {
		t.Fatalf("invalid replacement changed clip %d to %d", clip, runtime.soundClip)
	}
	info, err := runtime.services.Media.Info(runtime.owner, clip)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != shared.ClipPlaying {
		t.Fatalf("original clip state = %v", info.State)
	}
}

func TestGNEX32RuntimeCloseReleasesSharedResources(t *testing.T) {
	runtime, err := NewRuntime(
		gnex32AudioTestImage([][]byte{gnex32TestWave()}),
		Identity{MIN: "0111234567", UserID: "98B4970F"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.RunMain(3); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.text.EnsureFont(runtime.owner, shared.FontDescriptor{
		Family: "aram-fallback",
		Size:   12,
	}); err != nil {
		t.Fatal(err)
	}
	if got := runtime.services.Registry.Len(); got != 2 {
		t.Fatalf("registry size before close = %d, want sound and font", got)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
	if runtime.soundClip != 0 || runtime.services.Registry.Len() != 0 {
		t.Fatalf("resources survived close: clip=%d registry=%d",
			runtime.soundClip, runtime.services.Registry.Len())
	}
	if _, err := runtime.services.Coordinator.Adapter(runtime.owner); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("coordinator owner survived close: %v", err)
	}
	if audio, _, _ := runtime.DrainTimedAudio(); len(audio.PCM16) != 0 {
		t.Fatalf("close retained %d queued samples", len(audio.PCM16))
	}
}

func gnex32AudioTestImage(media [][]byte) gnex.GNEX32Image {
	code := make([]byte, 10)
	putWord(code, 0, 0x06)
	putWord(code, 2, 0)
	putWord(code, 4, 0x92)
	putWord(code, 6, 0x82)
	putWord(code, 8, 0x86)
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	resources := make([]gnex.GNEX32Media, len(media))
	for index := range media {
		resources[index] = gnex.GNEX32Media{Flags: 0x200, Data: media[index]}
	}
	return gnex.GNEX32Image{
		Header:  gnex.Header{BodyOffset: 160},
		Entry:   160,
		Code:    code,
		Symbols: symbols,
		Media:   resources,
	}
}

func gnex32TestWave() []byte {
	samples := make([]int16, 800)
	for index := range samples {
		samples[index] = 1000
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
