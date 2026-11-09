package runtime

import (
	"encoding/binary"
	"math"
	"testing"
)

// smafModulationScore is a HandyPhone (MA-2) score: optionally a modulation
// control, then C5 for 2 s. A short-form control (class 2) carries a table
// index 0-15; a long-form one (class 3, control 3) carries the depth 0-127,
// passed here as 16 + depth.
func smafModulationScore(index int) []byte {
	sequence := []byte{0x00, 0x00, 0x30, 0x00} // channel 0 program 0
	if index >= 16 {
		sequence = append(sequence, 0x00, 0x00, 0x33, byte(index-16))
	} else if index >= 0 {
		sequence = append(sequence, 0x00, 0x00, byte(0x20|index))
	}
	// C5 is octave 3 note 0 above the format's MIDI 36 base; gate 500 ticks.
	sequence = append(sequence, 0x00, 0x30, 0x82, 0x74, 0x83, 0x74, 0x00, 0x00, 0x00)
	trackBody := []byte{0x00, 0x00, 0x02, 0x02, 0x00, 0x00}
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsq"), sequence)
	file := appendSMAFChunk(nil, []byte{'M', 'T', 'R', 1}, trackBody)
	data := make([]byte, 8)
	copy(data, "MMMD")
	binary.BigEndian.PutUint32(data[4:], uint32(len(file)))
	return append(data, file...)
}

// smafPitchSpread estimates the fundamental in consecutive 40 ms windows over
// 0.2-1.8 s and returns the largest relative distance between two estimates.
func smafPitchSpread(t *testing.T, data []byte, nominal float64) float64 {
	t.Helper()
	pcm := decodeSMAFPCM16(data, 44_100)
	if pcm == nil {
		t.Fatal("score did not decode")
	}
	const window = 1764
	low, high := math.Inf(1), math.Inf(-1)
	for start := int(0.2 * 44_100); start+window <= int(1.8*44_100); start += window {
		samples := make([]float64, window)
		for frame := range samples {
			hann := 0.5 - 0.5*math.Cos(2*math.Pi*float64(frame)/float64(window-1))
			samples[frame] = hann * float64(pcm.samples[(start+frame)*2]) / 32768
		}
		best, bestMagnitude := 0.0, -1.0
		for cents := -60.0; cents <= 60; cents += 1 {
			frequency := nominal * math.Pow(2, cents/1200)
			if magnitude := toneMagnitude(samples, 44_100, frequency); magnitude > bestMagnitude {
				best, bestMagnitude = frequency, magnitude
			}
		}
		low, high = math.Min(low, best), math.Max(high, best)
	}
	return high/low - 1
}

// TestSMAFModulationAddsVibrato pins channel modulation. HandyPhone scores send
// it as the short-form class-2 control (index 1-15 through the format's table)
// and Mobile Standard scores as CC1; either way it sets the depth of the
// channel's vibrato.
func TestSMAFModulationAddsVibrato(t *testing.T) {
	c5 := smafNoteFrequency(72)
	plain := smafPitchSpread(t, smafModulationScore(-1), c5)
	deep := smafPitchSpread(t, smafModulationScore(14), c5)
	if plain > 0.003 {
		t.Fatalf("unmodulated note already wobbles by %.4f", plain)
	}
	if deep < 0.01 {
		t.Fatalf("full modulation spreads the pitch by only %.4f, want vibrato of at least 1%%", deep)
	}
	if long := smafPitchSpread(t, smafModulationScore(16+127), c5); long < 0.01 {
		t.Fatalf("long-form modulation 127 spreads the pitch by only %.4f, want vibrato of at least 1%%", long)
	}
}
