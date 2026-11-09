package runtime

import (
	"encoding/binary"
	"math"
	"testing"
)

// smafMA1Definition is an MA-1 setup voice, F0 13 43 02 <slot> 00 <program>
// <header> <modulator words> <carrier words> <4 trailing bytes> F7.
func smafMA1Definition(slot, program, header byte, modulator, carrier [4]byte) []byte {
	payload := []byte{0x43, 0x02, slot, 0x00, program, header}
	payload = append(payload, modulator[:]...)
	payload = append(payload, carrier[:]...)
	payload = append(payload, 0, 0, 0, 0)
	voice := []byte{0xf0, byte(len(payload) + 1)}
	voice = append(voice, payload...)
	return append(voice, 0xf7)
}

// smafMA1Score plays C4 on channel 0 for 0.5 s on program 0, defined by the
// given MA-1 voice, and ends at 1 s.
func smafMA1Score(definition []byte) []byte {
	sequence := []byte{
		0x00, 0x00, 0x30, 0x00, // channel 0 program 0
		0x00, 0x20, 0x7d, // C4, gate 125 x 4 ms
		0x80, 0x7a, 0x00, 0x00, 0x00, // end at 250 x 4 ms
	}
	trackBody := []byte{0x00, 0x00, 0x02, 0x02, 0x00, 0x00}
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsu"), definition)
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsq"), sequence)
	file := appendSMAFChunk(nil, []byte{'M', 'T', 'R', 1}, trackBody)
	data := make([]byte, 8)
	copy(data, "MMMD")
	binary.BigEndian.PutUint32(data[4:], uint32(len(file)))
	return append(data, file...)
}

// A silent modulator: ML 1, sustained, AR 15, SL 0, TL 63.
var smafMA1SilentModulator = [4]byte{0x28, 0x03, 0xc3, 0xf0}

// TestSMAFMA1VoiceReadsTheYMU757TimbreWords pins the 43 02 body layout: a
// header byte, then each operator as the YMU757 datasheet's two timbre
// register words, big-endian -
//
//	even: ML2-0 VIB EGT SUS RR3-0 DR3-0 AR3-2
//	odd:  AR1-0 SL3-0 TL5-0 WAV FL2-0
//
// with the basic octave in the header's low two bits.
func TestSMAFMA1VoiceReadsTheYMU757TimbreWords(t *testing.T) {
	definition := smafMA1Definition(1, 0x47, 0x22,
		// ML 3, VIB, damped, RR 5, DR 14, AR 13, SL 2, TL 24, half sine, FL 6.
		[4]byte{0x71, 0x7b, 0x49, 0x8e},
		// ML 2, sustained, SUS, RR 7, DR 4, AR 15, SL 1, TL 0, sine, FL 0.
		[4]byte{0x4d, 0xd3, 0xc4, 0x00},
	)
	voice := parseSMAFVoice(definition[2 : len(definition)-1])
	if !voice.valid || !voice.handyPhone || !voice.hasSlot || voice.slot != 1 ||
		voice.key.program != 0x47 {
		t.Fatalf("voice = %+v, want a valid HandyPhone slot 1 voice for program 0x47", voice)
	}
	patch := voice.patch
	if patch.fourOp || patch.algorithm != 0 || patch.noteShift != -12 || patch.feedback != 6 {
		t.Fatalf("patch fourOp=%v algorithm=%d noteShift=%d feedback=%d, want 2-op FM, -12, 6",
			patch.fourOp, patch.algorithm, patch.noteShift, patch.feedback)
	}
	modulator := smafOpPatch{
		multi: 3, vib: true, dvb: 2, dr: 14, ar: 13, sl: 2, tl: 24, wave: 1, fb: 6,
		// Damped: RR decays from SL while the note sounds, then the note's
		// end cuts it at RR 9.
		sr: 5, rr: 9, egType: true, ma1Timing: true,
	}
	carrier := smafOpPatch{
		multi: 2, dvb: 2, dr: 4, ar: 15, sl: 1,
		// Sustained: holds SL; SUS turns its RR 7 release into RR 6.
		sr: 0, rr: 6, egType: true, ma1Timing: true,
	}
	if patch.operators[0] != modulator {
		t.Errorf("modulator = %+v\nwant       %+v", patch.operators[0], modulator)
	}
	if patch.operators[1] != carrier {
		t.Errorf("carrier = %+v\nwant     %+v", patch.operators[1], carrier)
	}
}

