package runtime

import (
	"encoding/binary"
	"math"
	"testing"
)

// smafMasterVolumeScore is a Mobile Standard score that sends the MA-3 master
// volume exclusive (43 79 06 7F 00 vv) when master >= 0, then plays C4 for 1 s.
func smafMasterVolumeScore(master int) []byte {
	sequence := []byte{}
	if master >= 0 {
		sequence = append(sequence, 0x00, 0xf0, 0x07, 0x43, 0x79, 0x06, 0x7f, 0x00, byte(master), 0xf7)
	}
	sequence = append(sequence,
		0x00, 0xb0, 0x07, 0x50, // channel volume 80, well clear of the limiter
		0x00, 0xc0, 0x00,
		0x00, 0x90, 60, 100, 0x81, 0x7a,
		0x81, 0x7a, 0xff, 0x2f,
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

func smafMasterVolumeRMS(t *testing.T, data []byte) float64 {
	t.Helper()
	pcm := decodeSMAFPCM16(data, 44_100)
	if pcm == nil {
		t.Fatal("score did not decode")
	}
	start, end := int(0.2*44_100), int(0.8*44_100)
	var sum float64
	for frame := start; frame < end; frame++ {
		value := float64(pcm.samples[frame*2])
		sum += value * value
	}
	return math.Sqrt(sum / float64(end-start))
}

// TestSMAFMasterVolumeScalesTheWholeScore pins the MA-3 master volume message.
// Titles send it once at the top of every score, with values from 80 to 127,
// to balance their music against their sound effects.
func TestSMAFMasterVolumeScalesTheWholeScore(t *testing.T) {
	unset := smafMasterVolumeRMS(t, smafMasterVolumeScore(-1))
	full := smafMasterVolumeRMS(t, smafMasterVolumeScore(127))
	half := smafMasterVolumeRMS(t, smafMasterVolumeScore(64))
	if unset < 100 {
		t.Fatalf("reference note too quiet to measure: rms %.1f", unset)
	}
	if math.Abs(full-unset) > unset*0.01 {
		t.Fatalf("master volume 127 changed the level: %.1f, want %.1f", full, unset)
	}
	want := unset * 64 / 127
	if math.Abs(half-want) > want*0.05 {
		t.Fatalf("master volume 64 rms = %.1f, want %.1f (64/127 of %.1f)", half, want, unset)
	}
}
