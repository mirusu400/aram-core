package runtime

import (
	"encoding/binary"
	"math"
	"testing"
)

// smafMA2RegisterScore wraps a HandyPhone (MA-2) sequence in a format 0 score
// track with a 4 ms timebase.
func smafMA2RegisterScore(sequence []byte) []byte {
	trackBody := []byte{0x00, 0x00, 0x02, 0x02, 0x00, 0x00}
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsq"), sequence)
	file := appendSMAFChunk(nil, []byte{'M', 'T', 'R', 1}, trackBody)
	data := make([]byte, 8)
	copy(data, "MMMD")
	binary.BigEndian.PutUint32(data[4:], uint32(len(file)))
	return append(data, file...)
}

// smafMA2RegisterWrite is one in-sequence FM register write: duration, then
// FF F0 <length> 43 03 90 <register> <value> F7.
func smafMA2RegisterWrite(duration, register, value byte) []byte {
	return []byte{duration, 0xff, 0xf0, 0x06, 0x43, 0x03, 0x90, register, value, 0xf7}
}

func smafMA2Tone(t *testing.T, pcm *decodedPCM, from, to float64, frequency float64) float64 {
	t.Helper()
	start := int(from * float64(pcm.sampleRate))
	end := int(to * float64(pcm.sampleRate))
	if len(pcm.samples)/2 < end {
		t.Fatalf("score ends at %d frames, before %.2f s", len(pcm.samples)/2, to)
	}
	window := make([]float64, end-start)
	for frame := range window {
		hann := 0.5 - 0.5*math.Cos(2*math.Pi*float64(frame)/float64(len(window)-1))
		window[frame] = hann * float64(pcm.samples[(start+frame)*2]) / 32768
	}
	return toneMagnitude(window, float64(pcm.sampleRate), frequency)
}

// TestSMAFMA2RegisterWritesPlayTheChannelVoice pins MA-2 direct FM control. A
// title can key a channel's voice on and steer its pitch by writing the FM
// registers from the sequence instead of sending notes: 0xB0+ch holds the low
// eight F-Number bits, 0xC0+ch the key-on bit, the block and the top two
// F-Number bits. Pitch-sweep sound effects are built entirely this way.
func TestSMAFMA2RegisterWritesPlayTheChannelVoice(t *testing.T) {
	sequence := []byte{0x00, 0x00, 0x30, 0x00} // channel 0 program 0
	sequence = append(sequence, smafMA2RegisterWrite(0, 0xb0, 0x44)...)
	sequence = append(sequence, smafMA2RegisterWrite(0, 0xc0, 0x32)...) // key on, block 4, F-Number 0x244
	sequence = append(sequence, smafMA2RegisterWrite(125, 0xb0, 0x00)...)
	sequence = append(sequence, smafMA2RegisterWrite(125, 0xc0, 0x12)...) // key off
	sequence = append(sequence, 125, 0x00, 0x00, 0x00)
	pcm := decodeSMAFPCM16(smafMA2RegisterScore(sequence), 44_100)
	if pcm == nil {
		t.Fatal("register-driven score did not decode")
	}
	first := smafMA2RegisterFrequency(0x244, 4)
	second := smafMA2RegisterFrequency(0x200, 4)
	if a, b := smafMA2Tone(t, pcm, 0.1, 0.45, first), smafMA2Tone(t, pcm, 0.1, 0.45, second); a < 0.01 || a < 4*b {
		t.Fatalf("keyed voice at %.1f Hz = %.4f (%.1f Hz = %.4f), want it sounding at the written pitch", first, a, second, b)
	}
	if a, b := smafMA2Tone(t, pcm, 0.6, 0.95, second), smafMA2Tone(t, pcm, 0.6, 0.95, first); a < 0.01 || a < 4*b {
		t.Fatalf("F-Number write did not move the sounding voice: %.1f Hz = %.4f, %.1f Hz = %.4f", second, a, first, b)
	}
	tail := smafMA2Tone(t, pcm, 1.3, 1.45, second)
	if tail > smafMA2Tone(t, pcm, 0.6, 0.95, second)/10 {
		t.Fatalf("key-off write left the voice sounding: %.4f", tail)
	}
}
