package runtime

import (
	"encoding/binary"
	"math"
	"sort"
	"time"
)

const (
	maxSMAFEvents  = 400_000
	maxSMAFSeconds = 180
)

type smafTrack struct {
	number                 int
	format                 byte
	durationBase, gateBase byte
	audio                  bool
	channelStatus          []byte
	setup, sequence        []byte
	waves                  []smafWave
}

type smafWave struct {
	number     int
	sampleRate int
	pcm        []int16
}

type smafEventKind uint8

const (
	smafNoteOn smafEventKind = iota
	smafNoteOff
	smafProgram
	smafBankMSB
	smafBankLSB
	smafVolume
	smafPan
	smafExpression
	smafPitchBend
	smafModulation
	smafWaveOn
)

type smafEvent struct {
	sample  uint64
	kind    smafEventKind
	channel int
	a, b    int
}

type smafChannel struct {
	bankMSB, bankLSB, program int
	volume, expression, pan   float64
	bend                      float64
	octaveShift               int
	drum, rhythm              bool
}

type smafDecoder struct {
	rate         uint32
	tracks       []smafTrack
	events       []smafEvent
	voices       []smafParsedVoice
	channels     [128]smafChannel
	pool         [32]smafVoice
	pcmPool      [16]smafPCMVoice
	activeVoices []int
	activePCM    []int
	voiceListed  [32]bool
	pcmListed    [16]bool
	nextVoice    int
	nextPCM      int
}

type smafPCMVoice struct {
	pcm      []int16
	kernel   *resampleKernel
	position float64
	step     float64
	pan      float64
	panGains smafPanGains
	active   bool
}

// tick reads one output sample from the wave bank. The banks hold Yamaha ADPCM
// recorded at 4 to 22 kHz, so nearly every one is played back well above its
// own rate and has to be reconstructed rather than interpolated. Drawing a
// straight line between two stored samples, which is what this did, left the
// source's spectral images only about 14 dB below the sound itself across the
// local corpus - the grit percussion in these scores used to have. The mixer's
// windowed sinc puts them 55 dB down. See media_resample.go.
func (voice *smafPCMVoice) tick() float64 {
	if !voice.active || len(voice.pcm) == 0 || voice.kernel == nil ||
		voice.position >= float64(len(voice.pcm)) {
		voice.active = false
		return 0
	}
	index := int(voice.position)
	fraction := voice.position - float64(index)
	voice.position += voice.step
	kernel := voice.kernel
	phase := int(fraction * resamplePhases)
	if phase < 0 {
		phase = 0
	} else if phase >= resamplePhases {
		phase = resamplePhases - 1
	}
	row := kernel.weights[phase*kernel.taps : (phase+1)*kernel.taps]
	base := index - (kernel.half - 1)
	value := 0.0
	// The window sits wholly inside the wave for all but its first and last
	// few samples, and taking that case without a per-tap clamp is what keeps
	// the filter affordable on a voice that sounds at the render rate.
	if base >= 0 && base+len(row) <= len(voice.pcm) {
		window := voice.pcm[base : base+len(row)]
		for tap, weight := range row {
			value += float64(window[tap]) * weight
		}
		return value / 32768
	}
	last := len(voice.pcm) - 1
	for tap, weight := range row {
		at := base + tap
		if at < 0 {
			at = 0
		} else if at > last {
			at = last
		}
		value += float64(voice.pcm[at]) * weight
	}
	return value / 32768
}

func looksLikeSMAF(data []byte) bool {
	if len(data) < 12 || string(data[:4]) != "MMMD" {
		return false
	}
	size := binary.BigEndian.Uint32(data[4:8])
	return size >= 8 && uint64(size) <= uint64(len(data))
}

func decodeSMAFPCM16(data []byte, sampleRate uint32) *decodedPCM {
	if sampleRate < 8_000 || sampleRate > 192_000 {
		return nil
	}
	decoder := &smafDecoder{rate: sampleRate}
	if !decoder.parse(data) || !decoder.buildEvents() {
		return nil
	}
	stream := newSMAFRenderStream(decoder)
	samples := stream.renderUntil(nil, stream.end)
	if len(samples) == 0 {
		return nil
	}
	frames := len(samples) / 2
	return &decodedPCM{
		sampleRate: sampleRate,
		channels:   2,
		samples:    samples,
		duration: time.Duration(
			uint64(frames) * uint64(time.Second) / uint64(sampleRate),
		),
	}
}

func decodeSMAFLazyPCM16(data []byte, sampleRate uint32) *decodedPCM {
	if sampleRate < 8_000 || sampleRate > 192_000 {
		return nil
	}
	decoder := &smafDecoder{rate: sampleRate}
	if !decoder.parse(data) || !decoder.buildEvents() {
		return nil
	}
	// Sample-only ATR clips are short and cheap to decode. Keeping them eager
	// gives one-shot effects their exact natural length. FM score tracks are
	// synthesized incrementally so starting a BGM never stalls the guest.
	for _, track := range decoder.tracks {
		if track.audio {
			stream := newSMAFRenderStream(decoder)
			samples := stream.renderUntil(nil, stream.end)
			if len(samples) == 0 {
				return nil
			}
			return &decodedPCM{
				sampleRate: sampleRate,
				channels:   2,
				samples:    samples,
				duration: time.Duration(
					uint64(len(samples)/2) * uint64(time.Second) /
						uint64(sampleRate),
				),
			}
		}
	}
	return finishLazyScoreDecode(decoder, sampleRate, func() *smafDecoder {
		probeDecoder := &smafDecoder{rate: sampleRate}
		if !probeDecoder.parse(data) || !probeDecoder.buildEvents() {
			return nil
		}
		return probeDecoder
	})
}

