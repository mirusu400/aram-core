package runtime

type smafVoiceKey struct {
	bankMSB, bankLSB, program, drumNote int
}

type smafParsedVoice struct {
	key   smafVoiceKey
	patch smafPatch
	pcm   *smafPCMPatch
	valid bool
	// slot is the HandyPhone setup slot (the index byte of 43 02|03 <slot>
	// <bank> <program>) the voice was loaded into, for 43 03 91 to select.
	slot    int
	hasSlot bool
	// handyPhone marks the 43 02 and 43 03 forms, whose bank byte keeps bit 7.
	handyPhone bool
}

// parseSMAFVoice decodes the voice-definition exclusives the decoder knows:
// 43 79 06|07 7F 01 (MA-3/MA-5), 43 05 01, 43 03 (MA-2) and 43 02 (MA-1).
func parseSMAFVoice(data []byte) smafParsedVoice {
	voice := smafParsedVoice{patch: defaultSMAFPatch()}
	if len(data) < 5 || data[0] != 0x43 {
		return voice
	}
	if len(data) >= 11 && data[1] == 0x79 &&
		(data[2] == 0x06 || data[2] == 0x07) &&
		data[3] == 0x7f && data[4] == 0x01 {
		voice.key = smafVoiceKey{
			bankMSB:  int(data[5]),
			bankLSB:  int(data[6]),
			program:  int(data[7]),
			drumNote: int(data[8]),
		}
		if data[9] == 1 {
			body := data[10:]
			if data[2] == 0x06 {
				body = unpackSMAFMA3Bytes(body)
			}
			voice.pcm = parseSMAFPCMPatch(body)
			voice.valid = voice.pcm != nil
			return voice
		}
		if data[9] != 0 {
			return voice
		}
		body := data[10:]
		algorithmOffset := 2
		if data[2] == 0x06 {
			algorithmOffset = 3
		}
		algorithm := 0
		if len(body) > algorithmOffset {
			algorithm = int(body[algorithmOffset] & 7)
		}
		operatorCount := 4
		if algorithm <= 1 {
			operatorCount = 2
		}
		if data[2] == 0x06 {
			applySMAFMA3Voice(body, operatorCount, &voice.patch)
		} else {
			applySMAFVM35Voice(body, operatorCount, &voice.patch)
		}
		voice.valid = true
		return voice
	}
	if len(data) >= 6 && data[1] == 0x05 && data[2] == 0x01 {
		voice.key.bankLSB = int(data[3])
		voice.key.program = int(data[4])
		body := data[5:]
		algorithm := 0
		if len(body) >= 3 {
			algorithm = int(body[2] & 7)
		}
		operatorCount := 4
		if algorithm <= 1 {
			operatorCount = 2
		}
		applySMAFVM35Voice(body, operatorCount, &voice.patch)
		voice.valid = true
		return voice
	}
	if len(data) >= 5+smafMA1VoiceBytes && data[1] == 0x02 && data[2] < 0x10 {
		voice.slot, voice.hasSlot = int(data[2]), true
		voice.handyPhone = true
		voice.key.bankLSB = int(data[3])
		voice.key.program = int(data[4])
		applySMAFMA1Voice(data[5:], &voice.patch)
		voice.valid = true
		return voice
	}
	if len(data) >= 7 && data[1] == 0x03 {
		voice.slot, voice.hasSlot = int(data[2]&0x0f), data[2] < 0x10
		voice.handyPhone = true
		voice.key.bankLSB = int(data[3])
		voice.key.program = int(data[4])
		body := data[5:]
		if len(body) < 2 {
			return voice
		}
		feedback := (body[0] >> 3) & 7
		algorithm := body[0] & 7
		voice.patch.algorithm = algorithm
		voice.patch.fourOp = algorithm >= 2
		voice.patch.feedback = feedback
		voice.patch.lfo = body[0] >> 6 & 3
		operatorCount := 2
		if voice.patch.fourOp {
			operatorCount = 4
		}
		offset := 2
		for index := 0; index < operatorCount && offset+5 <= len(body); index++ {
			source := body[offset : offset+5]
			operator := &voice.patch.operators[index]
			operator.multi = source[0] >> 4 & 15
			operator.vib = source[0]&8 != 0
			egt := source[0]&4 != 0
			operator.ksr = source[0] & 1
			operator.rr = source[1] >> 4 & 15
			operator.dr = source[1] & 15
			operator.ar = source[2] >> 4 & 15
			operator.sl = source[2] & 15
			operator.tl = source[3] >> 2 & 63
			operator.ksl = source[3] & 3
			operator.am = source[4]&8 != 0
			operator.dvb = source[4] >> 6 & 3
			operator.dam = source[4] >> 4 & 3
			operator.wave = source[4] & 7
			if egt {
				operator.sr = 0
			} else {
				operator.sr = operator.rr
			}
			operator.egType = operator.sr != 0
			if index == 0 {
				operator.fb = feedback
			}
			offset += 5
		}
		voice.valid = true
	}
	return voice
}

