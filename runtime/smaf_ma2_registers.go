package runtime

import "math"

// smafMA2Registers follows a HandyPhone (MA-2) sequence's direct chip writes,
// FF F0 <length> 43 03 90|91 <register> <value> F7.
//
// A title can sound a channel's voice without sending notes by writing the
// chip's frequency registers from the sequence: 0xB0+ch holds the low eight
// F-Number bits and 0xC0+ch the key-on bit (5), the block (4-2) and the top
// two F-Number bits (1-0). Writing the F-Number while the key is on bends the
// sounding voice, which is how pitch-sweep sound effects are built; 56
// sequences in the test corpus consist of nothing else and played silent.
//
// The channel number is the chip's: score track n (1-4) only ever writes
// channels 4(n-1) to 4(n-1)+3, the same four its own notes use, so the low two
// bits select the channel within the track. The F-Numbers keyed in the corpus
// land on an equal-tempered ladder that f = F-Number * 49716 / 2^(20-block)
// puts within a few cents of concert pitch, in the octave the same channel's
// notes use. The SPH-W8300 firmware agrees: its handler (0x15bf8f0) turns
// the registers into a MIDI note of block*12+13 (+6 when F-Number bit 8 is
// clear) refined by pitch bend, which puts block 4 F-Number 365 on C#4 and
// 516 on G4, the pitches this formula gives.
type smafMA2Registers struct {
	fnumLow [16]int
	control [16]int
	keyed   [16]bool
	noteID  [16]uint32
}

const (
	smafMA2RegisterCommand = 0x90
	// smafMA2VoiceSlotCommand is 43 03 91 <0x18+r> <value>: each nibble of
	// register 0x18+r names the setup voice slot (the index byte of a
	// 43 03 <slot> <bank> <program> voice) that chip channel 2r (low nibble)
	// or 2r+1 (high nibble) plays; score track n writes only the two
	// registers for its four channels, and the default is the channel's own
	// number.
	//
	// On MA-2 hardware the score drives the chip itself: the SPH-X4209's
	// player (firmware 0x142c34) streams the exclusives straight into the
	// chip, so the write lands in the register that picks the channel's
	// voice. It is applied that way here, last writer wins against the
	// channel's program change. In the corpus the slot a channel is given
	// names the bank and program of its own program change in 4,111 of 4,145
	// cases, so it mostly agrees; an MA-5 handset (SPH-W8300, 0x15bfaa8)
	// that emulates MA-2 content in software skips it.
	smafMA2VoiceSlotCommand = 0x91
)

func smafMA2RegisterFrequency(fnum, block int) float64 {
	return float64(fnum) * 49716 / math.Exp2(float64(20-block))
}

// write applies one register write at sample at. It reports false for a
// payload that is not a register write, which the caller decodes as before.
func (registers *smafMA2Registers) write(
	decoder *smafDecoder,
	payload []byte,
	base int,
	at uint64,
) bool {
	if len(payload) != 5 || payload[0] != 0x43 || payload[1] != 0x03 {
		return false
	}
	if payload[2] == smafMA2VoiceSlotCommand {
		register, value := int(payload[3]), int(payload[4])
		if register < 0x18 || register > 0x1f {
			return true
		}
		for half, chip := range [2]int{(register - 0x18) * 2, (register-0x18)*2 + 1} {
			decoder.addEvent(smafEvent{
				sample: at, kind: smafVoiceSlot, channel: base + chip&3,
				a: value >> (4 * half) & 15,
			})
		}
		return true
	}
	if payload[2] != smafMA2RegisterCommand {
		return false
	}
	register, value := int(payload[3]), int(payload[4])
	chip := register & 15
	channel := base + chip&3
	switch register & 0xf0 {
	case 0xb0:
		registers.fnumLow[chip] = value
	case 0xc0:
		registers.control[chip] = value
	default:
		return true
	}
	control := registers.control[chip]
	frequency := smafMA2RegisterFrequency(
		(control&3)<<8|registers.fnumLow[chip],
		control>>2&7,
	)
	keyOn := control&0x20 != 0
	switch {
	case keyOn && !registers.keyed[chip]:
		if register&0xf0 != 0xc0 {
			return true
		}
		registers.keyed[chip] = true
		registers.noteID[chip] = uint32(len(decoder.events) + 1)
		decoder.addEvent(smafEvent{
			sample: at, kind: smafDirectKeyOn, channel: channel,
			noteID: registers.noteID[chip], a: smafMilliHertz(frequency),
		})
	case keyOn:
		decoder.addEvent(smafEvent{
			sample: at, kind: smafDirectPitch, channel: channel,
			noteID: registers.noteID[chip], a: smafMilliHertz(frequency),
		})
	case registers.keyed[chip]:
		registers.release(decoder, chip, channel, at)
	}
	return true
}

func (registers *smafMA2Registers) release(decoder *smafDecoder, chip, channel int, at uint64) {
	registers.keyed[chip] = false
	decoder.addEvent(smafEvent{
		sample: at, kind: smafDirectKeyOff, channel: channel,
		noteID: registers.noteID[chip],
	})
}

// releaseAll keys off whatever the sequence left keyed when it ends, so a
// voice cannot hold the render open to its ceiling.
func (registers *smafMA2Registers) releaseAll(decoder *smafDecoder, base int, at uint64) {
	for chip := range registers.keyed {
		if registers.keyed[chip] {
			registers.release(decoder, chip, base+chip&3, at)
		}
	}
}

func smafMilliHertz(frequency float64) int {
	return int(math.Round(frequency * 1000))
}

// startDirectVoice keys an FM voice at an explicit frequency, the chip-level
// counterpart of a note-on. It plays the channel's current voice, ignores the
// channel's octave shift and pitch bend (the register already is the pitch),
// and is matched only by its own key-off.
func (decoder *smafDecoder) startDirectVoice(event smafEvent) {
	channel := &decoder.channels[event.channel]
	embedded := decoder.resolveVoice(*channel, -1)
	patch := gmSMAFPatch(channel.program)
	if embedded != nil && embedded.pcm == nil {
		patch = embedded.patch
	}
	slot := decoder.allocateFMVoice()
	frequency := float64(event.a) / 1000
	voice := &decoder.pool[slot]
	voice.sampleRate = float64(decoder.rate)
	voice.channel = event.channel
	voice.note = -1
	voice.keyNote = -1
	voice.direct = true
	voice.noteID = event.noteID
	voice.volume = channel.volume * channel.expression
	voice.noteOn(patch, frequency, 1)
	voice.setModulation(channel.modulation)
	voice.pan = channel.pan
	if patch.panFixed {
		voice.pan = patch.panDefault
	}
	decoder.listFMVoice(slot)
}

func (decoder *smafDecoder) directVoice(event smafEvent) *smafVoice {
	for index := range decoder.pool {
		voice := &decoder.pool[index]
		if voice.active && voice.direct && voice.keyDown &&
			voice.channel == event.channel && voice.noteID == event.noteID {
			return voice
		}
	}
	return nil
}
