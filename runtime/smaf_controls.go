package runtime

import "math"

// Channel controllers have the same numbers in SMF and Mobile Standard SMAF.
func smafControllerKind(control int) (smafEventKind, bool) {
	switch control {
	case 0:
		return smafBankMSB, true
	case 1:
		return smafModulation, true
	case 6:
		return smafDataEntryMSB, true
	case 7:
		return smafVolume, true
	case 10:
		return smafPan, true
	case 11:
		return smafExpression, true
	case 32:
		return smafBankLSB, true
	case 38:
		return smafDataEntryLSB, true
	case 64:
		return smafHoldPedal, true
	case 98, 99:
		return smafNRPNSelect, true
	case 100:
		return smafRPNLSB, true
	case 101:
		return smafRPNMSB, true
	case 120:
		return smafAllSoundOff, true
	case 121:
		return smafResetControllers, true
	case 123:
		return smafAllNotesOff, true
	}
	return 0, false
}

// smafRegisteredParameter is the parameter a channel's Data Entry controllers
// address, chosen with CC101/CC100 (RPN) or CC99/CC98 (NRPN). Its zero value
// is the power-on state: nothing selected.
type smafRegisteredParameter struct {
	selected bool // an RPN was chosen and has not been replaced by an NRPN
	msb, lsb int
}

func (parameter *smafRegisteredParameter) selectParameter(kind smafEventKind, value int) {
	if kind == smafNRPNSelect {
		parameter.reset()
		return
	}
	if !parameter.selected {
		// The half of the number a score has not sent is still null.
		parameter.selected, parameter.msb, parameter.lsb = true, 127, 127
	}
	if kind == smafRPNMSB {
		parameter.msb = value & 0x7f
	} else {
		parameter.lsb = value & 0x7f
	}
}

func (parameter *smafRegisteredParameter) reset() {
	*parameter = smafRegisteredParameter{}
}

func (parameter smafRegisteredParameter) pitchBendSensitivity() bool {
	return parameter.selected && parameter.msb == 0 && parameter.lsb == 0
}

// smafBendSensitivity is RPN 0: how far a full pitch bend moves the pitch.
// Data Entry MSB is semitones and LSB cents. Its zero value is the default of
// two semitones, which a score that never sends RPN 0 plays with.
type smafBendSensitivity struct {
	sent      bool
	semitones int
	cents     int
}

func (sensitivity *smafBendSensitivity) set(msb bool, value int) {
	if !sensitivity.sent {
		sensitivity.sent, sensitivity.semitones, sensitivity.cents = true, 2, 0
	}
	if msb {
		sensitivity.semitones = value & 0x7f
	} else {
		sensitivity.cents = value & 0x7f
	}
}

func (sensitivity smafBendSensitivity) semitonesRange() float64 {
	if !sensitivity.sent {
		return 2
	}
	return float64(sensitivity.semitones) + float64(sensitivity.cents)/100
}

// applyBend converts the channel's last pitch bend into semitones at its
// current sensitivity. Scores commonly send RPN 0 = 12 or 24 and then bend by
// whole semitones; a fixed two-semitone range left those notes a fraction of
// the way to the written pitch.
func (channel *smafChannel) applyBend() {
	channel.bend = float64(channel.bendValue) / 8192 * channel.bendSensitivity.semitonesRange()
}

func (decoder *smafDecoder) updateChannelVolume(channel int) {
	state := &decoder.channels[channel]
	for index := range decoder.pool {
		voice := &decoder.pool[index]
		if voice.active && voice.channel == channel {
			voice.volume = state.volume * state.expression
		}
	}
	decoder.updatePCMVolume(channel)
}

func (decoder *smafDecoder) updateChannelPitch(channel int) {
	bend := decoder.channels[channel].bend
	for index := range decoder.pool {
		voice := &decoder.pool[index]
		if voice.active && voice.channel == channel {
			voice.setFrequency(smafNoteFrequency(voice.note) * math.Pow(2, bend/12))
		}
	}
	for index := range decoder.pcmPool {
		voice := &decoder.pcmPool[index]
		if voice.active && voice.sampled && voice.channel == channel {
			voice.setRate(decoder.rate, bend)
		}
	}
}

func (decoder *smafDecoder) setHoldPedal(channel int, hold bool) {
	decoder.channels[channel].holdPedal = hold
	if hold {
		return
	}
	for index := range decoder.pool {
		voice := &decoder.pool[index]
		if voice.active && voice.channel == channel {
			voice.releasePedal()
		}
	}
	for index := range decoder.pcmPool {
		voice := &decoder.pcmPool[index]
		if voice.active && voice.sampled && voice.channel == channel && voice.heldByPedal && !voice.keyDown {
			voice.heldByPedal = false
			voice.envelope.keyOff()
		}
	}
}

func (decoder *smafDecoder) allSoundOff(channel int) {
	// CC120 stops oscillators even after key release, including XOF and pedal
	// tails. CC123 only releases keys and follows the ordinary pedal/envelope.
	for index := range decoder.pool {
		voice := &decoder.pool[index]
		if voice.channel == channel {
			voice.active, voice.keyDown, voice.heldByPedal = false, false, false
			for op := range voice.operators {
				voice.operators[op].envelope.phase = smafEnvelopeIdle
				voice.operators[op].envelope.level = 0
			}
		}
	}
	for index := range decoder.pcmPool {
		voice := &decoder.pcmPool[index]
		if voice.channel == channel {
			voice.active, voice.keyDown, voice.heldByPedal = false, false, false
			voice.envelope.phase, voice.envelope.level = smafEnvelopeIdle, 0
		}
	}
}

func (decoder *smafDecoder) allNotesOff(channel int) {
	hold := decoder.channels[channel].holdPedal
	for index := range decoder.pool {
		voice := &decoder.pool[index]
		if voice.active && voice.keyDown && voice.channel == channel {
			voice.noteOffWithPedal(hold)
		}
	}
	for index := range decoder.pcmPool {
		voice := &decoder.pcmPool[index]
		if !voice.active || !voice.gated || voice.channel != channel {
			continue
		}
		if !voice.sampled {
			voice.active = false
		} else if voice.keyDown {
			voice.noteOffWithPedal(hold)
		}
	}
}