// finishLazyScoreDecode turns a built score into a decodedPCM. A score short
// enough that rendering it is not felt is rendered whole here, so playback is
// an array read and the length is exact; a longer one is left incremental and
// only its playable length is worked out. rebuild produces a second decoder for
// the length probe, which consumes the stream it walks.
func finishLazyScoreDecode(
	decoder *smafDecoder,
	sampleRate uint32,
	rebuild func() *smafDecoder,
) *decodedPCM {
	stream := newSMAFRenderStream(decoder)
	if stream.end == 0 {
		return nil
	}
	if stream.end <= uint64(sampleRate)*smafEagerRenderSeconds {
		if samples := stream.renderUntil(nil, stream.end); len(samples) != 0 {
			return &decodedPCM{
				sampleRate: sampleRate,
				channels:   2,
				samples:    samples,
				duration: time.Duration(
					uint64(len(samples)/2) * uint64(time.Second) /
						uint64(sampleRate),
				),
			}
		}
	}
	// An FM score's playable length is where its last voice stops sounding,
	// which is usually well short of stream.end: that is the last event plus a
	// two-second pad, so a half-second effect would otherwise report two
	// seconds and a music loop would carry trailing silence around the loop.
	//
	// Finding it used to mean rendering the whole score at decode, which is
	// what Play does synchronously - a freeze of up to 611 ms in the frame a
	// title starts its music (measured on 메이플스토리). probeEnd walks the
	// same loop with synthesis switched off and reports the same sample for a
	// quarter of the cost, so the samples can stay incremental and only the
	// part actually played is ever synthesized: the worst frame drops to
	// 196 ms and the mean frame improves too.
	//
	// The probe consumes the stream it walks, so it gets its own.
	length := stream.end
	if stream.end <= uint64(sampleRate)*smafProbedLengthSeconds {
		if probeDecoder := rebuild(); probeDecoder != nil {
			if probed := newSMAFRenderStream(probeDecoder).probeEnd(); probed != 0 {
				length = probed
			}
		}
	}
	return &decodedPCM{
		sampleRate: sampleRate,
		channels:   2,
		duration: time.Duration(
			length * uint64(time.Second) / uint64(sampleRate),
		),
		smaf: stream,
	}
}

// smafEagerRenderSeconds bounds how long an FM score may be while still being
// rendered whole at decode.
//
// It is deliberately just past the two-second pad every score carries, because
// that pad is what a one-shot effect's stream.end is almost entirely made of:
// a third-of-a-second hit sound ends at 2.0s and renders in well under a
// millisecond. Rendering those whole is strictly better than probing them and
// synthesizing again during playback, which would do the work twice.
const smafEagerRenderSeconds = 3

// smafProbedLengthSeconds bounds how long an FM score may be before decode
// stops looking for its natural end and takes stream.end as the length. The
// probe is cheap relative to a render but still linear in the score, and a
// minutes-long menu loop plays to its end anyway, so the two agree there.
const smafProbedLengthSeconds = 30

func (decoder *smafDecoder) parse(data []byte) bool {
	if !looksLikeSMAF(data) {
		return false
	}
	end := 8 + int(binary.BigEndian.Uint32(data[4:8]))
	if end > len(data) {
		end = len(data)
	}
	for offset := 8; offset+8 <= end; {
		if !smafChunkIDValid(data[offset:]) {
			offset++
			continue
		}
		size := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		if size < 0 || body > len(data) {
			return false
		}
		if size > len(data)-body {
			size = len(data) - body
		}
		if string(data[offset:offset+3]) == "MTR" {
			decoder.parseTrack(data[body:body+size], int(data[offset+3]))
		} else if string(data[offset:offset+3]) == "ATR" {
			decoder.parseAudioTrack(data[body:body+size], int(data[offset+3]))
		}
		offset = body + size
		if size == 0 {
			offset++
		}
	}
	return len(decoder.tracks) != 0
}

func (decoder *smafDecoder) parseAudioTrack(data []byte, number int) {
	if len(data) < 4 {
		return
	}
	track := smafTrack{
		number:       number,
		format:       data[0],
		durationBase: data[2],
		gateBase:     data[2],
		audio:        true,
	}
	for offset := 4; offset+8 <= len(data); {
		if !smafChunkIDValid(data[offset:]) {
			offset++
			continue
		}
		size := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		if size < 0 || body > len(data) {
			break
		}
		if size > len(data)-body {
			size = len(data) - body
		}
		switch string(data[offset : offset+3]) {
		case "Ats":
			if data[offset+3] == 'q' {
				track.sequence = append([]byte(nil), data[body:body+size]...)
			}
		case "Awa":
			if wave := decodeSMAFWave(
				int(data[offset+3]),
				data[body:body+size],
			); len(wave.pcm) != 0 {
				track.waves = append(track.waves, wave)
			}
		}
		offset = body + size
		if size == 0 {
			offset++
		}
	}
	if len(track.sequence) != 0 && len(track.waves) != 0 {
		decoder.tracks = append(decoder.tracks, track)
	}
}

