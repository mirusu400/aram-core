package runtime

import (
	"encoding/binary"
	"sort"
	"time"
)

// A Standard MIDI File carries the same thing a SMAF score does - channel
// voice messages on a timeline - so it is read into the events the FM
// synthesiser already plays rather than given a synthesiser of its own. The
// device advertises MIDI support (MEDIADEVICES answers "audio/MIDI,audio/MP3"),
// and a title that takes it at its word used to get a clip that decoded to
// nothing: silent, zero length, and never completing. Two titles in the local
// corpus ship SMF, one of them for its whole soundtrack.
//
// Only what the synthesiser can act on is translated. Aftertouch, channel
// pressure, and the meta events that carry key and time signatures change
// nothing about the notes and are skipped.
const (
	// smfDefaultTempo is the tempo a file is played at until it sets one:
	// 500000 microseconds per quarter note, which is 120 BPM.
	smfDefaultTempo = 500_000
	// smfPercussionChannel is the channel General MIDI reserves for the drum
	// kit. The SMAF channel model already has a rhythm flag for this.
	smfPercussionChannel = 9
	maxSMFTracks         = 64
)

// looksLikeSMF reports whether the bytes open with an SMF header chunk.
func looksLikeSMF(data []byte) bool {
	return len(data) >= 14 && string(data[:4]) == "MThd" &&
		binary.BigEndian.Uint32(data[4:8]) == 6
}

// looksLikeSequencedScore reports whether the bytes are a score the FM
// synthesiser can play, in either container the device accepts.
func looksLikeSequencedScore(data []byte) bool {
	return looksLikeSMAF(data) || looksLikeSMF(data)
}

// smfTimedEvent is one message with the tick it happens on. A file's tracks
// are read separately and merged, so the track and the position within it are
// kept to break ties the way the file orders them.
type smfTimedEvent struct {
	tick   uint64
	track  int
	order  int
	status byte
	data1  byte
	data2  byte
	tempo  uint32
}

// smfTempoEvent marks a tempo change rather than a channel message. No channel
// status byte has this value, so it cannot collide with a real message.
const smfTempoEvent = 0

func decodeSMFEvents(data []byte, rate uint32) *smafDecoder {
	if !looksLikeSMF(data) {
		return nil
	}
	division := binary.BigEndian.Uint16(data[12:14])
	if division == 0 {
		return nil
	}
	var merged []smfTimedEvent
	tracks := 0
	for offset := 14; offset+8 <= len(data) && tracks < maxSMFTracks; {
		length := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		if length < 0 || length > len(data)-body {
			length = len(data) - body
		}
		if string(data[offset:offset+4]) == "MTrk" {
			merged = readSMFTrack(data[body:body+length], tracks, merged)
			tracks++
		}
		offset = body + length
	}
	if len(merged) == 0 {
		return nil
	}
	// Every track counts ticks from the start of the file, so merging them by
	// tick reproduces the order a sequencer would play them in. The tie break
	// keeps a track's own messages in the order the file wrote them.
	sort.SliceStable(merged, func(i, j int) bool {
		left, right := merged[i], merged[j]
		if left.tick != right.tick {
			return left.tick < right.tick
		}
		if left.track != right.track {
			return left.track < right.track
		}
		return left.order < right.order
	})
	decoder := &smafDecoder{rate: rate}
	for index := range decoder.channels {
		decoder.channels[index].volume = 100.0 / 127
		decoder.channels[index].expression = 1
	}
	decoder.channels[smfPercussionChannel].rhythm = true
	if !buildSMFEvents(decoder, merged, division) {
		return nil
	}
	return decoder
}

