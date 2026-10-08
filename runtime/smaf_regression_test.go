package runtime

import (
	"bytes"
	"math"
	"strconv"
	"testing"
	"time"
)

func smafRegressionToneWindow(t *testing.T, pcm *decodedPCM, begin time.Duration, channel int, frequency float64) float64 {
	t.Helper()
	start := int(uint64(begin) * uint64(pcm.sampleRate) / uint64(time.Second))
	window := make([]float64, 2205)
	if len(pcm.samples)/2 < start+len(window) {
		t.Fatalf("missing audio window at %s: duration=%s", begin, pcm.duration)
	}
	for frame := range window {
		window[frame] = float64(pcm.samples[(start+frame)*2+channel]) / 32768
	}
	return toneMagnitude(window, float64(pcm.sampleRate), frequency)
}

func TestSMAFRegressionLongSampleIsNotClipped(t *testing.T) {
	voice := smafTestPCMVoice(1, 23999, 23999)
	voice[14] = 0x28 // SR=2 is valid for XOF; ignore the 20 ms key-off.
	file := smafStreamTestFile(smafTestSampledTrack(0, voice,
		[]byte{0, 0x90, 60, 127, 5}, smafTestPCM16Wave(24000, 1)))
	pcm := decodeSMAFPCM16(file, 44100)
	if pcm == nil {
		t.Fatal("valid one-shot sampled voice failed to decode")
	}
	if pcm.duration < 3*time.Second {
		t.Fatalf("3 s one-shot was truncated at %s while its waveform and envelope still play", pcm.duration)
	}
	if tail := smafRegressionToneWindow(t, pcm, 2500*time.Millisecond, 0, 1000); tail < 0.01 {
		t.Fatalf("one-shot lost its 1 kHz tail at 2.5 s: %.6f", tail)
	}
}

func TestSMAFRegressionInlineVoiceReplacement(t *testing.T) {
	for _, format := range []byte{0, 2} {
		name := "mobile"
		if format == 0 {
			name = "handy-phone"
		}
		t.Run(name, func(t *testing.T) {
			initial := smafTestPCMVoice(1, 7999, 7999)
			replacement := smafTestPCMVoice(1, 7999, 7999)
			replacement[10], replacement[11] = 0x3e, 0x80 // 16 kHz at C4, same key.
			var sequence []byte
			if format == 0 {
				sequence = []byte{0, 0x20, 40, 50, 0xff, 0xf0, byte(len(replacement) + 1)}
			} else {
				sequence = []byte{0, 0x90, 60, 127, 40, 50, 0xf0, byte(len(replacement) + 1)}
			}
			sequence = append(sequence, replacement...)
			sequence = append(sequence, 0xf7)
			if format == 0 {
				sequence = append(sequence, 0, 0x20, 40)
			} else {
				sequence = append(sequence, 0, 0x90, 60, 127, 40)
			}
			d := &smafDecoder{rate: 44100, tracks: []smafTrack{{
				format: format, durationBase: 2, gateBase: 2,
				setup:    append(append([]byte{0xf0, byte(len(initial) + 1)}, initial...), 0xf7),
				sequence: sequence,
				waves:    []smafWave{{number: 1, sampleRate: 8000, channels: 1, pcm: decodeSMAFStreamWave(1, append([]byte{3, 0x1f, 0x40}, smafTestPCM16(8000, 1)...)).pcm}},
			}}}
			if !d.buildEvents() {
				t.Fatal("valid inline voice score failed to parse")
			}
			stream := newSMAFRenderStream(d)
			pcm := &decodedPCM{sampleRate: 44100, channels: 2, samples: stream.renderUntil(nil, stream.end)}
			first := smafRegressionToneWindow(t, pcm, 20*time.Millisecond, 0, 1000)
			oldTone := smafRegressionToneWindow(t, pcm, 220*time.Millisecond, 0, 1000)
			newTone := smafRegressionToneWindow(t, pcm, 220*time.Millisecond, 0, 2000)
			t.Logf("first_1k=%.6f after_old_1k=%.6f after_new_2k=%.6f parsed_voices=%d", first, oldTone, newTone, len(d.voices))
			if first < 0.03 {
				t.Fatal("initial voice did not play the expected sample")
			}
			if newTone < 0.03 || oldTone > newTone/10 {
				t.Fatal("note after the exclusive still uses the obsolete instrument")
			}
		})
	}
}