func decodeSMAFWave(number int, data []byte) smafWave {
	if len(data) <= 2 {
		return smafWave{}
	}
	rates := [...]int{4_000, 8_000, 11_025, 22_050, 44_100}
	rateCode := int(data[1] & 15)
	sampleRate := 8_000
	if rateCode < len(rates) {
		sampleRate = rates[rateCode]
	}
	return smafWave{
		number:     number,
		sampleRate: sampleRate,
		pcm:        decodeYamahaADPCM(data[2:]),
	}
}

func decodeYamahaADPCM(data []byte) []int16 {
	result := make([]int16, 0, len(data)*2)
	previous, step := 0, 127
	decode := func(code byte) int16 {
		delta := step >> 3
		if code&1 != 0 {
			delta += step >> 2
		}
		if code&2 != 0 {
			delta += step >> 1
		}
		if code&4 != 0 {
			delta += step
		}
		if code&8 != 0 {
			delta = -delta
		}
		previous = max(-32768, min(32767, previous+delta))
		switch code & 7 {
		case 0, 1, 2, 3:
			step = step * 115 / 128
		case 4:
			step = step * 307 / 256
		case 5:
			step = step * 409 / 256
		case 6:
			step *= 2
		case 7:
			step = step * 307 / 128
		}
		step = max(127, min(24576, step))
		return int16(previous)
	}
	for _, value := range data {
		// SMAF wave banks use the low nibble first.
		result = append(result, decode(value&15), decode(value>>4&15))
	}
	return result
}

func smafChunkIDValid(data []byte) bool {
	if len(data) < 3 {
		return false
	}
	for _, value := range data[:3] {
		if value < 0x20 || value > 0x7e {
			return false
		}
	}
	return true
}

func (decoder *smafDecoder) parseTrack(data []byte, number int) {
	if len(data) < 4 {
		return
	}
	track := smafTrack{
		number:       number,
		format:       data[0],
		durationBase: data[2],
		gateBase:     data[3],
	}
	statusLength := 16
	if track.format == 0 {
		statusLength = 2
	} else if track.format == 3 {
		statusLength = 32
	}
	header := min(4+statusLength, len(data))
	track.channelStatus = append([]byte(nil), data[4:header]...)
	for offset := header; offset+8 <= len(data); {
		if !smafChunkIDValid(data[offset:]) {
			offset++
			continue
		}
		size := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		if size < 0 || body > len(data) {
			break
		}
		if size > len(data)-body {
			size = len(data) - body
		}
		switch string(data[offset : offset+4]) {
		case "Mtsu":
			track.setup = append([]byte(nil), data[body:body+size]...)
		case "Mtsq":
			track.sequence = append([]byte(nil), data[body:body+size]...)
		}
		offset = body + size
		if size == 0 {
			offset++
		}
	}
	if len(track.sequence) != 0 {
		decoder.tracks = append(decoder.tracks, track)
	}
}

func smafTimeBase(value byte) float64 {
	switch value {
	case 0, 0x10:
		return 1
	case 1, 0x11:
		return 2
	case 2, 0x12:
		return 4
	case 3, 0x13:
		return 5
	default:
		return 4
	}
}

func (decoder *smafDecoder) buildEvents() bool {
	for index := range decoder.channels {
		decoder.channels[index].volume = 100.0 / 127
		decoder.channels[index].expression = 1
	}
	for _, track := range decoder.tracks {
		if track.audio {
			decoder.decodeHandyPhone(track, 120, true)
			continue
		}
		base := track.number * 16
		if track.format == 0 {
			base = track.number * 4
		}
		if base < 0 || base > 112 {
			base = 0
		}
		decoder.applyRhythmChannels(track, base)
		decoder.collectVoices(track.setup, track.format != 0)
		switch track.format {
		case 0:
			decoder.decodeHandyPhone(track, base, false)
		case 1:
			inflated := inflateSMAFHuffman(track.sequence)
			if len(inflated) != 0 {
				track.sequence = inflated
				decoder.decodeMobile(track, base)
			}
		default:
			decoder.decodeMobile(track, base)
		}
	}
	if len(decoder.events) == 0 {
		return false
	}
	setupWindow := uint64(decoder.rate) / 20
	var seen [128][3]bool
	for index := range decoder.events {
		event := &decoder.events[index]
		kind := -1
		switch event.kind {
		case smafBankMSB:
			kind = 0
		case smafBankLSB:
			kind = 1
		case smafProgram:
			kind = 2
		}
		if kind < 0 || seen[event.channel&127][kind] {
			continue
		}
		seen[event.channel&127][kind] = true
		if event.sample <= setupWindow {
			event.sample = 0
		}
	}
	sortSMAFEvents(decoder.events)
	return true
}