// buildSMFEvents walks the merged messages, converting ticks to milliseconds
// against the tempo in force, and records what the synthesiser can play.
func buildSMFEvents(
	decoder *smafDecoder,
	merged []smfTimedEvent,
	division uint16,
) bool {
	tempo := uint32(smfDefaultTempo)
	ticksPerQuarter := float64(division)
	ticksPerSecond := 0.0
	if division&0x8000 != 0 {
		// An SMPTE division names frames per second in the high byte, as a
		// negative number, and ticks per frame in the low one. Tempo does not
		// apply: the timeline is already absolute.
		frames := float64(-int8(division >> 8))
		ticksPerSecond = frames * float64(division&0xff)
		if ticksPerSecond <= 0 {
			return false
		}
	}
	var (
		lastTick     uint64
		milliseconds float64
		sounding     [16][128]bool
		at           uint64
	)
	for _, event := range merged {
		if event.tick > lastTick {
			step := float64(event.tick - lastTick)
			if ticksPerSecond != 0 {
				milliseconds += step / ticksPerSecond * 1_000
			} else {
				milliseconds += step * float64(tempo) / ticksPerQuarter / 1_000
			}
			lastTick = event.tick
		}
		at = decoder.sampleAt(milliseconds)
		if event.status == smfTempoEvent {
			tempo = event.tempo
			if tempo == 0 {
				tempo = smfDefaultTempo
			}
			continue
		}
		if !applySMFMessage(decoder, event, at, &sounding) {
			break
		}
	}
	// A file that ends while notes are held would otherwise leave voices
	// sounding until the render's own ceiling stopped them.
	for channel := range sounding {
		for note := range sounding[channel] {
			if !sounding[channel][note] {
				continue
			}
			decoder.addEvent(smafEvent{
				sample:  at,
				kind:    smafNoteOff,
				channel: channel,
				a:       note,
			})
		}
	}
	if len(decoder.events) == 0 {
		return false
	}
	sortSMAFEvents(decoder.events)
	return true
}

// applySMFMessage records one channel voice message. It reports false when the
// event budget is spent, which stops the walk.
func applySMFMessage(
	decoder *smafDecoder,
	event smfTimedEvent,
	at uint64,
	sounding *[16][128]bool,
) bool {
	channel := int(event.status & 0x0f)
	note := int(event.data1 & 0x7f)
	switch event.status & 0xf0 {
	case 0x80:
		if !sounding[channel][note] {
			return true
		}
		sounding[channel][note] = false
		return decoder.addEvent(smafEvent{
			sample: at, kind: smafNoteOff, channel: channel, a: note,
		})
	case 0x90:
		// A note on with no velocity is how most files release a note.
		if event.data2&0x7f == 0 {
			if !sounding[channel][note] {
				return true
			}
			sounding[channel][note] = false
			return decoder.addEvent(smafEvent{
				sample: at, kind: smafNoteOff, channel: channel, a: note,
			})
		}
		sounding[channel][note] = true
		return decoder.addEvent(smafEvent{
			sample: at, kind: smafNoteOn, channel: channel,
			a: note, b: int(event.data2 & 0x7f),
		})
	case 0xb0:
		return applySMFController(decoder, event, at, sounding)
	case 0xc0:
		return decoder.addEvent(smafEvent{
			sample: at, kind: smafProgram, channel: channel, a: note,
		})
	case 0xe0:
		// Pitch bend is fourteen bits centred on 8192, and the synthesiser
		// wants it as a signed offset from that centre.
		bend := int(event.data1&0x7f) | int(event.data2&0x7f)<<7
		return decoder.addEvent(smafEvent{
			sample: at, kind: smafPitchBend, channel: channel, a: bend - 8192,
		})
	}
	return true
}

func applySMFController(
	decoder *smafDecoder,
	event smfTimedEvent,
	at uint64,
	sounding *[16][128]bool,
) bool {
	channel := int(event.status & 0x0f)
	value := int(event.data2 & 0x7f)
	kind := smafEventKind(0)
	switch event.data1 & 0x7f {
	case 0:
		kind = smafBankMSB
	case 7:
		kind = smafVolume
	case 10:
		kind = smafPan
	case 11:
		kind = smafExpression
	case 32:
		kind = smafBankLSB
	case 120, 123:
		// All sound off and all notes off both silence the channel.
		for note := range sounding[channel] {
			if !sounding[channel][note] {
				continue
			}
			sounding[channel][note] = false
			if !decoder.addEvent(smafEvent{
				sample: at, kind: smafNoteOff, channel: channel, a: note,
			}) {
				return false
			}
		}
		return true
	default:
		return true
	}
	return decoder.addEvent(smafEvent{
		sample: at, kind: kind, channel: channel, a: value,
	})
}