// TestSMAFMA1BasicOctaveRestoresTheWrittenPitch pins the header's basic
// octave. MA-1 voices for high instruments put an x2 carrier under BO 2 and
// bass voices an x1/2 carrier under BO 0, so the voice sounds at the written
// note; BO 1 plays the carrier's multiple as it is.
func TestSMAFMA1BasicOctaveRestoresTheWrittenPitch(t *testing.T) {
	// ML 2 (x2), sustained, AR 15, SL 0, TL 0.
	carrier := [4]byte{0x48, 0x03, 0xc0, 0x00}
	c4 := smafNoteFrequency(60)
	written := smafMA1Score(smafMA1Definition(0, 0, 0x22, smafMA1SilentModulator, carrier))
	if fundamental, octave := smafMA2Partial(t, written, c4), smafMA2Partial(t, written, 2*c4); fundamental < 4*octave {
		t.Fatalf("BO 2 x2 carrier should sound at the written C4: %.4f vs octave %.4f", fundamental, octave)
	}
	plain := smafMA1Score(smafMA1Definition(0, 0, 0x21, smafMA1SilentModulator, carrier))
	if fundamental, octave := smafMA2Partial(t, plain, c4), smafMA2Partial(t, plain, 2*c4); octave < 4*fundamental {
		t.Fatalf("BO 1 x2 carrier should sound an octave up: fundamental %.4f, octave %.4f", fundamental, octave)
	}
}

func smafMA1WindowRMS(t *testing.T, data []byte, from, to float64) float64 {
	t.Helper()
	pcm := decodeSMAFPCM16(data, 44_100)
	if pcm == nil {
		t.Fatal("score did not decode")
	}
	start, end := int(from*44_100), int(to*44_100)
	if len(pcm.samples)/2 < end {
		t.Fatalf("score ends after %d frames", len(pcm.samples)/2)
	}
	var sum float64
	for frame := start; frame < end; frame++ {
		value := float64(pcm.samples[frame*2])
		sum += value * value
	}
	return math.Sqrt(sum / float64(end-start))
}

// TestSMAFMA1EnvelopeFollowsTheYMU757 pins the MA-1 envelope as the YMU757
// datasheet defines it, on its own rate scale: a damped sound (EGT 0) decays
// from SL at RR while the note sounds and dies 286 ms (RR 9) after the note
// ends; a sustained one (EGT 1) holds SL and releases at RR; SUS slows either
// release to RR 6, 2.29 s.
func TestSMAFMA1EnvelopeFollowsTheYMU757(t *testing.T) {
	// Carriers: ML 1, AR 15, DR 0, SL 0, TL 0, with EGT, SUS and RR varied.
	damped := [4]byte{0x20, 0x03, 0xc0, 0x00}         // EGT 0, RR 0
	dampedSUS := [4]byte{0x24, 0x03, 0xc0, 0x00}      // EGT 0, SUS, RR 0
	dampedDecaying := [4]byte{0x22, 0x43, 0xc0, 0x00} // EGT 0, RR 9
	sustained := [4]byte{0x28, 0xc3, 0xc0, 0x00}      // EGT 1, RR 3
	sustainedRR9 := [4]byte{0x2a, 0x43, 0xc0, 0x00}   // EGT 1, RR 9
	level := func(carrier [4]byte, from, to float64) float64 {
		return smafMA1WindowRMS(t,
			smafMA1Score(smafMA1Definition(0, 0, 0x21, smafMA1SilentModulator, carrier)),
			from, to)
	}
	held := level(sustained, 0.2, 0.4)
	if held < 100 {
		t.Fatalf("reference note too quiet to measure: rms %.1f", held)
	}
	// 150-250 ms after the note ends at 0.5 s.
	if after := level(sustained, 0.65, 0.75) / held; after < 0.9 {
		t.Errorf("sustained RR 3 release lost %.2f of its level in 250 ms; RR 3 takes 18 s", after)
	}
	if after := level(damped, 0.65, 0.75) / level(damped, 0.2, 0.4); after > 0.1 {
		t.Errorf("damped note kept %.2f of its level 250 ms after it ended; the YMU757 cuts it in 286 ms", after)
	}
	if after := level(dampedSUS, 0.65, 0.75) / level(dampedSUS, 0.2, 0.4); after < 0.4 || after > 0.85 {
		t.Errorf("damped SUS note kept %.2f of its level 250 ms after it ended, want RR 6's slow fade", after)
	}
	// While the note sounds: damped RR 9 decays from SL, sustained RR 9 holds.
	if late := level(dampedDecaying, 0.4, 0.48) / level(dampedDecaying, 0.02, 0.1); late > 0.1 {
		t.Errorf("damped RR 9 note kept %.2f of its level 0.4 s in; RR 9 decays 48 dB in 286 ms", late)
	}
	if late := level(sustainedRR9, 0.4, 0.48) / level(sustainedRR9, 0.02, 0.1); late < 0.9 {
		t.Errorf("sustained RR 9 note lost %.2f of its level while held", late)
	}
}
