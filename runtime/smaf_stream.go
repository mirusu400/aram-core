package runtime

import "encoding/binary"

// Stream PCM uses Mtsp/Mwa inside an MTR score. Unlike the older Awa path,
// each Mwa starts with a three-byte wave type containing an explicit Hz rate.
// See Yamaha SMAF 3.05, section 4.4.2(7).
func parseSMAFStreamWaves(data []byte) []smafWave {
	var waves []smafWave
	for offset := 0; offset+8 <= len(data); {
		body := offset + 8
		size := uint64(binary.BigEndian.Uint32(data[offset+4 : body]))
		if size > uint64(len(data)-body) {
			break
		}
		end := body + int(size)
		if string(data[offset:offset+3]) == "Mwa" {
			if wave := decodeSMAFStreamWave(int(data[offset+3]), data[body:end]); len(wave.pcm) != 0 {
				waves = append(waves, wave)
			}
		}
		offset = end
	}
	return waves
}

func decodeSMAFStreamWave(number int, data []byte) smafWave {
	// Mono Yamaha 4-bit ADPCM. Other encodings must not be interpreted as
	// ADPCM or substituted with an FM instrument.
	if number < 1 || number > 62 || len(data) <= 3 || data[0] != 0x20 {
		return smafWave{}
	}
	rate := int(binary.BigEndian.Uint16(data[1:3]))
	if rate == 0 {
		return smafWave{}
	}
	return smafWave{
		number: number, sampleRate: rate,
		pcm: decodeYamahaADPCM(data[3:]),
	}
}

// Yamaha's MA-3/MA-5 authoring convention assigns bank MSB 125, LSB 0
// notes 0..12 and 92..110 to stream waves 1..32, at their native rate.
// These note values are wave selectors, not frequencies (note 0 is ~8 Hz).
// See MA-5 Authoring Tool User's Manual 1.3.3, section 4.5.1.
func smafStreamWaveNumber(note int) int {
	switch {
	case note >= 0 && note <= 12:
		return note + 1
	case note >= 92 && note <= 110:
		return note - 78
	default:
		return 0
	}
}

func (decoder *smafDecoder) startPCMWave(wave *smafWave) *smafPCMVoice {
	slot := -1
	for index := range decoder.pcmPool {
		if !decoder.pcmPool[index].active {
			slot = index
			break
		}
	}
	if slot < 0 {
		slot = decoder.nextPCM % len(decoder.pcmPool)
		decoder.nextPCM++
	}
	decoder.pcmPool[slot] = smafPCMVoice{
		pcm:    wave.pcm,
		kernel: resampleKernelFor(uint32(wave.sampleRate), decoder.rate),
		step:   float64(wave.sampleRate) / float64(decoder.rate),
		active: true, volume: 1,
	}
	if !decoder.pcmListed[slot] {
		decoder.pcmListed[slot] = true
		decoder.activePCM = append(decoder.activePCM, slot)
	}
	return &decoder.pcmPool[slot]
}

func (decoder *smafDecoder) startStreamWave(event smafEvent, number int) {
	channel := &decoder.channels[event.channel&127]
	for index := range channel.streamWaves {
		wave := &channel.streamWaves[index]
		if wave.number != number {
			continue
		}
		voice := decoder.startPCMWave(wave)
		voice.channel, voice.keyNote, voice.noteID = event.channel, event.a, event.noteID
		voice.gated = true
		voice.velocity = float64(event.b&127) / 127
		voice.velocity *= voice.velocity
		voice.volume = voice.velocity * channel.volume * channel.expression
		voice.pan = channel.pan
		return
	}
}

func (decoder *smafDecoder) updatePCMVolume(channelIndex int) {
	channel := &decoder.channels[channelIndex&127]
	for index := range decoder.pcmPool {
		voice := &decoder.pcmPool[index]
		if voice.active && voice.gated && voice.channel == channelIndex {
			voice.volume = voice.velocity * channel.volume * channel.expression
		}
	}
}
