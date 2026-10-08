package runtime

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
	"testing"
	"time"
)

func TestSMAFStreamWaveSampleEncodings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format byte
		data   []byte
		want   []int16
	}{
		{"signed-PCM8", 0x01, []byte{0x80, 0xff, 0, 0x7f}, []int16{-32768, -256, 0, 32512}},
		{"signed-PCM16-BE", 0x03, []byte{0x80, 0, 0xff, 0, 0, 0, 0x7f, 0xff}, []int16{-32768, -256, 0, 32767}},
		{"offset-PCM8", 0x11, []byte{0, 0x7f, 0x80, 0xff}, []int16{-32768, -256, 0, 32512}},
		{"offset-PCM16-BE", 0x13, []byte{0, 0, 0x7f, 0, 0x80, 0, 0xff, 0xff}, []int16{-32768, -256, 0, 32767}},
		{"stereo-signed-PCM8", 0x81, []byte{0x80, 0x7f, 0xff, 0}, []int16{-32768, 32512, -256, 0}},
		{"stereo-signed-PCM16-BE", 0x83, []byte{0x80, 0, 0x7f, 0xff, 0xff, 0, 0, 0}, []int16{-32768, 32767, -256, 0}},
		{"stereo-offset-PCM8", 0x91, []byte{0, 0xff, 0x7f, 0x80}, []int16{-32768, 32512, -256, 0}},
		{"stereo-offset-PCM16-BE", 0x93, []byte{0, 0, 0xff, 0xff, 0x7f, 0, 0x80, 0}, []int16{-32768, 32767, -256, 0}},
		{"stereo-ADPCM4", 0xa0, []byte{0xf7, 0xf7}, []int16{236, -236, 806, -806}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wave := decodeSMAFStreamWave(1, append([]byte{tc.format, 0x1f, 0x40}, tc.data...))
			if wave.sampleRate != 8000 || !slices.Equal(wave.pcm, tc.want) {
				t.Fatalf("decoded wave at %d Hz = %v, want 8000 Hz and %v", wave.sampleRate, wave.pcm, tc.want)
			}
		})
	}
}

func smafTestPCM16(frames, channels int) []byte {
	data := make([]byte, frames*channels*2)
	for frame := range frames {
		for channel := range channels {
			sample := int16(18000 * math.Sin(2*math.Pi*float64(1000*(channel+1)*frame)/8000))
			binary.BigEndian.PutUint16(data[(frame*channels+channel)*2:], uint16(sample))
		}
	}
	return data
}

func smafTestSampledTrack(number byte, voice, sequence, waves []byte) []byte {
	body := append([]byte{2, 0, 2, 2}, make([]byte, 16)...)
	setup := append([]byte{0xf0, byte(len(voice) + 1)}, voice...)
	setup = append(setup, 0xf7)
	body = appendSMAFChunk(body, []byte("Mtsu"), setup)
	body = appendSMAFChunk(body, []byte("Mtsq"), sequence)
	body = appendSMAFChunk(body, []byte("Mtsp"), waves)
	return appendSMAFChunk(nil, []byte{'M', 'T', 'R', number}, body)
}

func smafTestPCM16Wave(frames, channels int) []byte {
	format := byte(3)
	if channels == 2 {
		format |= 0x80
	}
	return appendSMAFChunk(nil, []byte{'M', 'w', 'a', 1}, append([]byte{format, 0x1f, 0x40}, smafTestPCM16(frames, channels)...))
}

func smafTestChannelTone(t *testing.T, pcm *decodedPCM, channel int, frequency float64) float64 {
	t.Helper()
	if pcm == nil || len(pcm.samples) < 6000 {
		t.Fatal("score did not produce the expected audio window")
	}
	window := make([]float64, 2000)
	for frame := range window {
		window[frame] = float64(pcm.samples[(frame+500)*2+channel]) / 32768
	}
	return toneMagnitude(window, 44100, frequency)
}

