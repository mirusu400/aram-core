package runtime

import (
	"bytes"
	"slices"
	"testing"
	"time"
)

func smafTestMA3Packed(data []byte) []byte {
	var packed []byte
	for offset := 0; offset < len(data); offset += 7 {
		block := data[offset:min(offset+7, len(data))]
		flags := byte(0)
		for index, value := range block {
			flags |= (value >> 7) << (6 - index)
		}
		packed = append(packed, flags)
		for _, value := range block {
			packed = append(packed, value&0x7f)
		}
	}
	return packed
}

func smafTestExclusive(payload []byte) []byte {
	result := append([]byte{0xf0}, smfVLQ(len(payload)+1)...)
	result = append(result, payload...)
	return append(result, 0xf7)
}

func smafTestUpload(device, id, format byte, data []byte) []byte {
	if device == 6 {
		data = smafTestMA3Packed(data)
	}
	return append([]byte{0x43, 0x79, device, 0x7f, 3, id, format}, data...)
}

func smafTestUploadTrack(setup, sequence, streamWaves []byte) []byte {
	body := append([]byte{2, 0, 2, 2}, make([]byte, 16)...)
	body = appendSMAFChunk(body, []byte("Mtsu"), setup)
	body = appendSMAFChunk(body, []byte("Mtsq"), sequence)
	body = appendSMAFChunk(body, []byte("Mtsp"), streamWaves)
	return appendSMAFChunk(nil, []byte{'M', 'T', 'R', 0}, body)
}

func TestSMAFUploadedWavePlaysThroughSampledInstrument(t *testing.T) {
	for _, device := range []byte{6, 7} {
		for _, format := range []byte{0, 1} {
			data := bytes.Repeat([]byte{0x77, 0x77, 0xff, 0xff}, 256)
			frames := len(data) * 2
			if format == 1 {
				data = bytes.Repeat([]byte{0, 64, 90, 64, 0, 192, 166, 192}, 256)
				frames = len(data)
			}
			voice := smafTestPCMVoice(58, frames-1, frames-1)
			voice[25] &^= 0x80 // uploaded RAM
			if device == 6 {
				voice = append(voice[:10:10], smafTestMA3Packed(voice[10:26])...)
				voice[2] = 6
			}
			setup := smafTestExclusive(smafTestUpload(device, 58, format, data))
			setup = append(setup, smafTestExclusive(voice)...)
			file := smafStreamTestFile(smafTestUploadTrack(setup, []byte{0, 0x90, 60, 127, 125}, nil))
			pcm := decodeSMAFPCM16(file, 44100)
			if pcm == nil || smafRegressionToneWindow(t, pcm, 20*time.Millisecond, 0, 1000) < 0.01 {
				t.Fatalf("device=%d format=%d: uploaded sampled instrument is silent", device, format)
			}
		}
	}
}

func TestSMAFInlineUploadAffectsOnlyLaterNotes(t *testing.T) {
	voice := smafTestPCMVoice(58, 2047, 2047)
	voice[25] &^= 0x80
	setup := smafTestExclusive(voice)
	upload := smafTestExclusive(smafTestUpload(6, 58, 0, bytes.Repeat([]byte{0x77, 0x77, 0xff, 0xff}, 256)))
	sequence := []byte{0, 0x90, 60, 127, 40, 50}
	sequence = append(sequence, upload...)
	sequence = append(sequence, 0, 0x90, 60, 127, 40)
	pcm := decodeSMAFPCM16(smafStreamTestFile(smafTestUploadTrack(setup, sequence, nil)), 44100)
	if pcm == nil {
		t.Fatal("inline upload score failed to decode")
	}
	if early := smafRegressionToneWindow(t, pcm, 20*time.Millisecond, 0, 1000); early > 0.0001 {
		t.Fatalf("a future upload changed the earlier missing sample: %.6f", early)
	}
	if later := smafRegressionToneWindow(t, pcm, 220*time.Millisecond, 0, 1000); later < 0.01 {
		t.Fatalf("inline uploaded wave was not used by the later note: %.6f", later)
	}
}

