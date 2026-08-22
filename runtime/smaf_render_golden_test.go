package runtime

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
	"unsafe"
)

// TestSMAFRenderIsBitStable pins the rendered PCM of a synthetic score.
//
// The FM renderer is performance-sensitive and has been optimized by removing
// per-sample repetition (memoized pan gains, a reduced-phase sine, tabled
// waveforms). Every one of those is supposed to be exact, and "the audio still
// sounds fine" cannot tell an exact change from a nearly-exact one. This hashes
// the samples so an accidental change of the synthesis math fails loudly. A
// deliberate change to the model updates the constant, with the audible
// difference described in the commit.
//
// The score is built here rather than loaded, so the test carries no authored
// music: it plays overlapping notes on two channels, pans them apart so the
// stereo gains differ per voice, and bends one so a voice retunes mid-note.
func TestSMAFRenderIsBitStable(t *testing.T) {
	const wantHash = "35ca156344954641d1597a9346fec1c2" +
		"369acb8ecf352e69fdb2dae78c2cd8de"
	sequence := []byte{
		0x00, 0xb0, 0x07, 0x7f, // channel 0 volume
		0x00, 0xb0, 0x0a, 0x00, // channel 0 panned hard left
		0x00, 0xc0, 0x00, // channel 0 program 0
		0x00, 0xb1, 0x07, 0x60, // channel 1 volume
		0x00, 0xb1, 0x0a, 0x7f, // channel 1 panned hard right
		0x00, 0xc1, 0x18, // channel 1 program 24
		0x00, 0x90, 60, 110, 120, // C4 on channel 0
		0x0a, 0x91, 67, 90, 110, // G4 on channel 1, overlapping
		0x14, 0xb0, 0x0a, 0x40, // channel 0 re-panned to centre
		0x0a, 0x90, 64, 100, 90, // E4 on channel 0
		0x14, 0xe1, 0x00, 0x50, // pitch bend on channel 1
		0x28, 0x80, 60, 60, // release
		0x28, 0xff, 0x2f, // end of sequence
	}
	decoded := decodeSMAFPCM16(smafScore(sequence), 44_100)
	if decoded == nil || len(decoded.samples) == 0 {
		t.Fatal("decodeSMAFPCM16 rendered nothing")
	}
	raw := unsafe.Slice(
		(*byte)(unsafe.Pointer(&decoded.samples[0])),
		len(decoded.samples)*2,
	)
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if got != wantHash {
		t.Fatalf(
			"rendered %d samples hashing to %s, want %s;"+
				" the synthesis math changed",
			len(decoded.samples), got, wantHash,
		)
	}
}

// TestSMAFProbeEndMatchesRender checks that the silent length probe stops on
// the same sample a full render does.
//
// probeEnd is what lets a score report its natural length without being
// synthesized at decode, so the two have to agree exactly: if the probe stops
// early a clip is truncated, and if it stops late a loop carries silence. It
// holds because the render's stop condition reads only the event index and
// whether a voice is still sounding, and a voice retires purely on its
// envelopes, which the silent pass advances identically.
func TestSMAFProbeEndMatchesRender(t *testing.T) {
	for _, score := range [][]byte{
		smafGoldenScore(),
		// A single short note: the render stops long before the two-second pad
		// in stream.end, which is the case the probe exists for.
		smafScore([]byte{
			0x00, 0xb0, 0x07, 0x7f,
			0x00, 0xc0, 0x00,
			0x00, 0x90, 60, 100, 20,
			0x14, 0xff, 0x2f,
		}),
		// Overlapping notes on two channels with a long gate, so voices retire
		// at different samples.
		smafScore([]byte{
			0x00, 0xb0, 0x07, 0x7f,
			0x00, 0xc0, 0x30,
			0x00, 0x90, 48, 120, 127,
			0x05, 0x91, 72, 40, 10,
			0x40, 0xff, 0x2f,
		}),
	} {
		rendered := decodeSMAFPCM16(score, 44_100)
		if rendered == nil {
			t.Fatal("decodeSMAFPCM16 rendered nothing")
		}
		want := uint64(len(rendered.samples) / 2)

		decoder := &smafDecoder{rate: 44_100}
		if !decoder.parse(score) || !decoder.buildEvents() {
			t.Fatal("probe decoder rejected the score")
		}
		got := newSMAFRenderStream(decoder).probeEnd()
		if got != want {
			t.Errorf("probeEnd = %d, render produced %d frames", got, want)
		}
	}
}

// smafScore wraps a mobile-score sequence in the smallest MMF that carries it.
func smafScore(sequence []byte) []byte {
	trackBody := []byte{0x02, 0x00, 0x02, 0x02}
	trackBody = append(trackBody, make([]byte, 16)...)
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsq"), sequence)
	file := appendSMAFChunk(nil, []byte{'M', 'T', 'R', 0}, trackBody)
	data := make([]byte, 8)
	copy(data, "MMMD")
	binary.BigEndian.PutUint32(data[4:], uint32(len(file)))
	return append(data, file...)
}

// smafGoldenScore is the sequence TestSMAFRenderIsBitStable renders.
func smafGoldenScore() []byte {
	return smafScore([]byte{
		0x00, 0xb0, 0x07, 0x7f,
		0x00, 0xb0, 0x0a, 0x00,
		0x00, 0xc0, 0x00,
		0x00, 0xb1, 0x07, 0x60,
		0x00, 0xb1, 0x0a, 0x7f,
		0x00, 0xc1, 0x18,
		0x00, 0x90, 60, 110, 120,
		0x0a, 0x91, 67, 90, 110,
		0x14, 0xb0, 0x0a, 0x40,
		0x0a, 0x90, 64, 100, 90,
		0x14, 0xe1, 0x00, 0x50,
		0x28, 0x80, 60, 60,
		0x28, 0xff, 0x2f,
	})
}