func TestSMAFRegressionFutureVoiceDoesNotChangeEarlierNotes(t *testing.T) {
	voice := smafTestPCMVoice(1, 7999, 7999)
	sequence := []byte{0, 0x90, 60, 127, 40, 50, 0xf0, byte(len(voice) + 1)}
	sequence = append(sequence, voice...)
	sequence = append(sequence, 0xf7, 0, 0x90, 60, 127, 40)
	file := smafStreamTestFile(smafStreamTestTrackWithSequence(0, sequence, smafTestPCM16Wave(8000, 1)))
	d := &smafDecoder{rate: 44100}
	if !d.parse(file) || !d.buildEvents() {
		t.Fatal("future voice fixture failed to parse")
	}
	stream := newSMAFRenderStream(d)
	stream.renderUntil(nil, 4410) // First note, before the 200 ms voice definition.
	pcmActive, fmActive := 0, 0
	for _, voice := range d.pcmPool {
		if voice.active {
			pcmActive++
		}
	}
	for _, voice := range d.pool {
		if voice.active {
			fmActive++
		}
	}
	t.Logf("before_200ms: PCM=%d FM=%d", pcmActive, fmActive)
	if pcmActive != 0 || fmActive == 0 {
		t.Fatal("an earlier note consumed a sampled instrument defined only later in the score")
	}
}

func TestSMAFRegressionMA3PCMParametersMatchUnpackedMA5(t *testing.T) {
	plain := smafTestPCMVoice(1, 64, 799)
	packed := append([]byte(nil), plain[:10]...)
	packed[2] = 6
	// MA-3 stores groups of seven parameter bytes behind their MSB flags.
	// The 16 used PCM parameters become 19 packed bytes; MA-5 stores them raw.
	parameters := plain[10:26]
	for offset := 0; offset < len(parameters); offset += 7 {
		block := parameters[offset:min(offset+7, len(parameters))]
		flags := byte(0)
		for index, value := range block {
			flags |= (value >> 7) << (6 - index)
		}
		packed = append(packed, flags)
		for _, value := range block {
			packed = append(packed, value&0x7f)
		}
	}
	decoded := parseSMAFVoice(packed)
	if !decoded.valid || decoded.pcm == nil {
		t.Fatal("MA-3 sampled voice was discarded")
	}
	t.Logf("packed MA3: Fs=%d LP=%d EP=%d WaveID=%d RAM=%t", decoded.pcm.sampleRate, decoded.pcm.loopPoint, decoded.pcm.endPoint, decoded.pcm.waveID, decoded.pcm.ram)
	if decoded.pcm.sampleRate != 8000 || decoded.pcm.loopPoint != 64 || decoded.pcm.endPoint != 799 || decoded.pcm.waveID != 1 || !decoded.pcm.ram {
		t.Fatal("equivalent MA-3 and MA-5 sampled voices decode to different parameters")
	}
}

func TestSMAFRegressionRAMAndROMSelectCorrectMemory(t *testing.T) {
	for _, ram := range []bool{true, false} {
		name := "ROM"
		if ram {
			name = "RAM"
		}
		t.Run(name, func(t *testing.T) {
			voice := smafTestPCMVoice(1, 7999, 7999)
			// RM=0 selects uploaded RAM; RM=1 selects the handset's preset ROM.
			if ram {
				voice[25] &= 0x7f
			} else {
				voice[25] |= 0x80
			}
			pcm := decodeSMAFPCM16(smafStreamTestFile(smafTestSampledTrack(0, voice,
				[]byte{0, 0x90, 60, 127, 125}, smafTestPCM16Wave(8000, 1))), 44100)
			if pcm == nil {
				t.Fatal("memory selection fixture did not decode")
			}
			magnitude := smafRegressionToneWindow(t, pcm, 20*time.Millisecond, 0, 1000)
			t.Logf("selected_memory=%s tone_1k=%.6f", name, magnitude)
			if ram && magnitude < 0.03 {
				t.Fatal("uploaded RAM wave was ignored")
			}
			if !ram && magnitude > 0.0001 {
				t.Fatal("a ROM voice consumed an unrelated RAM wave")
			}
		})
	}
}