// sortSMAFEvents puts the timeline in the order the render loop consumes it.
// Events that land on the same sample are ordered so a channel's setup - its
// bank, program, volume, and pan - is in force before a note on that sample
// reads it.
func sortSMAFEvents(events []smafEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		left, right := events[i], events[j]
		if left.sample != right.sample {
			return left.sample < right.sample
		}
		leftNote := left.kind == smafNoteOn || left.kind == smafNoteOff
		rightNote := right.kind == smafNoteOn || right.kind == smafNoteOff
		return !leftNote && rightNote
	})
}

func (decoder *smafDecoder) applyRhythmChannels(track smafTrack, base int) {
	if track.format == 0 {
		for index := 0; index < 4 && index/2 < len(track.channelStatus); index++ {
			status := track.channelStatus[index/2] & 15
			if index%2 == 0 {
				status = track.channelStatus[index/2] >> 4
			}
			decoder.channels[(base+index)&127].rhythm = status&3 == 3
		}
		return
	}
	for index, status := range track.channelStatus {
		if index >= 32 {
			break
		}
		decoder.channels[(base+index)&127].rhythm = status&3 == 3
	}
}

func (decoder *smafDecoder) collectVoices(data []byte, mobile bool) {
	for offset := 0; offset < len(data); {
		if data[offset] == 0xff {
			offset++
			if offset >= len(data) {
				break
			}
		}
		if data[offset] != 0xf0 {
			offset++
			continue
		}
		offset++
		length := 0
		if mobile {
			length = readSMAFVLQ(data, &offset)
		} else {
			length = readSMAFHPS(data, &offset)
		}
		if length <= 0 || length > len(data)-offset {
			break
		}
		payload := data[offset : offset+length]
		if payload[len(payload)-1] == 0xf7 {
			payload = payload[:len(payload)-1]
		}
		if voice := parseSMAFVoice(payload); voice.valid {
			decoder.voices = append(decoder.voices, voice)
		}
		offset += length
	}
}

func readSMAFVLQ(data []byte, offset *int) int {
	value := 0
	for guard := 0; *offset < len(data) && guard < 5; guard++ {
		next := data[*offset]
		*offset++
		value = value<<7 | int(next&0x7f)
		if next&0x80 == 0 {
			break
		}
	}
	return value
}

func readSMAFHPS(data []byte, offset *int) int {
	if *offset >= len(data) {
		return 0
	}
	first := data[*offset]
	*offset++
	if first < 0x80 {
		return int(first)
	}
	if *offset >= len(data) {
		return int(first & 0x7f)
	}
	second := data[*offset]
	*offset++
	return (int(first&0x7f)+1)<<7 | int(second)
}

func (decoder *smafDecoder) addEvent(event smafEvent) bool {
	if len(decoder.events) >= maxSMAFEvents {
		return false
	}
	decoder.events = append(decoder.events, event)
	return true
}

func (decoder *smafDecoder) sampleAt(milliseconds float64) uint64 {
	if milliseconds <= 0 {
		return 0
	}
	maximum := float64(maxSMAFSeconds * decoder.rate)
	sample := milliseconds * float64(decoder.rate) / 1000
	if sample >= maximum {
		return uint64(maximum)
	}
	return uint64(sample)
}