func TestSMAFSampledInstrumentUsesWaveAndMusicalPitch(t *testing.T) {
	for _, note := range []byte{48, 60, 72} {
		sequence := []byte{0, 0x90, note, 127, 125, 0, 0xff, 0x2f, 0}
		file := smafStreamTestFile(smafTestSampledTrack(0, smafTestPCMVoice(1, 7999, 7999), sequence, smafTestPCM16Wave(8000, 1)))
		pcm := decodeSMAFPCM16(file, 44100)
		frequency := 1000 * math.Pow(2, float64(int(note)-60)/12)
		if got := smafTestChannelTone(t, pcm, 0, frequency); got < 0.04 {
			t.Fatalf("note %d: sample tone at %.0f Hz = %.5f, want the pitched sample", note, frequency, got)
		}
		// A replacement GM patch produces the same output after a sample change.
		changed := smafTestPCM16Wave(8000, 1)
		for i := 11; i < len(changed); i++ {
			changed[i] = 0
		}
		muted := decodeSMAFPCM16(smafStreamTestFile(smafTestSampledTrack(0,
			smafTestPCMVoice(1, 7999, 7999), sequence, changed)), 44100)
		if got := smafTestChannelTone(t, muted, 0, frequency); got > 0.0001 {
			t.Fatalf("note %d still sounds after its waveform was zeroed: %.5f", note, got)
		}
	}
}

func TestSMAFSampledInstrumentLoopsReleasesAndKeepsOverlappingGates(t *testing.T) {
	sequence := []byte{0, 0x90, 60, 127, 125, 0, 0xff, 0x2f, 0}
	file := smafStreamTestFile(smafTestSampledTrack(0, smafTestPCMVoice(1, 64, 127), sequence, smafTestPCM16Wave(128, 1)))
	d := &smafDecoder{rate: 44100}
	if !d.parse(file) || !d.buildEvents() {
		t.Fatal("sampled instrument did not parse")
	}
	stream := newSMAFRenderStream(d)
	stream.renderUntil(nil, 1500) // past the 16 ms recorded waveform
	first := &d.pcmPool[0]
	if !first.active || !first.looped || !first.keyDown {
		t.Fatal("sampled voice stopped instead of looping between LP and EP")
	}
	d.fire(smafEvent{kind: smafNoteOn, channel: 0, a: 60, b: 64, noteID: 1234})
	second := &d.pcmPool[1]
	d.fire(smafEvent{kind: smafNoteOff, channel: 0, a: 60, noteID: 1234})
	if !first.keyDown || second.keyDown || second.envelope.phase != smafEnvelopeRelease {
		t.Fatal("a later gate released the earlier sampled voice")
	}
	d.fire(smafEvent{kind: smafVolume, channel: 0, a: 32})
	d.fire(smafEvent{kind: smafExpression, channel: 0, a: 63})
	wantVolume := 32.0 / 127 * (63.0 / 127)
	if math.Abs(first.volume-wantVolume) > 1e-12 || math.Abs(second.volume-wantVolume*math.Pow(64.0/127, 2)) > 1e-12 {
		t.Fatal("sampled voices lost velocity, volume, or expression")
	}
	d.fire(smafEvent{kind: smafPitchBend, channel: 0, a: 8192})
	if math.Abs(first.step-8000.0/44100*math.Pow(2, 2.0/12)) > 1e-12 {
		t.Fatal("pitch bend was not applied to the sampled instrument")
	}
	d.fire(smafEvent{kind: smafNoteOff, channel: 0, a: 60, noteID: first.noteID})
	stream.renderUntil(nil, 10000)
	if first.active || second.active {
		t.Fatal("looping sample did not stop after its release envelope")
	}
	for _, voice := range d.pool {
		if voice.active {
			t.Fatal("sampled instrument also started an FM replacement")
		}
	}
}

func TestSMAFSampledInstrumentLazyLengthAndAudioMatchEager(t *testing.T) {
	sequence := []byte{0x85, 0x6e, 0x90, 60, 127, 125, 0, 0xff, 0x2f, 0} // after 3 seconds
	file := smafStreamTestFile(smafTestSampledTrack(0, smafTestPCMVoice(1, 0, 799), sequence, smafTestPCM16Wave(800, 1)))
	eager, lazy := decodeSMAFPCM16(file, 44100), decodeSMAFLazyPCM16(file, 44100)
	if eager == nil || lazy == nil || lazy.smaf == nil {
		t.Fatal("sampled score did not enter incremental playback")
	}
	if eager.duration < 3500*time.Millisecond || eager.duration > 3600*time.Millisecond || lazy.duration != eager.duration {
		t.Fatalf("sampled score length: eager=%s, lazy=%s; want 3.5 seconds plus a short release", eager.duration, lazy.duration)
	}
	lazy.ensureFrame(uint64(len(eager.samples)/2 - 1))
	if !slices.Equal(eager.samples, lazy.samples) {
		t.Fatal("incremental sampled score differs from eager audio")
	}
}

