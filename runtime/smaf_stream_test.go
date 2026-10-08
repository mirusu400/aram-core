package runtime

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// These synthetic ADPCM waves have four positive and four negative samples
// per period at 8 kHz. A bank-125 note selects a wave, not a musical pitch.
func smafStreamTestWave(number byte, rate int, pattern []byte) []byte {
	body := []byte{0x20, byte(rate >> 8), byte(rate)}
	body = append(body, bytes.Repeat(pattern, 128)...)
	return appendSMAFChunk(nil, []byte{'M', 'w', 'a', number}, body)
}

func smafStreamTestTrack(number byte, note byte, gate []byte, waves []byte) []byte {
	sequence := []byte{
		0, 0xb0, 0, 125,
		0, 0xb0, 0x20, 0,
		0, 0xc0, 0,
		0, 0x90, note, 127,
	}
	sequence = append(sequence, gate...)
	sequence = append(sequence, 0, 0xff, 0x2f, 0)
	return smafStreamTestTrackWithSequence(number, sequence, waves)
}

func smafStreamTestTrackWithSequence(number byte, sequence, waves []byte) []byte {
	body := append([]byte{2, 0, 2, 2}, make([]byte, 16)...)
	body = appendSMAFChunk(body, []byte("Mtsq"), sequence)
	body = appendSMAFChunk(body, []byte("Mtsp"), waves)
	return appendSMAFChunk(nil, []byte{'M', 'T', 'R', number}, body)
}

func TestSMAFStreamPCMLazyLengthAndSamplesMatchEager(t *testing.T) {
	sequence := []byte{
		0, 0xb0, 0, 125, 0, 0xb0, 0x20, 0, 0, 0xc0, 0,
		0x85, 0x6e, 0x90, 0, 127, 125, // wave 1 after 3 seconds
		0, 0xff, 0x2f, 0,
	}
	file := smafStreamTestFile(smafStreamTestTrackWithSequence(0, sequence,
		smafStreamTestWave(1, 8000, []byte{0x77, 0x77, 0xff, 0xff})))
	eager, lazy := decodeSMAFPCM16(file, 44_100), decodeSMAFLazyPCM16(file, 44_100)
	if eager == nil || lazy == nil || lazy.smaf == nil {
		t.Fatal("long stream score did not take the incremental path")
	}
	if lazy.duration != eager.duration {
		t.Fatalf("stream length probe = %s, want %s", lazy.duration, eager.duration)
	}
	lazy.ensureFrame(uint64(len(eager.samples)/2 - 1))
	if len(lazy.samples) != len(eager.samples) {
		t.Fatalf("incremental stream produced %d samples, want %d", len(lazy.samples), len(eager.samples))
	}
	for i, sample := range eager.samples {
		if lazy.samples[i] != sample {
			t.Fatalf("incremental PCM differs at sample %d", i)
		}
	}
}

func TestSMAFStreamPCMSelectsHighNotesAndKeepsFMTracks(t *testing.T) {
	waves := append(smafStreamTestWave(1, 8000, []byte{0x77, 0xff}),
		smafStreamTestWave(14, 11025, []byte{0x77, 0x77, 0xff, 0xff})...)
	sequence := []byte{
		0, 0xb0, 0, 125, 0, 0xb0, 0x20, 0, 0, 0xc0, 0,
		0, 0x90, 92, 127, 125, // stream wave 14
		0, 0xb1, 0, 124,
		0, 0x91, 60, 127, 125, // ordinary C4 on another channel
		0, 0xff, 0x2f, 0,
	}
	d := &smafDecoder{rate: 44_100}
	if !d.parse(smafStreamTestFile(smafStreamTestTrackWithSequence(0, sequence, waves))) || !d.buildEvents() {
		t.Fatal("mixed stream/FM track did not parse")
	}
	newSMAFRenderStream(d).renderUntil(nil, 100)
	if !d.pcmPool[0].active || d.pcmPool[0].step != 11025.0/44100 || !d.pool[0].active || d.pool[0].keyNote != 60 {
		t.Fatal("stream wave selection replaced or transposed the FM note")
	}
}

func TestSMAFStreamPCMControllersAndOverlappingGates(t *testing.T) {
	d := &smafDecoder{rate: 44_100}
	if !d.parse(smafStreamTestFile(smafStreamTestTrack(0, 0, []byte{125},
		smafStreamTestWave(1, 8000, []byte{0x77, 0x77, 0xff, 0xff})))) || !d.buildEvents() {
		t.Fatal("stream score did not parse")
	}
	for _, event := range d.events {
		if event.sample == 0 {
			d.fire(event)
		}
	}
	firstID := d.pcmPool[0].noteID
	d.fire(smafEvent{kind: smafNoteOn, noteID: firstID + 2, channel: 0, a: 0, b: 64})
	d.fire(smafEvent{kind: smafVolume, channel: 0, a: 32})
	d.fire(smafEvent{kind: smafExpression, channel: 0, a: 63})
	d.fire(smafEvent{kind: smafPan, channel: 0, a: 0})
	want := 32.0 / 127 * (63.0 / 127)
	if math.Abs(d.pcmPool[0].volume-want) > 1e-12 || d.pcmPool[0].pan != -1 ||
		math.Abs(d.pcmPool[1].volume-want*math.Pow(64.0/127, 2)) > 1e-12 {
		t.Fatal("stream PCM did not retain velocity, volume, expression or pan")
	}
	d.fire(smafEvent{kind: smafNoteOff, noteID: firstID + 2, channel: 0, a: 0})
	if !d.pcmPool[0].active || d.pcmPool[1].active {
		t.Fatal("the later overlapping gate stopped the wrong sampled effect")
	}
	d.fire(smafEvent{kind: smafNoteOff, noteID: firstID, channel: 0, a: 0})
	if d.pcmPool[0].active {
		t.Fatal("the original gate did not stop its sampled effect")
	}
}