// readSMFTrack reads one MTrk chunk into absolute-tick messages, appending to
// out. A malformed chunk stops the track rather than the file: the tracks
// already read still play.
func readSMFTrack(
	track []byte,
	index int,
	out []smfTimedEvent,
) []smfTimedEvent {
	var (
		tick   uint64
		order  int
		status byte
	)
	for offset := 0; offset < len(track); {
		delta, ok := readSMFVLQ(track, &offset)
		if !ok || offset >= len(track) {
			break
		}
		tick += uint64(delta)
		current := track[offset]
		switch {
		case current == 0xff:
			offset++
			if offset >= len(track) {
				return out
			}
			meta := track[offset]
			offset++
			length, lengthOK := readSMFVLQ(track, &offset)
			if !lengthOK || length > len(track)-offset {
				return out
			}
			if meta == 0x51 && length == 3 {
				out = append(out, smfTimedEvent{
					tick: tick, track: index, order: order,
					status: smfTempoEvent,
					tempo: uint32(track[offset])<<16 |
						uint32(track[offset+1])<<8 |
						uint32(track[offset+2]),
				})
				order++
			}
			offset += length
			if meta == 0x2f {
				return out
			}
			continue
		case current == 0xf0 || current == 0xf7:
			offset++
			length, lengthOK := readSMFVLQ(track, &offset)
			if !lengthOK || length > len(track)-offset {
				return out
			}
			offset += length
			continue
		case current >= 0xf1:
			// System common messages carry nothing the synthesiser reads, and
			// none of them is valid running status.
			offset++
			continue
		case current&0x80 != 0:
			status = current
			offset++
		case status == 0:
			// A data byte with no status to run under means the track is not
			// what it claims to be.
			return out
		}
		wanted := 2
		if kind := status & 0xf0; kind == 0xc0 || kind == 0xd0 {
			wanted = 1
		}
		if offset+wanted > len(track) {
			return out
		}
		event := smfTimedEvent{
			tick: tick, track: index, order: order,
			status: status, data1: track[offset],
		}
		if wanted == 2 {
			event.data2 = track[offset+1]
		}
		offset += wanted
		order++
		out = append(out, event)
	}
	return out
}

// readSMFVLQ reads a variable-length quantity. It reports false when the value
// runs past the end of the chunk or past the four bytes the format allows,
// which is how a truncated or misaligned track is caught.
func readSMFVLQ(data []byte, offset *int) (int, bool) {
	value := 0
	for read := 0; read < 4; read++ {
		if *offset >= len(data) {
			return 0, false
		}
		next := data[*offset]
		*offset++
		value = value<<7 | int(next&0x7f)
		if next&0x80 == 0 {
			return value, true
		}
	}
	return 0, false
}

// decodeSMFPCM16 renders a Standard MIDI File whole.
func decodeSMFPCM16(data []byte, sampleRate uint32) *decodedPCM {
	if sampleRate < 8_000 || sampleRate > 192_000 {
		return nil
	}
	decoder := decodeSMFEvents(data, sampleRate)
	if decoder == nil {
		return nil
	}
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
			uint64(len(samples)/2) * uint64(time.Second) / uint64(sampleRate),
		),
	}
}

// decodeSMFLazyPCM16 renders a Standard MIDI File the way a SMAF score is
// rendered: whole when it is short, incrementally when it is long enough that
// rendering it up front would stall the frame that started it.
func decodeSMFLazyPCM16(data []byte, sampleRate uint32) *decodedPCM {
	if sampleRate < 8_000 || sampleRate > 192_000 {
		return nil
	}
	decoder := decodeSMFEvents(data, sampleRate)
	if decoder == nil {
		return nil
	}
	return finishLazyScoreDecode(decoder, sampleRate, func() *smafDecoder {
		return decodeSMFEvents(data, sampleRate)
	})
}

// decodeScoreLazyPCM16 decodes whichever score container the bytes hold.
func decodeScoreLazyPCM16(data []byte, sampleRate uint32) *decodedPCM {
	if looksLikeSMF(data) {
		return decodeSMFLazyPCM16(data, sampleRate)
	}
	return decodeSMAFLazyPCM16(data, sampleRate)
}