func TestSMAFSampledDrumUsesNativeRateAndFixedPan(t *testing.T) {
	voice := smafTestPCMVoice(1, 7999, 7999)
	voice[8] = 38 // drum key
	voice[12] = 1 // voice pan enabled, hard left
	sequence := []byte{0, 0x90, 38, 127, 125, 0, 0xff, 0x2f, 0}
	file := smafStreamTestFile(smafTestSampledTrack(0, voice, sequence, smafTestPCM16Wave(8000, 1)))
	d := &smafDecoder{rate: 44100}
	if !d.parse(file) || !d.buildEvents() {
		t.Fatal("sampled drum did not parse")
	}
	stream := newSMAFRenderStream(d)
	stream.renderUntil(nil, 100)
	d.fire(smafEvent{kind: smafPan, channel: 0, a: 127})
	if got := d.pcmPool[0].step; got != 8000.0/44100 || d.pcmPool[0].pan != -1 {
		t.Fatal("drum wave was transposed or its fixed pan was overridden")
	}
	pcm := decodeSMAFPCM16(file, 44100)
	if smafTestChannelTone(t, pcm, 0, 1000) < 0.04 || smafTestChannelTone(t, pcm, 1, 1000) > 0.0001 {
		t.Fatal("sampled drum did not preserve its native frequency and pan")
	}
}

func TestSMAFSampledMissingAndROMWavesDoNotBecomeFM(t *testing.T) {
	for _, kind := range []string{"missing", "ROM", "zero-rate"} {
		t.Run(kind, func(t *testing.T) {
			voice := smafTestPCMVoice(1, 127, 127)
			waves := smafTestPCM16Wave(128, 1)
			switch kind {
			case "missing":
				waves = nil
			case "ROM":
				voice[25] &^= 0x80
			case "zero-rate":
				voice[10], voice[11] = 0, 0
			}
			file := smafStreamTestFile(smafTestSampledTrack(0, voice, []byte{0, 0x90, 60, 127, 125}, waves))
			d := &smafDecoder{rate: 44100}
			if !d.parse(file) || !d.buildEvents() {
				t.Fatal("sampled definition was not recognized")
			}
			newSMAFRenderStream(d).renderUntil(nil, 100)
			for _, v := range d.pool {
				if v.active {
					t.Fatal("unavailable sampled voice became a GM/FM patch")
				}
			}
			for _, v := range d.pcmPool {
				if v.active {
					t.Fatal("unavailable sampled voice used an unrelated wave")
				}
			}
		})
	}
}

func TestSMAFStereoStreamAndAudioTrackKeepSeparateChannels(t *testing.T) {
	wave := smafTestPCM16(8000, 2)
	stream := smafStreamTestTrack(0, 0, []byte{125}, smafTestPCM16Wave(8000, 2))
	audio := smafTestATR(0, []byte{0, 0, 0x81, 0x30, 2, 2}, []byte{0, 0x41, 125, 0, 0, 0, 0}, wave)
	for _, track := range [][]byte{stream, audio} {
		pcm := decodeSMAFPCM16(smafStreamTestFile(track), 44100)
		if smafTestChannelTone(t, pcm, 0, 1000) < 0.04 || smafTestChannelTone(t, pcm, 1, 2000) < 0.04 ||
			smafTestChannelTone(t, pcm, 0, 2000) > 0.003 || smafTestChannelTone(t, pcm, 1, 1000) > 0.003 {
			t.Fatal("stereo wave channels were mixed, reversed, or resampled as a mono stream")
		}
		if pcm.duration < 500*time.Millisecond || pcm.duration > 501*time.Millisecond {
			t.Fatalf("stereo gate duration = %s, want 500 ms", pcm.duration)
		}
	}
}

