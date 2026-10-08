package runtime

import (
	"encoding/binary"
	"math"
)

// SMAF PCM words are big endian. Mwa and ATR use different headers, but
// their decoded samples share this interleaved, frame-based representation.
func decodeSMAFSamples(number, rate, channels, format, baseBits int, data []byte) smafWave {
	if rate <= 0 || channels < 1 || channels > 2 || len(data) == 0 {
		return smafWave{}
	}
	var pcm []int16
	switch format {
	case 0, 1: // signed two's complement / unsigned offset binary PCM
		if baseBits != 1 && baseBits != 3 {
			return smafWave{}
		}
		bytesPerSample := 1
		if baseBits == 3 {
			bytesPerSample = 2
		}
		if len(data)%(bytesPerSample*channels) != 0 {
			return smafWave{}
		}
		pcm = make([]int16, len(data)/bytesPerSample)
		for index := range pcm {
			if bytesPerSample == 1 {
				value := data[index]
				if format == 1 {
					value ^= 0x80
				}
				pcm[index] = int16(int8(value)) << 8
			} else {
				value := binary.BigEndian.Uint16(data[index*2:])
				if format == 1 {
					value ^= 0x8000
				}
				pcm[index] = int16(value)
			}
		}
	case 2: // Yamaha ADPCM, four bits per channel sample
		if baseBits != 0 {
			return smafWave{}
		}
		pcm = decodeYamahaADPCMChannels(data, channels)
	default:
		return smafWave{}
	}
	return smafWave{number: number, sampleRate: rate, channels: channels, pcm: pcm}
}

func (voice *smafPCMVoice) tickStereo() (float64, float64) {
	channels := max(1, voice.channels)
	end := voice.end
	if end == 0 {
		end = len(voice.pcm) / channels
	}
	if !voice.active || end <= 0 || voice.kernel == nil {
		voice.active = false
		return 0, 0
	}
	if voice.position >= float64(end) {
		if !voice.loop {
			voice.active = false
			return 0, 0
		}
		voice.position = float64(voice.loopStart) + math.Mod(voice.position-float64(end), float64(end-voice.loopStart))
		voice.looped = true
	}
	gain := 1.0
	if voice.sampled {
		gain = voice.envelope.advance() * voice.totalGain
		if voice.envelope.phase == smafEnvelopeIdle {
			voice.active = false
			return 0, 0
		}
	}
	index := int(voice.position)
	phase := min(resamplePhases-1, max(0, int((voice.position-float64(index))*resamplePhases)))
	voice.position += voice.step
	kernel := voice.kernel
	row := kernel.weights[phase*kernel.taps : (phase+1)*kernel.taps]
	base := index - (kernel.half - 1)
	left, right := 0.0, 0.0
	// Most mono samples use a contiguous window, retaining the original hot path.
	if channels == 1 && base >= 0 && base+len(row) <= end && (!voice.looped || base >= voice.loopStart) {
		for tap, weight := range row {
			left += float64(voice.pcm[base+tap]) * weight
		}
		left *= gain / 32768
		return left, left
	}
	for tap, weight := range row {
		at := base + tap
		if voice.loop && (at >= end || (voice.looped && at < voice.loopStart)) {
			length := end - voice.loopStart
			at = voice.loopStart + ((at-voice.loopStart)%length+length)%length
		} else {
			at = min(end-1, max(0, at))
		}
		left += float64(voice.pcm[at*channels]) * weight
		if channels == 2 {
			right += float64(voice.pcm[at*channels+1]) * weight
		}
	}
	left *= gain / 32768
	if channels == 1 {
		return left, left
	}
	return left, right * gain / 32768
}

type smafPCMPatch struct {
	sampleRate, waveID  int
	loopPoint, endPoint int
	ram                 bool
	panFixed            bool
	pan                 float64
	envelope            smafOpPatch
}

// Yamaha VM35 PCM parameter bytes (MA-3/MA-5) describe a C4 playback rate,
// an amplitude envelope, and RAM/ROM wave selection. RM=0 selects RAM; looping
// is enabled by LP < EP. See MA-5 Authoring Tool 1.3.3, sections 4.18.3-4.
func parseSMAFPCMPatch(body []byte) *smafPCMPatch {
	if len(body) < 16 {
		return nil
	}
	pan := int(body[2] >> 3)
	patch := &smafPCMPatch{
		sampleRate: int(binary.BigEndian.Uint16(body)),
		waveID:     int(body[15] & 0x7f), ram: body[15]&0x80 == 0,
		loopPoint: int(binary.BigEndian.Uint16(body[11:13])),
		endPoint:  int(binary.BigEndian.Uint16(body[13:15])),
		panFixed:  body[2]&1 != 0,
		envelope: smafOpPatch{
			sr: body[4] >> 4, xof: body[4]&8 != 0,
			rr: body[5] >> 4, dr: body[5] & 15,
			ar: body[6] >> 4, sl: body[6] & 15, tl: body[7] >> 2,
			egType: true, // SR=0 sustains at SL until key-off.
		},
	}
	if pan <= 15 {
		patch.pan = float64(pan-15) / 15
	} else {
		patch.pan = float64(pan-15) / 16
	}
	return patch
}

func (decoder *smafDecoder) startSampledVoice(event smafEvent, patch *smafPCMPatch, drum bool) {
	// ROM samples require handset data. A missing RAM wave must likewise never
	// be replaced by a different FM instrument.
	if !patch.ram || patch.sampleRate <= 0 {
		return
	}
	channel := &decoder.channels[event.channel]
	waves := channel.sampledWaves
	if len(waves) == 0 {
		waves = channel.streamWaves
	}
	for index := range waves {
		wave := &waves[index]
		if wave.number != patch.waveID {
			continue
		}
		voice := decoder.startPCMWave(wave)
		voice.channel, voice.keyNote, voice.noteID = event.channel, event.a, event.noteID
		voice.gated, voice.sampled, voice.keyDown = true, true, true
		voice.velocity = math.Pow(float64(event.b&127)/127, 2)
		voice.volume = voice.velocity * channel.volume * channel.expression
		voice.totalGain = math.Pow(10, -0.75*float64(patch.envelope.tl)/20)
		if patch.envelope.tl == 63 {
			voice.totalGain = 0
		}
		voice.pan, voice.panFixed = channel.pan, patch.panFixed
		if voice.panFixed {
			voice.pan = patch.pan
		}
		voice.baseRate = float64(patch.sampleRate)
		if !drum {
			voice.baseRate *= math.Pow(2, float64(event.a+channel.octaveShift-60)/12)
		}
		voice.setRate(decoder.rate, channel.bend)
		voice.envelope.configure(patch.envelope, float64(decoder.rate), 0)
		voice.envelope.keyOn()
		voice.end = min(voice.end, patch.endPoint+1)
		voice.loopStart = patch.loopPoint
		voice.loop = voice.loopStart < patch.endPoint && voice.loopStart < voice.end
		return
	}
}

func (voice *smafPCMVoice) setRate(outputRate uint32, bend float64) {
	rate := voice.baseRate * math.Pow(2, bend/12)
	voice.step = rate / float64(outputRate)
	voice.kernel = resampleKernelFor(uint32(math.Ceil(rate)), outputRate)
}
