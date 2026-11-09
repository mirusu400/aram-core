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
	if voice.hasSlot {
		decoder.ma2Slots[voice.slot] = voice
	}
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
	if volume, ok := parseSMAFMasterVolume(data); ok {
		if !timed {
			decoder.masterVolume.set(volume)
			return
		}
		decoder.addEvent(smafEvent{sample: at, kind: smafMasterVolumeChange, channel: base, a: volume})
		return
	}
	if channel, level, ok := parseSMAFStreamLevel(data); ok {
		if channel < 1 || base+channel-1 >= len(decoder.channels) {
			return
		}
		if !timed {
			decoder.channels[base+channel-1].streamLevel.set(level)
			return
		}
		decoder.addEvent(smafEvent{sample: at, kind: smafStreamLevelChange, channel: base + channel - 1, a: level})
		return
	}
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

// parseSMAFMasterVolume recognises the MA-3/MA-5 master volume exclusive,
// 43 79 06|07 7F 00 vv. Titles send it once at the top of a score, with values
// from 80 to 127 in the corpus, to set that score's level against the rest of
// the title's sound.
func parseSMAFMasterVolume(data []byte) (int, bool) {
	if len(data) != 6 || data[0] != 0x43 || data[1] != 0x79 ||
		(data[2] != 6 && data[2] != 7) || data[3] != 0x7f || data[4] != 0 ||
		data[5] > 127 {
		return 0, false
	}
	return int(data[5]), true
}

// smafMasterVolume scales the whole mix. The zero value is full scale: a score
// that never sends the message plays exactly as before.
//
// The law is the handset's. The SPH-W8300's SMAF library (firmware 0x15c0b54)
// forwards the message to its synthesizer as the GM master volume
// F0 7F 7F 04 01 00 vv F7 and sets its audio slots to (vv+1)^2, so 127 is
// unity and the gain falls with the square of the value, as GM volume does.
type smafMasterVolume struct {
	sent  bool
	value int
}

func (volume *smafMasterVolume) set(value int) {
	volume.sent, volume.value = true, value&0x7f
}

func (volume smafMasterVolume) gain() float64 {
	if !volume.sent {
		return 1
	}
	scale := float64(volume.value+1) / 128
	return scale * scale
}

// smafStreamFullLevel is full scale for a 7F 0B stream level.
const smafStreamFullLevel = 0x80

// parseSMAFStreamLevel recognises 43 79 06|07 7F 0B <channel> <mode> <value>,
// the stream audio level of one channel (counted from 1), and returns that
// level out of smafStreamFullLevel. About 330 corpus scores send it, nearly
// all as mode 2 or as mode 0 with value 0x40.
//
// For MA-5 scores (device 07) the SPH-W8300's SMAF library (firmware
// 0x15c0c58) is the reference: the channel's audio slots play at the full
// master level when mode is 2 and at half of it otherwise, and the value byte
// is never read. That library has no handler for the MA-3 form (device 06);
// the SPH-E3700's does (firmware 0x29313c). It turns the message into the
// sound driver's stream-volume command (0x16, beside stream on, off and
// pairing), passing 0x80 for mode 2, 0xFF for mode 1 and the value byte for
// any other mode; the value above full scale is held at full here. The two
// agree on everything the corpus sends: mode 2 is full and mode 0 with 0x40
// is half.
func parseSMAFStreamLevel(data []byte) (channel, level int, ok bool) {
	if len(data) != 8 || data[0] != 0x43 || data[1] != 0x79 ||
		(data[2] != 6 && data[2] != 7) || data[3] != 0x7f || data[4] != 0x0b {
		return 0, 0, false
	}
	channel, mode := int(data[5]), int(data[6])
	switch {
	case mode == 2:
		level = smafStreamFullLevel
	case data[2] == 7:
		level = smafStreamFullLevel / 2
	case mode == 1:
		level = smafStreamFullLevel
	default:
		level = min(int(data[7]), smafStreamFullLevel)
	}
	return channel, level, true
}

// smafStreamLevel is a channel's stream audio level from 7F 0B; a channel
// that never received one plays its stream audio at full level.
type smafStreamLevel struct {
	sent  bool
	value int
}

func (level *smafStreamLevel) set(value int) {
	level.sent, level.value = true, value
}

func (level smafStreamLevel) gain() float64 {
	if !level.sent {
		return 1
	}
	return float64(level.value) / smafStreamFullLevel
}