// smafBasicOctaveShift is the note shift a voice's two-bit basic octave (BO)
// asks for: BO 1 plays the written note, BO 0 an octave above, BO 2 and 3 one
// and two octaves below.
func smafBasicOctaveShift(value byte) int {
	return 12 * (1 - int(value&3))
}

// smafMA1VoiceBytes is the body of an MA-1 voice definition, F0 13 43 02
// <slot> <bank> <program> <body> F7: a header byte, the two YMU757 operator
// words and four trailing bytes.
const smafMA1VoiceBytes = 13

// applySMAFMA1Voice decodes the body of an MA-1 setup voice, 43 02 <slot>
// <bank> <program> <13 bytes>.
//
// After one header byte the body holds the YMU757 (MA-1) timbre registers
// verbatim: the modulator's two 16-bit words, then the carrier's, big-endian,
// laid out as the chip's datasheet gives them -
//
//	even: ML2-0 VIB EGT SUS RR3-0 DR3-0 AR3-2
//	odd:  AR1-0 SL3-0 TL5-0 WAV FL2-0
//
// The SPH-X4209, an MA-2 handset, pins the layout. Its score streamer
// (firmware 0x142c34) copies these definitions to the chip byte by byte except
// for one fix-up of the carrier's EGT bit (payload byte 10, bit 3) and the
// four RR bits that straddle bytes 10 and 11: a damped carrier (EGT 0) whose
// RR is 5 or slower is rewritten as sustained with RR 9. That is the MA-1's
// own damped envelope restated for the MA-2: the YMU757 decays a damped sound
// from SL at RR and cuts it 286 ms (RR 9) after the note ends. Across the
// corpus's 29 distinct definitions this reading gives every carrier TL 0 and
// feedback 0, every sustained instrument EGT 1, and the low two header bits
// track the carrier's multiple: 0 under each x1/2 bass carrier and 2 under
// each x2 one, the basic octave the MA-3 voice format also carries, which
// brings both back to the written pitch.
//
// The rest of the header and the four trailing bytes do not map to a YMU757
// register and are not decoded.
func applySMAFMA1Voice(body []byte, patch *smafPatch) {
	patch.noteShift = smafBasicOctaveShift(body[0])
	patch.algorithm = 0
	patch.fourOp = false
	// The YMU757 vibrato runs at 6.4 Hz with a depth of 13.47 cents: LFO 2
	// (6 Hz) at depth 2 (13.4 cents) on this engine.
	patch.lfo = 2
	for index := range 2 {
		source := body[1+index*4 : 5+index*4]
		even := uint16(source[0])<<8 | uint16(source[1])
		odd := uint16(source[2])<<8 | uint16(source[3])
		operator := &patch.operators[index]
		*operator = smafOpPatch{
			multi:     uint8(even >> 13),
			vib:       even>>12&1 != 0,
			dvb:       2,
			dr:        uint8(even >> 2 & 15),
			ar:        uint8(even&3<<2 | odd>>14),
			sl:        uint8(odd >> 10 & 15),
			tl:        uint8(odd >> 4 & 63),
			wave:      uint8(odd >> 3 & 1),
			fb:        uint8(odd & 7),
			egType:    true,
			ma1Timing: true,
		}
		rr := uint8(even >> 6 & 15)
		damped := even>>11&1 == 0
		// A sustained sound holds SL until the note ends and then releases at
		// RR. A damped one decays from SL at RR while the note sounds and is
		// cut in 286 ms (RR 9) once it ends. SUS slows either release to RR 6.
		operator.rr = rr
		if damped {
			operator.sr = rr
			operator.rr = 9
		}
		if even>>10&1 != 0 {
			operator.rr = 6
		}
	}
	patch.feedback = patch.operators[0].fb
}