func TestSMAFRegressionATRVolumeDoesNotMuteAnotherTrack(t *testing.T) {
	first := smafTestATR(0, []byte{0, 0, 1, 0x30, 2, 2},
		[]byte{0, 1, 125, 25, 0, 0x37, 0}, smafTestPCM16(8000, 1))
	second := smafTestATR(1, []byte{0, 0, 1, 0x30, 2, 2},
		[]byte{0, 1, 125}, smafTestPCM16(8000, 1))
	combined := decodeSMAFPCM16(smafStreamTestFile(first, second), 44100)
	control := decodeSMAFPCM16(smafStreamTestFile(second), 44100)
	if combined == nil || control == nil {
		t.Fatal("valid independent audio tracks failed to decode")
	}
	got := smafRegressionToneWindow(t, combined, 200*time.Millisecond, 0, 1000)
	want := smafRegressionToneWindow(t, control, 200*time.Millisecond, 0, 1000)
	t.Logf("other_track_1k_after_track0_mute=%.6f control=%.6f", got, want)
	if want < 0.03 {
		t.Fatal("control track was already silent")
	}
	if got < want*0.8 {
		t.Fatal("muting ATR0 also muted the independently addressed ATR1")
	}
}

func TestSMAFRegressionATRCanSelectAllDocumentedWaveIDs(t *testing.T) {
	for _, id := range []byte{1, 15, 16, 17, 31, 32, 47, 48, 62} {
		t.Run(strconv.Itoa(int(id)), func(t *testing.T) {
			track := smafTestATR(0, []byte{0, 0, 1, 0x30, 2, 2},
				[]byte{0, id, 125}, smafTestPCM16(8000, 1))
			// Change the Awa ID in the synthetic fixture without changing the samples.
			waveChunk := bytes.Index(track, []byte{'A', 'w', 'a', 1})
			if waveChunk < 0 {
				t.Fatal("fixture has no Awa chunk")
			}
			track[waveChunk+3] = id
			pcm := decodeSMAFPCM16(smafStreamTestFile(track), 44100)
			if pcm == nil {
				t.Fatal("documented ATR wave failed to decode")
			}
			magnitude := smafRegressionToneWindow(t, pcm, 20*time.Millisecond, 0, 1000)
			t.Logf("wave_ID=%d tone_1k=%.6f", id, magnitude)
			if magnitude < 0.03 {
				t.Fatalf("valid wave ID %d is silent", id)
			}
		})
	}
}

func TestSMAFRegressionATRChannelsDoNotOverlapScoreChannels(t *testing.T) {
	mutedScore := smafStreamTestTrackWithSequence(7,
		[]byte{0, 0xb8, 7, 0, 0, 0x98, 60, 127, 125}, nil)
	for _, number := range []byte{0, 255} {
		t.Run(strconv.Itoa(int(number)), func(t *testing.T) {
			audio := smafTestATR(number, []byte{0, 0, 1, 0x30, 2, 2},
				[]byte{0, 1, 125}, smafTestPCM16(8000, 1))
			pcm := decodeSMAFPCM16(smafStreamTestFile(audio, mutedScore), 44100)
			if pcm == nil || smafRegressionToneWindow(t, pcm, 20*time.Millisecond, 0, 1000) < 0.03 {
				t.Fatal("muting score channel 120 also muted the independently addressed ATR track")
			}
		})
	}
}

func smafRegressionChannelRMS(t *testing.T, pcm *decodedPCM, begin time.Duration, channel int) float64 {
	t.Helper()
	start := int(uint64(begin) * uint64(pcm.sampleRate) / uint64(time.Second))
	if len(pcm.samples)/2 < start+2205 {
		t.Fatal("missing RMS window")
	}
	sum := 0.0
	for frame := range 2205 {
		value := float64(pcm.samples[(start+frame)*2+channel]) / 32768
		sum += value * value
	}
	return math.Sqrt(sum / 2205)
}

func TestSMAFRegressionPanUpdatesSoundingFMNote(t *testing.T) {
	sequence := []byte{0, 0xb0, 0x0a, 0, 0, 0x90, 60, 127, 125, 25, 0xb0, 0x0a, 127}
	pcm := decodeSMAFPCM16(smafScore(sequence), 44100)
	if pcm == nil {
		t.Fatal("FM pan fixture failed to decode")
	}
	left := smafRegressionChannelRMS(t, pcm, 200*time.Millisecond, 0)
	right := smafRegressionChannelRMS(t, pcm, 200*time.Millisecond, 1)
	t.Logf("after_left_to_right_pan: left_RMS=%.6f right_RMS=%.6f", left, right)
	if left < 0.001 && right < 0.001 {
		t.Fatal("FM fixture was already silent")
	}
	if right < left*20 {
		t.Fatal("a sounding FM note ignores the pan change")
	}
}