func (decoder *smafDecoder) decodeMobile(track smafTrack, base int) {
	data := track.sequence
	durationBase := smafTimeBase(track.durationBase)
	gateBase := smafTimeBase(track.gateBase)
	var velocity [16]byte
	for index := range velocity {
		velocity[index] = 64
	}
	offset := 0
	milliseconds := 0.0
	for guard := 0; offset < len(data) && guard < 4_000_000 &&
		len(decoder.events) < maxSMAFEvents; guard++ {
		duration := readSMAFVLQ(data, &offset)
		milliseconds += float64(duration) * durationBase
		if milliseconds > maxSMAFSeconds*1000 || offset >= len(data) {
			break
		}
		status := data[offset]
		offset++
		if status < 0x80 {
			continue
		}
		channel := base + int(status&15)
		at := decoder.sampleAt(milliseconds)
		switch status & 0xf0 {
		case 0x80, 0x90:
			if offset >= len(data) {
				return
			}
			note := int(data[offset] & 0x7f)
			offset++
			currentVelocity := int(velocity[status&15])
			if status&0xf0 == 0x90 {
				if offset >= len(data) {
					return
				}
				currentVelocity = int(data[offset] & 0x7f)
				velocity[status&15] = byte(currentVelocity)
				offset++
			}
			gate := readSMAFVLQ(data, &offset)
			if gate == 0 {
				continue
			}
			off := at + decoder.sampleAt(float64(gate)*gateBase)
			decoder.addEvent(smafEvent{
				sample: at, kind: smafNoteOn, channel: channel,
				a: note, b: currentVelocity,
			})
			decoder.addEvent(smafEvent{
				sample: off, kind: smafNoteOff, channel: channel, a: note,
			})
		case 0xa0:
			offset = min(offset+2, len(data))
		case 0xb0:
			if offset+2 > len(data) {
				return
			}
			control, value := data[offset], int(data[offset+1])
			offset += 2
			kind := smafEventKind(255)
			switch control {
			case 0:
				kind = smafBankMSB
			case 0x20:
				kind = smafBankLSB
			case 7:
				kind = smafVolume
			case 0x0a:
				kind = smafPan
			case 0x0b:
				kind = smafExpression
			case 1:
				kind = smafModulation
			}
			if kind != 255 {
				decoder.addEvent(smafEvent{
					sample: at, kind: kind, channel: channel, a: value,
				})
			}
		case 0xc0:
			if offset >= len(data) {
				return
			}
			decoder.addEvent(smafEvent{
				sample: at, kind: smafProgram, channel: channel,
				a: int(data[offset] & 0x7f),
			})
			offset++
		case 0xd0:
			if offset < len(data) {
				offset++
			}
		case 0xe0:
			if offset+2 > len(data) {
				return
			}
			value := int(data[offset]&0x7f) |
				int(data[offset+1]&0x7f)<<7
			offset += 2
			decoder.addEvent(smafEvent{
				sample: at, kind: smafPitchBend, channel: channel,
				a: value - 8192,
			})
		case 0xf0:
			switch status {
			case 0xf0:
				length := readSMAFVLQ(data, &offset)
				if length <= 0 || length > len(data)-offset {
					return
				}
				payload := data[offset : offset+length]
				if payload[len(payload)-1] == 0xf7 {
					payload = payload[:len(payload)-1]
				}
				if voice := parseSMAFVoice(payload); voice.valid {
					decoder.voices = append(decoder.voices, voice)
				}
				offset += length
			case 0xff:
				if offset >= len(data) {
					return
				}
				meta := data[offset]
				offset++
				if meta == 0 {
					continue
				}
				if meta == 0x2f {
					return
				}
				if offset >= len(data) {
					return
				}
				length := int(data[offset])
				offset++
				offset = min(offset+length, len(data))
			}
		}
	}
}

func (decoder *smafDecoder) decodeHandyPhone(
	track smafTrack,
	base int,
	waveMode bool,
) {
	data := track.sequence
	durationBase := smafTimeBase(track.durationBase)
	gateBase := smafTimeBase(track.gateBase)
	offset := 0
	milliseconds := 0.0
	for guard := 0; offset < len(data) && guard < 2_000_000 &&
		len(decoder.events) < maxSMAFEvents; guard++ {
		duration := readSMAFHPS(data, &offset)
		milliseconds += float64(duration) * durationBase
		if milliseconds > maxSMAFSeconds*1000 || offset >= len(data) {
			break
		}
		first := data[offset]
		offset++
		if first == 0xff {
			if offset >= len(data) {
				return
			}
			second := data[offset]
			offset++
			if second == 0 {
				continue
			}
			if second == 0xf0 {
				length := readSMAFHPS(data, &offset)
				if length <= 0 || length > len(data)-offset {
					return
				}
				payload := data[offset : offset+length]
				if payload[len(payload)-1] == 0xf7 {
					payload = payload[:len(payload)-1]
				}
				if voice := parseSMAFVoice(payload); voice.valid {
					decoder.voices = append(decoder.voices, voice)
				}
				offset += length
				continue
			}
			if offset >= len(data) {
				return
			}
			length := int(data[offset])
			offset++
			offset = min(offset+length, len(data))
			continue
		}
		if first == 0 {
			if offset >= len(data) {
				return
			}
			second := data[offset]
			offset++
			if second == 0 {
				if offset >= len(data) || data[offset] == 0 {
					return
				}
				offset++
				continue
			}
			channel := base + int(second>>6&3)
			class := second >> 4 & 3
			value := int(second & 15)
			at := decoder.sampleAt(milliseconds)
			if class == 3 {
				if offset >= len(data) {
					return
				}
				longValue := int(data[offset])
				offset++
				kind := smafEventKind(255)
				switch value {
				case 0:
					kind = smafProgram
					longValue &= 0x7f
				case 1:
					kind = smafBankLSB
				case 2:
					shift := 0
					if longValue >= 0x81 && longValue <= 0x84 {
						shift = -12 * (longValue - 0x80)
					} else if longValue >= 1 && longValue <= 4 {
						shift = 12 * longValue
					}
					decoder.addEvent(smafEvent{
						sample: at, kind: smafModulation, channel: channel,
						a: 1000 + shift, b: 1,
					})
				case 4:
					kind = smafPitchBend
					longValue = (longValue - 64) * 128
				case 7:
					kind = smafVolume
					longValue &= 0x7f
				case 0x0a:
					kind = smafPan
					longValue &= 0x7f
				case 0x0b:
					kind = smafExpression
					longValue &= 0x7f
				}
				if kind != 255 {
					decoder.addEvent(smafEvent{
						sample: at, kind: kind, channel: channel, a: longValue,
					})
				}
			} else if class == 0 {
				expression := 0
				if value > 1 {
					expression = min(value*8+15, 127)
				}
				decoder.addEvent(smafEvent{
					sample: at, kind: smafExpression, channel: channel,
					a: expression,
				})
			} else if class == 1 {
				decoder.addEvent(smafEvent{
					sample: at, kind: smafPitchBend, channel: channel,
					a: (value*8)<<7 - 8192,
				})
			}
			continue
		}
		channel := base + int(first>>6&3)
		note := int(first&15) + int(first>>4&3)*12 + 36
		gate := readSMAFHPS(data, &offset)
		if gate == 0 {
			continue
		}
		on := decoder.sampleAt(milliseconds)
		if waveMode {
			decoder.addEvent(smafEvent{
				sample:  on,
				kind:    smafWaveOn,
				channel: channel,
				a:       int(first & 15),
				b:       127,
			})
			continue
		}
		off := on + decoder.sampleAt(float64(gate)*gateBase)
		decoder.addEvent(smafEvent{
			sample: on, kind: smafNoteOn, channel: channel, a: note, b: 127,
		})
		decoder.addEvent(smafEvent{
			sample: off, kind: smafNoteOff, channel: channel, a: note,
		})
	}
}