func applySMAFVM35Voice(
	body []byte,
	operatorCount int,
	patch *smafPatch,
) {
	if len(body) < 3 {
		return
	}
	patch.noteShift = smafBasicOctaveShift(body[1])
	patch.panFixed = body[2]&(1<<5) != 0
	if patch.panFixed {
		panpot := int(body[1] >> 3 & 31)
		switch {
		case panpot == 15:
			patch.panDefault = 0
		case panpot < 15:
			patch.panDefault = -float64(15-panpot) / 15
		default:
			patch.panDefault = float64(panpot-15) / 16
		}
	}
	patch.algorithm = body[2] & 7
	patch.fourOp = patch.algorithm >= 2
	patch.lfo = body[2] >> 6 & 3
	offset := 3
	for index := 0; index < min(operatorCount, 4) &&
		offset+7 <= len(body); index++ {
		source := body[offset : offset+7]
		operator := &patch.operators[index]
		operator.sr = source[0] >> 4 & 15
		operator.ksr = source[0] & 1
		operator.egType = operator.sr != 0
		operator.rr = source[1] >> 4 & 15
		operator.dr = source[1] & 15
		operator.ar = source[2] >> 4 & 15
		operator.sl = source[2] & 15
		operator.tl = source[3] >> 2 & 63
		operator.ksl = source[3] & 3
		operator.xof = source[0]&8 != 0
		operator.sus = source[0]&2 != 0
		operator.am = source[4]>>4&1 != 0
		operator.dam = source[4] >> 5 & 3
		operator.vib = source[4]&1 != 0
		operator.dvb = source[4] >> 1 & 3
		operator.multi = source[5] >> 4 & 15
		operator.dt = source[5] & 7
		operator.wave = source[6] >> 3 & 31
		operator.fb = source[6] & 7
		if index == 0 {
			patch.feedback = operator.fb
		}
		offset += 7
	}
}

func applySMAFMA3Voice(body []byte, operatorCount int, patch *smafPatch) {
	var raw [36]byte
	copy(raw[:], body)
	raw[2] |= raw[0] << 2 & 0x80
	raw[3] |= raw[0] << 3 & 0x80
	for index := 0; index < 4; index++ {
		raw[4+index*8] |= raw[index*8] << 4 & 0x80
		raw[5+index*8] |= raw[index*8] << 5 & 0x80
		raw[6+index*8] |= raw[index*8] << 6 & 0x80
		raw[7+index*8] |= raw[index*8] << 7 & 0x80
		raw[10+index*8] |= raw[8+index*8] << 2 & 0x80
		raw[11+index*8] |= raw[8+index*8] << 3 & 0x80
	}
	fixed := make([]byte, 3, 31)
	copy(fixed, raw[1:4])
	for index := 0; index < 4; index++ {
		fixed = append(fixed, raw[4+index*8:8+index*8]...)
		fixed = append(fixed, raw[9+index*8:12+index*8]...)
	}
	applySMAFVM35Voice(fixed, operatorCount, patch)
}
