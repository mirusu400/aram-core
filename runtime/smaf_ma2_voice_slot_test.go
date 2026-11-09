package runtime

import (
	"encoding/binary"
	"math"
	"testing"
)

// smafMA2SetupVoice is a HandyPhone setup voice, F0 <len> 43 03 <slot> <bank>
// <program> <body> F7: one silent modulator feeding one sine carrier at the
// given frequency multiple.
func smafMA2SetupVoice(slot, program, carrierMultiple byte) []byte {
	return smafMA2SetupBankVoice(slot, 0, program, carrierMultiple)
}

func smafMA2SetupBankVoice(slot, bank, program, carrierMultiple byte) []byte {
	body := []byte{
		0x00, 0x00, // LFO/feedback/algorithm 0, reserved
		0x14, 0x00, 0xf0, 0xfc, 0x00, // modulator: multiple 1, sustaining, TL 63 (silent)
		carrierMultiple<<4 | 0x04, 0x00, 0xf0, 0x00, 0x00, // carrier: AR 15, sustaining, TL 0
	}
	payload := append([]byte{0x43, 0x03, slot, bank, program}, body...)
	voice := []byte{0xf0, byte(len(payload) + 1)}
	voice = append(voice, payload...)
	return append(voice, 0xf7)
}

func smafMA2VoiceSlotScore(slotWrite bool) []byte {
	setup := append(smafMA2SetupVoice(0, 0, 1), smafMA2SetupVoice(1, 5, 2)...)
	sequence := []byte{0x00, 0x00, 0x30, 0x00} // channel 0 program 0: slot 0's voice
	if slotWrite {
		// 43 03 91 18 01: chip channel 0 (low nibble) plays slot 1.
		sequence = append(sequence, 0x00, 0xff, 0xf0, 0x06, 0x43, 0x03, 0x91, 0x18, 0x01, 0xf7)
	}
	sequence = append(sequence, 0x00, 0x20, 0x81, 0x7a, 0x81, 0x7a, 0x00, 0x00, 0x00) // C4 for 1 s
	trackBody := []byte{0x00, 0x00, 0x02, 0x02, 0x00, 0x00}
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsu"), setup)
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsq"), sequence)
	file := appendSMAFChunk(nil, []byte{'M', 'T', 'R', 1}, trackBody)
	data := make([]byte, 8)
	copy(data, "MMMD")
	binary.BigEndian.PutUint32(data[4:], uint32(len(file)))
	return append(data, file...)
}

func smafMA2Partial(t *testing.T, data []byte, frequency float64) float64 {
	t.Helper()
	pcm := decodeSMAFPCM16(data, 44_100)
	if pcm == nil {
		t.Fatal("score did not decode")
	}
	start, end := int(0.2*44_100), int(0.8*44_100)
	window := make([]float64, end-start)
	for frame := range window {
		hann := 0.5 - 0.5*math.Cos(2*math.Pi*float64(frame)/float64(len(window)-1))
		window[frame] = hann * float64(pcm.samples[(start+frame)*2]) / 32768
	}
	return toneMagnitude(window, 44_100, frequency)
}

// TestSMAFMA2VoiceSlotWriteSelectsTheSetupVoice pins 43 03 91. On MA-2
// hardware the score drives the chip directly, and registers 0x18-0x1F give
// each chip channel (one nibble each) the setup voice slot it plays: the
// index byte of a 43 03 <slot> <bank> <program> definition. A later write
// overrides the channel's program change, as the register it lands in does.
func TestSMAFMA2VoiceSlotWriteSelectsTheSetupVoice(t *testing.T) {
	c4 := smafNoteFrequency(60)
	plain := smafMA2VoiceSlotScore(false)
	if fundamental, octave := smafMA2Partial(t, plain, c4), smafMA2Partial(t, plain, 2*c4); fundamental < 4*octave {
		t.Fatalf("program 0 should play slot 0's voice at the fundamental: %.4f vs octave %.4f", fundamental, octave)
	}
	slotted := smafMA2VoiceSlotScore(true)
	if fundamental, octave := smafMA2Partial(t, slotted, c4), smafMA2Partial(t, slotted, 2*c4); octave < 4*fundamental {
		t.Fatalf("slot write should switch channel 0 to slot 1's voice an octave up: fundamental %.4f, octave %.4f", fundamental, octave)
	}
}

// TestSMAFMA2DrumBankVoiceStaysApartFromBankZero pins the HandyPhone bank
// byte. Bank 0x80 (bit 7 set, the drum bank) and bank 0 are different banks:
// a score that defines a voice under each with the same program number must
// get the one its bank select names, not whichever was defined last.
func TestSMAFMA2DrumBankVoiceStaysApartFromBankZero(t *testing.T) {
	setup := append(smafMA2SetupBankVoice(0, 0x80, 5, 2), smafMA2SetupBankVoice(1, 0x00, 5, 1)...)
	sequence := []byte{
		0x00, 0x00, 0x31, 0x80, // channel 0 bank select 0x80
		0x00, 0x00, 0x30, 0x05, // program 5
		0x00, 0x20, 0x81, 0x7a, 0x81, 0x7a, 0x00, 0x00, 0x00, // C4 for 1 s
	}
	trackBody := []byte{0x00, 0x00, 0x02, 0x02, 0x00, 0x00}
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsu"), setup)
	trackBody = appendSMAFChunk(trackBody, []byte("Mtsq"), sequence)
	file := appendSMAFChunk(nil, []byte{'M', 'T', 'R', 1}, trackBody)
	data := make([]byte, 8)
	copy(data, "MMMD")
	binary.BigEndian.PutUint32(data[4:], uint32(len(file)))
	data = append(data, file...)
	c4 := smafNoteFrequency(60)
	if fundamental, octave := smafMA2Partial(t, data, c4), smafMA2Partial(t, data, 2*c4); octave < 4*fundamental {
		t.Fatalf("bank 0x80 program 5 played bank 0's voice: fundamental %.4f, octave %.4f", fundamental, octave)
	}
}
