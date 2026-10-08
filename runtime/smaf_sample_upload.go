package runtime

// MA-3 exclusive parameters and sample bytes pack seven low bytes behind one
// MSB bitmap, ordered from bit 6 to bit 0. MA-5 stores the same parameters raw.
func unpackSMAFMA3Bytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	result := make([]byte, 0, len(data)-((len(data)+7)/8))
	for offset := 0; offset < len(data); {
		flags := data[offset]
		offset++
		count := min(7, len(data)-offset)
		if flags&0x80 != 0 || count == 0 || flags&byte((1<<(7-count))-1) != 0 {
			return nil
		}
		for index := 0; index < count; index++ {
			value := data[offset+index]
			if value&0x80 != 0 {
				return nil
			}
			result = append(result, value|((flags>>uint(6-index))&1)<<7)
		}
		offset += count
	}
	return result
}

// A native sampled-wave upload is 43 79 device 7f 03 WaveID Format Data.
// The playback rate and loop/end points come from the linked PCM instrument,
// rather than a stream-wave header. Keep this bank separate from Mtsp/Mwa.
func parseSMAFSampledWave(data []byte) smafWave {
	if len(data) < 8 || data[0] != 0x43 || data[1] != 0x79 ||
		(data[2] != 6 && data[2] != 7) || data[3] != 0x7f || data[4] != 3 ||
		data[5] > 127 || data[6] > 1 {
		return smafWave{}
	}
	bytes := data[7:]
	if data[2] == 6 {
		bytes = unpackSMAFMA3Bytes(bytes)
	}
	format, bits := 2, 0 // mono Yamaha ADPCM
	if data[6] == 1 {
		format, bits = 0, 1 // mono signed PCM8
	}
	return decodeSMAFSamples(int(data[5]), 8000, 1, format, bits, bytes)
}

func (decoder *smafDecoder) setVoice(voice smafParsedVoice) {
	for index := range decoder.voices {
		if decoder.voices[index].key == voice.key {
			decoder.voices[index] = voice
			return
		}
	}
	decoder.voices = append(decoder.voices, voice)
}

func (decoder *smafDecoder) setSampledWave(base, count int, wave smafWave) {
	for channel := base; channel < base+count && channel < len(decoder.channels); channel++ {
		waves := &decoder.channels[channel].sampledWaves
		replaced := false
		for index := range *waves {
			if (*waves)[index].number == wave.number {
				(*waves)[index] = wave
				replaced = true
				break
			}
		}
		if !replaced {
			*waves = append(*waves, wave)
		}
	}
}

func (decoder *smafDecoder) decodeExclusive(data []byte, base, count int, at uint64, timed bool) {
	if voice := parseSMAFVoice(data); voice.valid {
		if !timed {
			decoder.setVoice(voice)
			return
		}
		index := len(decoder.voiceChanges)
		if decoder.addEvent(smafEvent{sample: at, kind: smafVoiceChange, channel: base, a: index}) {
			decoder.voiceChanges = append(decoder.voiceChanges, voice)
		}
	} else if wave := parseSMAFSampledWave(data); len(wave.pcm) != 0 {
		if !timed {
			decoder.setSampledWave(base, count, wave)
			return
		}
		index := len(decoder.waveChanges)
		if decoder.addEvent(smafEvent{sample: at, kind: smafWaveChange, channel: base, a: index, b: count}) {
			decoder.waveChanges = append(decoder.waveChanges, wave)
		}
	}
}

func (decoder *smafDecoder) hasPCMWaves() bool {
	if len(decoder.waveChanges) != 0 {
		return true
	}
	for _, track := range decoder.tracks {
		if len(track.waves) != 0 {
			return true
		}
	}
	for index := range decoder.channels {
		if len(decoder.channels[index].sampledWaves) != 0 {
			return true
		}
	}
	return false
}

func (decoder *smafDecoder) hasSampledVoices() bool {
	for _, voices := range [][]smafParsedVoice{decoder.voices, decoder.voiceChanges} {
		for _, voice := range voices {
			if voice.pcm != nil && voice.pcm.ram {
				return true
			}
		}
	}
	return false
}