func (decoder *smafDecoder) resolveVoice(
	channel smafChannel,
	note int,
) (smafPatch, bool) {
	var melody *smafParsedVoice
	for index := range decoder.voices {
		voice := &decoder.voices[index]
		if voice.key.bankMSB != channel.bankMSB ||
			voice.key.bankLSB != channel.bankLSB ||
			voice.key.program != channel.program {
			continue
		}
		if voice.key.drumNote != 0 {
			if voice.key.drumNote == note {
				return voice.patch, true
			}
		} else if melody == nil {
			melody = voice
		}
	}
	if melody != nil {
		return melody.patch, true
	}
	return smafPatch{}, false
}

func smafNoteFrequency(note int) float64 {
	return 440 * math.Pow(2, (float64(note)-69)/12)
}

func (decoder *smafDecoder) fire(event smafEvent) {
	channel := &decoder.channels[event.channel&127]
	switch event.kind {
	case smafProgram:
		channel.program = event.a
	case smafBankMSB:
		channel.bankMSB = event.a
	case smafBankLSB:
		channel.bankLSB = event.a & 0x7f
		channel.drum = event.a&0x80 != 0
	case smafVolume:
		channel.volume = float64(event.a&0x7f) / 127
		for index := range decoder.pool {
			voice := &decoder.pool[index]
			if voice.active && voice.channel == event.channel {
				voice.volume = channel.volume * channel.expression
			}
		}
	case smafExpression:
		channel.expression = float64(event.a&0x7f) / 127
		for index := range decoder.pool {
			voice := &decoder.pool[index]
			if voice.active && voice.channel == event.channel {
				voice.volume = channel.volume * channel.expression
			}
		}
	case smafPan:
		channel.pan = float64(event.a&0x7f-64) / 64
	case smafModulation:
		if event.b == 1 {
			channel.octaveShift = event.a - 1000
		}
	case smafPitchBend:
		channel.bend = float64(event.a) / 8192 * 2
		for index := range decoder.pool {
			voice := &decoder.pool[index]
			if voice.active && voice.channel == event.channel {
				voice.setFrequency(smafNoteFrequency(voice.note) *
					math.Pow(2, channel.bend/12))
			}
		}
	case smafNoteOff:
		for index := range decoder.pool {
			voice := &decoder.pool[index]
			if voice.active && voice.channel == event.channel &&
				voice.keyNote == event.a {
				voice.noteOff()
				break
			}
		}
	case smafNoteOn:
		patch, found := decoder.resolveVoice(*channel, event.a)
		if !found {
			if channel.rhythm || channel.drum {
				patch = drumSMAFPatch(event.a)
			} else {
				patch = gmSMAFPatch(channel.program)
			}
		}
		slot := -1
		for index := range decoder.pool {
			if !decoder.pool[index].active {
				slot = index
				break
			}
		}
		if slot < 0 {
			slot = decoder.nextVoice % len(decoder.pool)
			decoder.nextVoice++
		}
		soundingNote := event.a + channel.octaveShift + patch.noteShift
		frequency := smafNoteFrequency(soundingNote) *
			math.Pow(2, channel.bend/12)
		voice := &decoder.pool[slot]
		voice.sampleRate = float64(decoder.rate)
		voice.channel = event.channel
		voice.note = soundingNote
		voice.keyNote = event.a
		voice.volume = channel.volume * channel.expression
		velocity := float64(event.b)
		if velocity == 0 {
			velocity = 100
		}
		velocity /= 127
		voice.noteOn(patch, frequency, velocity*velocity)
		voice.pan = channel.pan
		if voice.pan == 0 {
			voice.pan = patch.panDefault
		}
		if !decoder.voiceListed[slot] {
			decoder.voiceListed[slot] = true
			decoder.activeVoices = append(decoder.activeVoices, slot)
		}
	case smafWaveOn:
		var wave *smafWave
		for trackIndex := range decoder.tracks {
			for waveIndex := range decoder.tracks[trackIndex].waves {
				candidate := &decoder.tracks[trackIndex].waves[waveIndex]
				if candidate.number == event.a {
					wave = candidate
					break
				}
			}
			if wave != nil {
				break
			}
		}
		if wave == nil || len(wave.pcm) == 0 {
			return
		}
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
			pcm: wave.pcm,
			kernel: resampleKernelFor(
				uint32(wave.sampleRate),
				decoder.rate,
			),
			step:   float64(wave.sampleRate) / float64(decoder.rate),
			active: true,
		}
		if !decoder.pcmListed[slot] {
			decoder.pcmListed[slot] = true
			decoder.activePCM = append(decoder.activePCM, slot)
		}
	}
}

