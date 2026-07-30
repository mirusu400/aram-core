package runtime

type smafVoiceKey struct {
	bankMSB, bankLSB, program, drumNote int
}

type smafParsedVoice struct {
	key   smafVoiceKey
	patch smafPatch
	valid bool
}

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
		// Non-zero voice types reference sampled voices. The FM path safely
		// ignores them until their associated Mtsp wave bank is available.
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
	if len(data) >= 7 && data[1] == 0x03 {
		voice.key.bankLSB = int(data[3] & 0x7f)
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

func applySMAFVM35Voice(
	body []byte,
	operatorCount int,
	patch *smafPatch,
) {
	if len(body) < 3 {
		return
	}
	basicOctave := int(body[1] & 3)
	switch basicOctave {
	case 0:
		patch.noteShift = 12
	case 1:
		patch.noteShift = 0
	case 2:
		patch.noteShift = -12
	default:
		patch.noteShift = -24
	}
	if body[2]&(1<<5) != 0 {
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
