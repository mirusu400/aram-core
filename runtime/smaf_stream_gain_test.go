package runtime

import (
	"math"
	"testing"
)

// smafStreamGainScore plays stream wave 1 on channel 0 for 1 s, optionally
// after a 43 79 <device> 7F 0B <channel 1> <mode> 40 message.
func smafStreamGainScore(device byte, mode int) []byte {
	sequence := []byte{0, 0xb0, 0, 125, 0, 0xb0, 0x20, 0, 0, 0xc0, 0}
	if mode >= 0 {
		sequence = append(sequence, 0, 0xf0, 0x09, 0x43, 0x79, device, 0x7f, 0x0b, 0x01, byte(mode), 0x40, 0xf7)
	}
	sequence = append(sequence, 0, 0x90, 0, 127, 0x81, 0x7a, 0x81, 0x7a, 0xff, 0x2f, 0)
	return smafStreamTestFile(smafStreamTestTrackWithSequence(0, sequence,
		smafStreamTestWave(1, 8000, []byte{0x77, 0x77, 0xff, 0xff})))
}

func smafStreamGainRMS(t *testing.T, data []byte) float64 {
	t.Helper()
	pcm := decodeSMAFPCM16(data, 44_100)
	if pcm == nil {
		t.Fatal("stream score did not decode")
	}
	start, end := int(0.05*44_100), int(0.25*44_100)
	if len(pcm.samples)/2 < end {
		t.Fatalf("stream score ends after %d frames", len(pcm.samples)/2)
	}
	var sum float64
	for frame := start; frame < end; frame++ {
		value := float64(pcm.samples[frame*2])
		sum += value * value
	}
	return math.Sqrt(sum / float64(end-start))
}

// TestSMAFMA5StreamGainMode pins 43 79 07 7F 0B <channel> <mode> <value>. The
// SPH-W8300's SMAF library (firmware 0x15c0c58) handles it for MA-5 scores
// only: it sets the audio slots playing on that channel (counted from 1) to
// the full (vv+1)^2 master level when mode is 2 and to half of it otherwise,
// and never reads the value byte. The MA-3 form (device 06) has no handler.
func TestSMAFMA5StreamGainMode(t *testing.T) {
	plain := smafStreamGainScore(0x07, -1)
	full := smafStreamGainRMS(t, plain)
	if full < 50 {
		t.Fatalf("stream wave too quiet to measure: rms %.1f", full)
	}
	if got := smafStreamGainRMS(t, smafStreamGainScore(0x07, 2)); math.Abs(got-full) > full*0.01 {
		t.Fatalf("mode 2 stream rms = %.1f, want the full %.1f", got, full)
	}
	if got := smafStreamGainRMS(t, smafStreamGainScore(0x07, 0)); math.Abs(got-full/2) > full*0.03 {
		t.Fatalf("mode 0 stream rms = %.1f, want half of %.1f", got, full)
	}
	if got := smafStreamGainRMS(t, smafStreamGainScore(0x06, 0)); math.Abs(got-full) > full*0.01 {
		t.Fatalf("MA-3 form changed the stream: rms %.1f, want %.1f", got, full)
	}
}