func TestSMAFAudioTrackGateAndWaveIDsAreTrackLocal(t *testing.T) {
	wave := bytes.Repeat([]byte{0x77, 0x77, 0xff, 0xff}, 1024)
	first := smafTestATR(0, []byte{0, 0, 0x11, 0, 0x10, 0x11}, []byte{1, 1, 1, 0, 0, 0, 0}, wave)
	second := smafTestATR(1, []byte{0, 0, 0x12, 0, 0x10, 0x11}, []byte{1, 1, 20, 0, 0, 0, 0}, wave)
	d := &smafDecoder{rate: 44100}
	if !d.parse(smafStreamTestFile(first, second)) || !d.buildEvents() {
		t.Fatal("audio tracks did not parse")
	}
	stream := newSMAFRenderStream(d)
	stream.renderUntil(nil, 500)
	if !d.pcmPool[0].active || !d.pcmPool[1].active || d.pcmPool[0].step != 8000.0/44100 || d.pcmPool[1].step != 11025.0/44100 {
		t.Fatal("audio tracks selected another track's wave with the same ID")
	}
	stream.renderUntil(nil, 1400)
	if d.pcmPool[0].active || !d.pcmPool[1].active {
		t.Fatal("20 ms gate was ignored or stopped the other track's wave")
	}
	pcm := decodeSMAFPCM16(smafStreamTestFile(first), 44100)
	if pcm == nil || len(pcm.samples)/2 != 1324 {
		t.Fatal("audio track did not finish at 10 ms start + 20 ms gate")
	}
}

func TestSMAFPCMRejectsIncompleteSamplesAndUnsupportedFormats(t *testing.T) {
	for _, body := range [][]byte{
		{3, 0x1f, 0x40, 0},       // partial mono PCM16 word
		{0x81, 0x1f, 0x40, 0},    // partial stereo PCM8 frame
		{0x83, 0x1f, 0x40, 0, 0}, // partial stereo PCM16 frame
		{0x21, 0x1f, 0x40, 0},    // ADPCM is not 8-bit linear PCM
		{0x70, 0x1f, 0x40, 0},    // unknown codec
	} {
		if wave := decodeSMAFStreamWave(1, body); len(wave.pcm) != 0 {
			t.Fatalf("invalid stream wave was decoded: %v", body)
		}
	}
	if voice := parseSMAFVoice(smafTestPCMVoice(1, 0, 63)[:25]); voice.valid {
		t.Fatal("truncated sampled voice was accepted")
	}
	for _, header := range [][]byte{
		{0, 0, 0x15, 0, 2, 2}, // invalid sample rate
		{0, 0, 0x21, 0, 2, 2}, // TwinVQ is not ADPCM
		{0, 0, 0x31, 0, 2, 2}, // MP3 is not ADPCM
	} {
		d := &smafDecoder{rate: 44100}
		if d.parse(smafStreamTestFile(smafTestATR(0, header, []byte{0, 1, 125}, []byte{0x77, 0xff}))) {
			t.Fatal("unsupported ATR codec/rate was interpreted as a wave")
		}
	}
	track := smafTestATR(0, []byte{0, 0, 0x11, 0, 2, 2}, []byte{0, 1, 125}, []byte{0x77, 0xff})
	// Keep the outer size valid while truncating the nested Awa payload.
	track = track[:len(track)-1]
	binary.BigEndian.PutUint32(track[4:8], uint32(len(track)-8))
	d := &smafDecoder{rate: 44100}
	if d.parse(smafStreamTestFile(track)) {
		t.Fatal("truncated Awa payload was accepted")
	}
}

func TestSMAFAudioTrackControllersApplyBeforeAndDuringWave(t *testing.T) {
	sequence := []byte{0, 0, 0x37, 64, 0, 1, 125, 0, 0, 0, 0}
	file := smafStreamTestFile(smafTestATR(0, []byte{0, 0, 0x11, 0, 2, 2}, sequence,
		bytes.Repeat([]byte{0x77, 0x77, 0xff, 0xff}, 128)))
	d := &smafDecoder{rate: 44100}
	if !d.parse(file) || !d.buildEvents() {
		t.Fatal("ATR with volume control did not parse")
	}
	newSMAFRenderStream(d).renderUntil(nil, 100)
	voice := &d.pcmPool[0]
	if !voice.active || voice.volume != 64.0/127 {
		t.Fatal("initial audio track volume did not apply to its wave")
	}
	d.fire(smafEvent{kind: smafVolume, channel: 120, a: 32})
	d.fire(smafEvent{kind: smafExpression, channel: 120, a: 63})
	d.fire(smafEvent{kind: smafPan, channel: 120, a: 0})
	if math.Abs(voice.volume-32.0/127*(63.0/127)) > 1e-12 || voice.pan != -1 {
		t.Fatal("ongoing audio track wave lost its volume, expression, or pan")
	}
}