type smafRenderStream struct {
	decoder                         *smafDecoder
	cursor, end                     uint64
	eventIndex                      int
	highPassCoefficient             float64
	highPassInputLeft               float64
	highPassInputRight              float64
	highPassOutputLeft              float64
	highPassOutputRight             float64
	filterCoefficient               float64
	filter1Left, filter1Right       float64
	filter2Left, filter2Right       float64
	limiterEnvelope, limiterRelease float64
	finished                        bool
}

func newSMAFRenderStream(decoder *smafDecoder) *smafRenderStream {
	stream := &smafRenderStream{decoder: decoder}
	if len(decoder.events) == 0 {
		stream.finished = true
		return stream
	}
	last := decoder.events[len(decoder.events)-1].sample
	maximum := uint64(decoder.rate) * maxSMAFSeconds
	stream.end = last + uint64(decoder.rate)*2
	if stream.end > maximum {
		stream.end = maximum
	}
	if stream.end == 0 || stream.end > uint64(math.MaxInt/2) {
		stream.end = 0
		stream.finished = true
		return stream
	}
	stream.filterCoefficient = 1 - math.Exp(
		-2*math.Pi*10_000/float64(decoder.rate),
	)
	stream.highPassCoefficient = math.Exp(
		-2 * math.Pi * 20 / float64(decoder.rate),
	)
	stream.limiterRelease = math.Exp(-1 / (0.15 * float64(decoder.rate)))
	return stream
}

// renderUntil synthesizes samples up to target and appends them to output.
func (stream *smafRenderStream) renderUntil(
	output []int16,
	target uint64,
) []int16 {
	return stream.render(output, target, false)
}

// probeEnd reports the sample a full render would stop at: the score's natural
// end, where the last voice's envelopes go idle, which is usually well before
// the two-second pad in stream.end.
//
// A caller that needs the natural length used to have to render the whole score
// to find it, which froze the emulation for over half a second when a title
// started its music. The silent pass costs about a quarter of that - it still
// advances an envelope per operator per sample, but synthesizes no waveform and
// mixes nothing. It stops on the same sample because the stop condition reads
// only the event index and whether any voice is still sounding, and a voice
// retires purely on its envelopes. TestSMAFProbeEndMatchesRender checks the two
// against each other, and every score in the local corpus probes to exactly the
// length a full render produces.
//
// The stream is consumed: probing leaves it at its end, so the caller renders
// from a fresh one.
func (stream *smafRenderStream) probeEnd() uint64 {
	if stream == nil || stream.end == 0 {
		return 0
	}
	stream.render(nil, stream.end, true)
	return stream.cursor
}

// render is the shared body. When silent is set it advances exactly the same
// decoder, voice, and stream state, but samples no waveform, runs no filter,
// and produces no output.
func (stream *smafRenderStream) render(
	output []int16,
	target uint64,
	silent bool,
) []int16 {
	if stream == nil || stream.finished || stream.end == 0 {
		return output
	}
	if target > stream.end {
		target = stream.end
	}
	decoder := stream.decoder
	last := decoder.events[len(decoder.events)-1].sample
	// A render that starts from nothing - the eager whole-track path - knows
	// exactly how many samples it will produce, so reserve them instead of
	// letting append double a multi-megabyte slice a dozen times. An
	// incremental render is handed the samples rendered so far and must keep
	// append's amortized growth, or extending it would copy everything again
	// on every call.
	if output == nil && !silent {
		if remaining := target - stream.cursor; remaining > 0 {
			output = make([]int16, 0, int(remaining)*2)
		}
	}
	for stream.cursor < target {
		for stream.eventIndex < len(decoder.events) &&
			decoder.events[stream.eventIndex].sample <= stream.cursor {
			decoder.fire(decoder.events[stream.eventIndex])
			stream.eventIndex++
		}
		left, right := 0.0, 0.0
		active := false
		// The live lists are rebuilt only on the sample where a voice actually
		// ends. Re-appending every surviving index on every one of the 44100
		// samples a second holds was a bounds check and a store per voice per
		// sample, for a list that changes a handful of times in a whole track.
		retired := false
		for _, index := range decoder.activeVoices {
			voice := &decoder.pool[index]
			if !voice.active {
				decoder.voiceListed[index] = false
				retired = true
				continue
			}
			active = true
			value := voice.tick(silent) * 0.32
			if !silent {
				panLeft, panRight := voice.panGains.gains(voice.pan)
				left += value * panLeft
				right += value * panRight
			}
			if !voice.active {
				decoder.voiceListed[index] = false
				retired = true
			}
		}
		if retired {
			kept := decoder.activeVoices[:0]
			for _, index := range decoder.activeVoices {
				if decoder.pool[index].active {
					kept = append(kept, index)
				}
			}
			decoder.activeVoices = kept
		}
		retired = false
		for _, index := range decoder.activePCM {
			voice := &decoder.pcmPool[index]
			if !voice.active {
				decoder.pcmListed[index] = false
				retired = true
				continue
			}
			active = true
			value := voice.tick() * 0.32
			if !silent {
				panLeft, panRight := voice.panGains.gains(voice.pan)
				left += value * panLeft
				right += value * panRight
			}
			if !voice.active {
				decoder.pcmListed[index] = false
				retired = true
			}
		}
		if retired {
			kept := decoder.activePCM[:0]
			for _, index := range decoder.activePCM {
				if decoder.pcmPool[index].active {
					kept = append(kept, index)
				}
			}
			decoder.activePCM = kept
		}
		// A silent pass skips the presentation chain entirely: nothing in it
		// can change whether a voice is still sounding, which is all the pass
		// is looking for.
		if !silent {
			output = stream.mixSample(output, left, right)
		}
		stream.cursor++
		if stream.eventIndex >= len(decoder.events) && !active &&
			stream.cursor > last {
			stream.finished = true
			break
		}
	}
	if stream.cursor >= stream.end {
		stream.finished = true
	}
	return output
}