func TestSMAFStreamWaveRejectsTruncatedChunk(t *testing.T) {
	chunk := smafStreamTestWave(1, 8000, []byte{0x77, 0xff})
	if waves := parseSMAFStreamWaves(chunk[:len(chunk)-1]); len(waves) != 0 {
		t.Fatal("truncated Mwa chunk was accepted")
	}
}

func smafStreamTestFile(tracks ...[]byte) []byte {
	data := []byte("MMMD\x00\x00\x00\x00")
	for _, track := range tracks {
		data = append(data, track...)
	}
	binary.BigEndian.PutUint32(data[4:8], uint32(len(data)-8))
	return data
}

func TestSMAFStreamPCMPlaysWaveInsteadOfSubaudibleNote(t *testing.T) {
	file := smafStreamTestFile(smafStreamTestTrack(5, 0, []byte{125},
		smafStreamTestWave(1, 8000, []byte{0x77, 0x77, 0xff, 0xff})))
	decoded := decodeSMAFPCM16(file, 44_100)
	if decoded == nil {
		t.Fatal("stream PCM decode failed")
	}
	mono := make([]float64, 3000)
	for i := range mono {
		mono[i] = float64(decoded.samples[(i+1000)*2]) / 32768
	}
	if got := toneMagnitude(mono, 44_100, 1000); got < 0.05 {
		t.Fatalf("sampled 1 kHz effect amplitude = %.6f; want the wave, not MIDI note 0", got)
	}
	other := smafStreamTestFile(smafStreamTestTrack(5, 0, []byte{125},
		smafStreamTestWave(1, 8000, []byte{0x77, 0xff})))
	changed := decodeSMAFPCM16(other, 44_100)
	if changed == nil {
		t.Fatal("second stream PCM decode failed")
	}
	if len(decoded.samples) == len(changed.samples) {
		equal := true
		for i := range decoded.samples {
			if decoded.samples[i] != changed.samples[i] {
				equal = false
				break
			}
		}
		if equal {
			t.Fatal("changing the sampled effect did not change output PCM")
		}
	}
}

func TestSMAFStreamPCMHonorsGateAndTrackWaveBank(t *testing.T) {
	first := smafStreamTestTrack(0, 0, []byte{2},
		smafStreamTestWave(1, 8000, []byte{0x77, 0x77, 0xff, 0xff}))
	second := smafStreamTestTrack(1, 0, []byte{125},
		smafStreamTestWave(1, 11025, []byte{0x77, 0xff}))
	d := &smafDecoder{rate: 44_100}
	if !d.parse(smafStreamTestFile(first, second)) || !d.buildEvents() {
		t.Fatal("stream tracks did not parse")
	}
	stream := newSMAFRenderStream(d)
	stream.renderUntil(nil, 500)
	active := 0
	for _, voice := range d.pcmPool {
		if !voice.active {
			continue
		}
		active++
		if math.Abs(voice.step-11025.0/44100) > 1e-12 {
			t.Fatalf("active stream rate ratio = %.6f; want track 1's wave", voice.step)
		}
	}
	if active != 1 {
		t.Fatalf("active sampled effects = %d, want 1 after track 0's 8 ms gate", active)
	}
	for _, voice := range d.pool {
		if voice.active {
			t.Fatal("stream PCM also created a substitute FM voice")
		}
	}
}

func TestSMAFStreamPCMUnsupportedWaveDoesNotBecomeFM(t *testing.T) {
	for _, waves := range [][]byte{
		nil,
		appendSMAFChunk(nil, []byte{'M', 'w', 'a', 1}, []byte{0x20, 0x1f}),
		appendSMAFChunk(nil, []byte{'M', 'w', 'a', 1}, []byte{0x20, 0, 0, 0x77}),
		appendSMAFChunk(nil, []byte{'M', 'w', 'a', 1}, []byte{0x70, 0x1f, 0x40, 0x77}),
	} {
		d := &smafDecoder{rate: 44_100}
		if !d.parse(smafStreamTestFile(smafStreamTestTrack(0, 0, []byte{125}, waves))) || !d.buildEvents() {
			t.Fatal("synthetic sequence did not parse")
		}
		for _, event := range d.events {
			if event.sample == 0 {
				d.fire(event)
			}
		}
		for _, voice := range d.pool {
			if voice.active {
				t.Fatal("missing/unsupported stream wave fell back to FM")
			}
		}
		for _, voice := range d.pcmPool {
			if voice.active {
				t.Fatal("missing/unsupported stream wave became active")
			}
		}
	}
}