func TestSMAFUploadedBankIsSeparateFromStreamBank(t *testing.T) {
	data := bytes.Repeat([]byte{0x77, 0x77, 0xff, 0xff}, 256)
	setup := smafTestExclusive(smafTestUpload(6, 1, 0, data))
	voice := smafTestPCMVoice(1, 2047, 2047)
	voice[25] &^= 0x80
	setup = append(setup, smafTestExclusive(voice)...)
	sequence := []byte{
		0, 0xb0, 0, 125, 0, 0xb0, 0x0a, 0, 0, 0x90, 0, 127, 125,
		0, 0xb1, 0x0a, 127, 0, 0x91, 60, 127, 125,
	}
	streamWave := smafTestPCM16Wave(8000, 1)
	// A stereo test generator's right channel gives a distinct 2 kHz source.
	stereo := smafTestPCM16(8000, 2)
	for frame := range 8000 {
		copy(streamWave[11+frame*2:], stereo[frame*4+2:frame*4+4])
	}
	pcm := decodeSMAFPCM16(smafStreamTestFile(smafTestUploadTrack(setup, sequence, streamWave)), 44100)
	if pcm == nil || smafRegressionToneWindow(t, pcm, 20*time.Millisecond, 0, 2000) < 0.03 ||
		smafRegressionToneWindow(t, pcm, 20*time.Millisecond, 1, 1000) < 0.01 {
		t.Fatal("uploaded instrument and stream wave with the same ID replaced each other")
	}
}

func TestSMAFSameFrameVoiceChangeKeepsSequenceOrder(t *testing.T) {
	initial := smafTestPCMVoice(1, 7999, 7999)
	replacement := smafTestPCMVoice(1, 7999, 7999)
	replacement[10], replacement[11] = 0x3e, 0x80
	sequence := []byte{0, 0x90, 60, 127, 40, 0}
	sequence = append(sequence, smafTestExclusive(replacement)...)
	sequence = append(sequence, 0, 0x90, 60, 127, 40)
	file := smafStreamTestFile(smafTestSampledTrack(0, initial, sequence, smafTestPCM16Wave(8000, 1)))
	pcm := decodeSMAFPCM16(file, 44100)
	if pcm == nil || smafRegressionToneWindow(t, pcm, 20*time.Millisecond, 0, 1000) < 0.03 ||
		smafRegressionToneWindow(t, pcm, 20*time.Millisecond, 0, 2000) < 0.03 {
		t.Fatal("a same-frame voice change must leave the preceding note on its original instrument")
	}
}

func TestSMAFSampledLongTailLazyLengthAndSamplesMatch(t *testing.T) {
	voice := smafTestPCMVoice(1, 23999, 23999)
	voice[14] = 0x28
	file := smafStreamTestFile(smafTestSampledTrack(0, voice, []byte{0, 0x90, 60, 127, 5}, smafTestPCM16Wave(24000, 1)))
	eager, lazy := decodeSMAFPCM16(file, 44100), decodeSMAFLazyPCM16(file, 44100)
	if eager == nil || lazy == nil || eager.duration < 3*time.Second || eager.duration != lazy.duration {
		t.Fatal("long sampled tail did not retain its natural eager/incremental length")
	}
	lazy.ensureFrame(uint64(len(eager.samples)/2 - 1))
	if !slices.Equal(eager.samples, lazy.samples) {
		t.Fatal("incremental playback truncates or changes the long sampled tail")
	}
}

func TestSMAFMA3PackingAndUploadBounds(t *testing.T) {
	raw := []byte{0, 0x80, 0xff, 0x7f, 0x81, 1, 2, 0x83, 4}
	if decoded := unpackSMAFMA3Bytes(smafTestMA3Packed(raw)); !slices.Equal(decoded, raw) {
		t.Fatal("MA-3 MSB restoration changed parameter/sample bytes")
	}
	for _, malformed := range [][]byte{nil, {0}, {0x80, 0}, {0, 0x80}, {1, 0}, {0, 1, 2, 3, 4, 5, 6, 7, 0}} {
		if decoded := unpackSMAFMA3Bytes(malformed); decoded != nil {
			t.Fatalf("malformed MA-3 packing accepted: %v", malformed)
		}
	}
	valid := smafTestUpload(6, 58, 0, []byte{0x77, 0xff})
	for _, field := range []int{0, 2, 4, 5, 6} {
		malformed := append([]byte(nil), valid...)
		malformed[field] = 0x80
		if wave := parseSMAFSampledWave(malformed); len(wave.pcm) != 0 {
			t.Fatalf("invalid upload header field %d produced a wave", field)
		}
	}
	for _, malformed := range [][]byte{valid[:7], append(valid[:7:7], 0), append(valid[:7:7], 0, 0xff)} {
		if wave := parseSMAFSampledWave(malformed); len(wave.pcm) != 0 {
			t.Fatal("empty, incomplete, or non-packed upload was accepted")
		}
	}
	voice := smafTestPCMVoice(1, 0, 799)
	packed := append(voice[:10:10], smafTestMA3Packed(voice[10:26])...)
	packed[2] = 6
	if parsed := parseSMAFVoice(packed[:len(packed)-1]); parsed.valid {
		t.Fatal("truncated packed PCM parameters were accepted")
	}
}
