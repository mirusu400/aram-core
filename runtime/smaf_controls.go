package runtime

import "math"

// Channel controllers have the same numbers in SMF and Mobile Standard SMAF.
func smafControllerKind(control int) (smafEventKind, bool) {
	switch control {
	case 0:
		return smafBankMSB, true
	case 1:
		return smafModulation, true
	case 7:
		return smafVolume, true
	case 10:
		return smafPan, true
	case 11:
		return smafExpression, true
	case 32:
		return smafBankLSB, true
	case 64:
		return smafHoldPedal, true
	case 120:
		return smafAllSoundOff, true
	case 121:
		return smafResetControllers, true
	case 123:
		return smafAllNotesOff, true
	}
	return 0, false
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