func TestSMAFSampledLoopPointsAreBoundedByDecodedFrames(t *testing.T) {
	for _, points := range [][2]int{{0, 0}, {127, 127}, {65535, 65535}, {64, 65535}, {65535, 127}} {
		file := smafStreamTestFile(smafTestSampledTrack(0, smafTestPCMVoice(1, points[0], points[1]),
			[]byte{0, 0x90, 60, 127, 125}, smafTestPCM16Wave(128, 2)))
		d := &smafDecoder{rate: 44100}
		if !d.parse(file) || !d.buildEvents() {
			t.Fatal("sampled loop fixture did not parse")
		}
		newSMAFRenderStream(d).renderUntil(nil, 3000)
		voice := &d.pcmPool[0]
		if voice.end > 128 || (voice.loop && voice.loopStart >= voice.end) {
			t.Fatal("sampled loop/end points were not limited to decoded stereo frames")
		}
	}
}

func smafTestPCMVoice(waveID byte, loopPoint, endPoint int) []byte {
	// Yamaha VM35 sampled voice: Fs is the playback rate at C4 (note 60).
	// RM selects RAM, not looping; LP < EP enables looping.
	body := []byte{
		0x1f, 0x40, 0, 0, 0, 0xf0, 0xf0, 0,
		0, 0, 0, byte(loopPoint >> 8), byte(loopPoint), byte(endPoint >> 8), byte(endPoint),
		0x80 | waveID, 0, 0, 0,
	}
	return append([]byte{0x43, 0x79, 0x07, 0x7f, 1, 0, 0, 0, 0, 1}, body...)
}

func TestSMAFParsesEmbeddedPCMVoice(t *testing.T) {
	if voice := parseSMAFVoice(smafTestPCMVoice(1, 511, 511)); !voice.valid {
		t.Fatal("embedded PCM voice ignored; ordinary note falls back to FM")
	}
}

func smafTestATR(number byte, header, sequence, wave []byte) []byte {
	body := append([]byte(nil), header...)
	body = appendSMAFChunk(body, []byte("Atsq"), sequence)
	body = appendSMAFChunk(body, []byte{'A', 'w', 'a', 1}, wave)
	return appendSMAFChunk(nil, []byte{'A', 'T', 'R', number}, body)
}

func TestSMAFAudioTrackReadsWaveFormatAndTimeBasesFromHeader(t *testing.T) {
	data := bytes.Repeat([]byte{0x77, 0x72, 0xff, 0xff}, 128)
	file := smafStreamTestFile(smafTestATR(0,
		[]byte{0, 0, 0x11, 0, 0x10, 0x11}, // ADPCM 8 kHz; duration 10 ms, gate 20 ms
		[]byte{1, 1, 10, 0, 0, 0, 0}, data))
	d := &smafDecoder{rate: 44100}
	if !d.parse(file) || len(d.tracks) != 1 {
		t.Fatal("synthetic ATR did not parse")
	}
	track := d.tracks[0]
	if track.durationBase != 0x10 || track.gateBase != 0x11 {
		t.Errorf("ATR time bases = %#x/%#x, want 0x10/0x11", track.durationBase, track.gateBase)
	}
	wave := track.waves[0]
	if wave.sampleRate != 8000 || !slices.Equal(wave.pcm, decodeYamahaADPCM(data)) {
		t.Errorf("ATR wave = %d Hz, %d samples; want 8000 Hz and all %d samples", wave.sampleRate, len(wave.pcm), len(data)*2)
	}
	if !d.buildEvents() {
		t.Fatal("ATR did not build events")
	}
	if at := d.events[0].sample; at != 441 {
		t.Errorf("first wave starts at frame %d, want 441 (10 ms)", at)
	}
}

func TestSMAFTimeBases(t *testing.T) {
	for _, tc := range []struct {
		code         byte
		milliseconds float64
	}{
		{0, 1}, {1, 2}, {2, 4}, {3, 5}, {0x10, 10}, {0x11, 20}, {0x12, 40}, {0x13, 50},
	} {
		if got := smafTimeBase(tc.code); got != tc.milliseconds {
			t.Errorf("time base %#x = %.0f ms, want %.0f ms", tc.code, got, tc.milliseconds)
		}
	}
}
