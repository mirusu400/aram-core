package runtime

import (
	"encoding/binary"
	"math"
	"testing"
)

// smafBendRangeScore is a Mobile Standard score that bends channel 0 up by
// raw 683, one semitone at a sensitivity of 12, before playing C4 for 1 s.
// controls are inserted ahead of the bend.
func smafBendRangeScore(controls ...byte) []byte {
	sequence := []byte{
		0x00, 0xb0, 0x07, 0x7f, // channel volume
		0x00, 0xc0, 0x00, // program 0
	}
	sequence = append(sequence, controls...)
	sequence = append(sequence,
		0x00, 0xe0, 0x2b, 0x45, // pitch bend 8875 = centre + 683
		0x00, 0x90, 60, 110, 0x81, 0x7a, // C4, 1 s gate
		0x81, 0x7a, 0xff, 0x2f, // end after 1 s
	)
	trackBody := []byte{0x02, 0x00, 0x02, 0x02}
	trackBody = append(trackBody, make([]byte, 16)...)
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsq"), sequence)
	file := appendSMAFChunk(nil, []byte{'M', 'T', 'R', 0}, trackBody)
	data := make([]byte, 8)
	copy(data, "MMMD")
	binary.BigEndian.PutUint32(data[4:], uint32(len(file)))
	return append(data, file...)
}

// smafBendRangePitch reports which of the candidate fundamentals dominates
// 300-700 ms into the rendered score.
func smafBendRangePitch(t *testing.T, data []byte, candidates ...float64) float64 {
	t.Helper()
	pcm := decodeSMAFPCM16(data, 44_100)
	if pcm == nil {
		t.Fatal("score did not decode")
	}
	start := int(0.3 * float64(pcm.sampleRate))
	window := make([]float64, int(0.4*float64(pcm.sampleRate)))
	for frame := range window {
		hann := 0.5 - 0.5*math.Cos(2*math.Pi*float64(frame)/float64(len(window)-1))
		window[frame] = hann * float64(pcm.samples[(start+frame)*2]) / 32768
	}
	best, bestMagnitude := 0.0, -1.0
	for _, frequency := range candidates {
		if magnitude := toneMagnitude(window, float64(pcm.sampleRate), frequency); magnitude > bestMagnitude {
			best, bestMagnitude = frequency, magnitude
		}
	}
	return best
}

func TestSMAFPitchBendFollowsRegisteredSensitivity(t *testing.T) {
	c4 := smafNoteFrequency(60)
	shortBend := c4 * math.Pow(2, 683.0/8192*2/12) // the default ±2 semitones
	cSharp4 := smafNoteFrequency(61)
	t.Run("RPN 0 sets the range", func(t *testing.T) {
		score := smafBendRangeScore(
			0x00, 0xb0, 101, 0,
			0x00, 0xb0, 100, 0,
			0x00, 0xb0, 6, 12,
		)
		if got := smafBendRangePitch(t, score, c4, shortBend, cSharp4); got != cSharp4 {
			t.Fatalf("bend of one semitone at sensitivity 12 sounds at %.1f Hz, want C#4 %.1f Hz", got, cSharp4)
		}
	})
	t.Run("data entry without a selected parameter changes nothing", func(t *testing.T) {
		score := smafBendRangeScore(0x00, 0xb0, 6, 12)
		if got := smafBendRangePitch(t, score, c4, shortBend, cSharp4); got != shortBend {
			t.Fatalf("unselected data entry moved the bend to %.1f Hz, want %.1f Hz", got, shortBend)
		}
	})
	t.Run("a non-registered parameter deselects RPN 0", func(t *testing.T) {
		score := smafBendRangeScore(
			0x00, 0xb0, 101, 0,
			0x00, 0xb0, 100, 0,
			0x00, 0xb0, 99, 1,
			0x00, 0xb0, 98, 8,
			0x00, 0xb0, 6, 12,
		)
		if got := smafBendRangePitch(t, score, c4, shortBend, cSharp4); got != shortBend {
			t.Fatalf("NRPN data entry moved the bend to %.1f Hz, want %.1f Hz", got, shortBend)
		}
	})
	t.Run("reset all controllers keeps the sensitivity", func(t *testing.T) {
		score := smafBendRangeScore(
			0x00, 0xb0, 101, 0,
			0x00, 0xb0, 100, 0,
			0x00, 0xb0, 6, 12,
			0x00, 0xb0, 121, 0,
		)
		if got := smafBendRangePitch(t, score, c4, shortBend, cSharp4); got != cSharp4 {
			t.Fatalf("bend after reset all controllers sounds at %.1f Hz, want C#4 %.1f Hz", got, cSharp4)
		}
	})
}