// mixSample runs one mixed stereo sample through the presentation chain - the
// DC-blocking high pass, the two-pole low pass, and the limiter - and appends
// it to output.
func (stream *smafRenderStream) mixSample(
	output []int16,
	left, right float64,
) []int16 {
	highPassedLeft := stream.highPassCoefficient *
		(stream.highPassOutputLeft + left - stream.highPassInputLeft)
	highPassedRight := stream.highPassCoefficient *
		(stream.highPassOutputRight + right - stream.highPassInputRight)
	stream.highPassInputLeft = left
	stream.highPassInputRight = right
	stream.highPassOutputLeft = highPassedLeft
	stream.highPassOutputRight = highPassedRight
	left, right = highPassedLeft, highPassedRight
	stream.filter1Left += stream.filterCoefficient *
		(left - stream.filter1Left)
	stream.filter2Left += stream.filterCoefficient *
		(stream.filter1Left - stream.filter2Left)
	stream.filter1Right += stream.filterCoefficient *
		(right - stream.filter1Right)
	stream.filter2Right += stream.filterCoefficient *
		(stream.filter1Right - stream.filter2Right)
	left, right = stream.filter2Left, stream.filter2Right
	peak := max(math.Abs(left), math.Abs(right))
	stream.limiterEnvelope = max(
		peak,
		stream.limiterEnvelope*stream.limiterRelease,
	)
	if stream.limiterEnvelope > 0.92 {
		gain := 0.92 / stream.limiterEnvelope
		left *= gain
		right *= gain
	}
	return append(output, smafFloatToPCM(left), smafFloatToPCM(right))
}

func smafFloatToPCM(value float64) int16 {
	value = smafClampUnit(value)
	if value < 0 {
		return int16(value*32768 - 0.5)
	}
	return int16(value*32767 + 0.5)
}

func inflateSMAFHuffman(data []byte) []byte {
	if len(data) < 4 {
		return nil
	}
	decoded := binary.BigEndian.Uint32(data[:4])
	if decoded == 0 || decoded > 8<<20 {
		return nil
	}
	bits := data[4:]
	bitOffset := 0
	readBit := func() int {
		if bitOffset/8 >= len(bits) {
			return -1
		}
		value := int(bits[bitOffset/8] >> (7 - bitOffset%8) & 1)
		bitOffset++
		return value
	}
	readByte := func() int {
		value := 0
		for range 8 {
			bit := readBit()
			if bit < 0 {
				return -1
			}
			value = value<<1 | bit
		}
		return value
	}
	var left, right [511]int
	nextInternal := 256
	failed := false
	var readTree func(int) int
	readTree = func(depth int) int {
		if failed || depth > 256 {
			failed = true
			return 0
		}
		bit := readBit()
		if bit < 0 {
			failed = true
			return 0
		}
		if bit == 0 {
			value := readByte()
			if value < 0 {
				failed = true
				return 0
			}
			return value
		}
		if nextInternal > 510 {
			failed = true
			return 0
		}
		index := nextInternal
		nextInternal++
		left[index] = readTree(depth + 1)
		right[index] = readTree(depth + 1)
		return index
	}
	root := readTree(0)
	if failed {
		return nil
	}
	output := make([]byte, 0, decoded)
	for range decoded {
		node := root
		for guard := 0; node >= 256 && guard < 256; guard++ {
			bit := readBit()
			if bit < 0 {
				return output
			}
			if bit == 0 {
				node = left[node]
			} else {
				node = right[node]
			}
		}
		output = append(output, byte(node))
	}
	return output
}
