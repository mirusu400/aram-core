package runtime

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// smfVLQ encodes a delta time the way a track chunk carries it.
func smfVLQ(value int) []byte {
	if value == 0 {
		return []byte{0}
	}
	var stack []byte
	for value > 0 {
		stack = append(stack, byte(value&0x7f))
		value >>= 7
	}
	encoded := make([]byte, 0, len(stack))
	for index := len(stack) - 1; index >= 0; index-- {
		part := stack[index]
		if index != 0 {
			part |= 0x80
		}
		encoded = append(encoded, part)
	}
	return encoded
}

// smfFile assembles a Standard MIDI File from already-encoded track bodies.
func smfFile(format, division int, tracks ...[]byte) []byte {
	header := make([]byte, 14)
	copy(header[0:4], "MThd")
	binary.BigEndian.PutUint32(header[4:8], 6)
	binary.BigEndian.PutUint16(header[8:10], uint16(format))
	binary.BigEndian.PutUint16(header[10:12], uint16(len(tracks)))
	binary.BigEndian.PutUint16(header[12:14], uint16(division))
	file := header
	for _, track := range tracks {
		chunk := make([]byte, 8)
		copy(chunk[0:4], "MTrk")
		binary.BigEndian.PutUint32(chunk[4:8], uint32(len(track)))
		file = append(file, chunk...)
		file = append(file, track...)
	}
	return file
}

func smfEndOfTrack() []byte {
	return []byte{0x00, 0xff, 0x2f, 0x00}
}

// smfTempo encodes a tempo meta event at the current delta.
func smfTempo(microsecondsPerQuarter int) []byte {
	return []byte{
		0x00, 0xff, 0x51, 0x03,
		byte(microsecondsPerQuarter >> 16),
		byte(microsecondsPerQuarter >> 8),
		byte(microsecondsPerQuarter),
	}
}

// TestSMFRendersANoteAtItsPitch is the whole point of the decoder: a Standard
// MIDI File has to reach the FM synthesiser as notes rather than as silence.
// The device advertises MIDI in MEDIADEVICES, so a title that asks for it and
// gets a clip that decodes to nothing has no music at all.
func TestSMFRendersANoteAtItsPitch(t *testing.T) {
	const division = 480
	track := []byte{}
	track = append(track, smfTempo(500_000)...) // 120 BPM: a quarter is 500 ms
	track = append(track, 0x00, 0xc0, 0x00)     // program 0
	track = append(track, 0x00, 0x90, 69, 100)  // A4 on
	track = append(track, smfVLQ(division*2)...)
	track = append(track, 0x80, 69, 0) // off one second later
	track = append(track, smfEndOfTrack()...)

	decoded := decodeSMFPCM16(smfFile(0, division, track), 44_100)
	if decoded == nil || len(decoded.samples) == 0 {
		t.Fatal("decodeSMFPCM16 rendered nothing")
	}
	mono := make([]float64, 0, len(decoded.samples)/2)
	for index := 0; index+1 < len(decoded.samples); index += 2 {
		mono = append(mono, float64(decoded.samples[index])/32768)
	}
	if len(mono) < 20_480 {
		t.Fatalf("rendered %d frames", len(mono))
	}
	// Measure over the sustain, past the attack.
	window := mono[4_096:20_480]
	fundamental := toneMagnitude(window, 44_100, 440)
	for _, other := range []float64{220, 330, 587} {
		magnitude := toneMagnitude(window, 44_100, other)
		if magnitude > fundamental {
			t.Fatalf(
				"%.0f Hz (%.5f) is louder than the A4 that was played (%.5f)",
				other, magnitude, fundamental,
			)
		}
	}
	if fundamental < 0.005 {
		t.Fatalf("A4 came out at %.5f, want an audible note", fundamental)
	}
}

// TestSMFHonoursTempoAndDivision pins the tick-to-time conversion. Getting it
// wrong plays a score at the wrong speed while still sounding like music, so
// nothing but a timing check catches it.
func TestSMFHonoursTempoAndDivision(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		division int
		tempo    int
		ticks    int
		want     time.Duration
	}{
		{"120bpm quarter", 480, 500_000, 480, 500 * time.Millisecond},
		{"120bpm two beats", 480, 500_000, 960, time.Second},
		{"240bpm quarter", 480, 250_000, 480, 250 * time.Millisecond},
		{"coarse division", 96, 500_000, 96, 500 * time.Millisecond},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			track := append([]byte{}, smfTempo(testCase.tempo)...)
			track = append(track, 0x00, 0x90, 60, 100)
			track = append(track, smfVLQ(testCase.ticks)...)
			track = append(track, 0x80, 60, 0)
			track = append(track, smfEndOfTrack()...)
			decoder := decodeSMFEvents(
				smfFile(0, testCase.division, track), 44_100,
			)
			if decoder == nil {
				t.Fatal("decodeSMFEvents returned nil")
			}
			var released uint64
			found := false
			for _, event := range decoder.events {
				if event.kind == smafNoteOff {
					released, found = event.sample, true
				}
			}
			if !found {
				t.Fatal("no note off recorded")
			}
			got := time.Duration(released) * time.Second / 44_100
			difference := got - testCase.want
			if difference < -time.Millisecond || difference > time.Millisecond {
				t.Errorf("note released at %v, want %v", got, testCase.want)
			}
		})
	}
}

// TestSMFTempoChangeAppliesFromItsTick checks that a tempo meta event partway
// through retimes only what follows it.
func TestSMFTempoChangeAppliesFromItsTick(t *testing.T) {
	const division = 480
	track := append([]byte{}, smfTempo(500_000)...)
	track = append(track, 0x00, 0x90, 60, 100)
	track = append(track, smfVLQ(division)...) // 500 ms at 120 BPM
	// 250000 microseconds per quarter is 240 BPM.
	track = append(track, 0xff, 0x51, 0x03, 0x03, 0xd0, 0x90)
	track = append(track, smfVLQ(division)...) // 250 ms at 240 BPM
	track = append(track, 0x80, 60, 0)
	track = append(track, smfEndOfTrack()...)
	decoder := decodeSMFEvents(smfFile(0, division, track), 44_100)
	if decoder == nil {
		t.Fatal("decodeSMFEvents returned nil")
	}
	var released uint64
	for _, event := range decoder.events {
		if event.kind == smafNoteOff {
			released = event.sample
		}
	}
	got := time.Duration(released) * time.Second / 44_100
	want := 750 * time.Millisecond
	difference := got - want
	if difference < -time.Millisecond || difference > time.Millisecond {
		t.Errorf("note released at %v, want %v", got, want)
	}
}

// TestSMFReadsRunningStatus checks the shorthand every real file uses: after a
// status byte, following messages of the same kind omit it. Reading those as
// status bytes turns a melody into nonsense.
func TestSMFReadsRunningStatus(t *testing.T) {
	const division = 480
	track := []byte{0x00, 0x90, 60, 100} // explicit status
	for _, note := range []byte{62, 64, 65} {
		track = append(track, smfVLQ(division/4)...)
		track = append(track, note, 100) // running status
	}
	track = append(track, smfVLQ(division)...)
	track = append(track, 0xb0, 123, 0) // all notes off
	track = append(track, smfEndOfTrack()...)
	decoder := decodeSMFEvents(smfFile(0, division, track), 44_100)
	if decoder == nil {
		t.Fatal("decodeSMFEvents returned nil")
	}
	started, released := 0, 0
	for _, event := range decoder.events {
		switch event.kind {
		case smafNoteOn:
			started++
		case smafNoteOff:
			released++
		}
	}
	if started != 4 {
		t.Errorf("recorded %d note ons, want 4", started)
	}
	if released != 4 {
		t.Errorf(
			"recorded %d note offs, want the 4 the all-notes-off ends",
			released,
		)
	}
}

// TestSMFMergesTracksOnOneTimeline pins format 1 handling: every track counts
// ticks from the start of the file and they sound together.
func TestSMFMergesTracksOnOneTimeline(t *testing.T) {
	const division = 480
	tempoTrack := append([]byte{}, smfTempo(500_000)...)
	tempoTrack = append(tempoTrack, smfEndOfTrack()...)
	first := []byte{0x00, 0x90, 60, 100}
	first = append(first, smfVLQ(division)...)
	first = append(first, 0x80, 60, 0)
	first = append(first, smfEndOfTrack()...)
	second := []byte{0x00, 0x91, 67, 100}
	second = append(second, smfVLQ(division)...)
	second = append(second, 0x81, 67, 0)
	second = append(second, smfEndOfTrack()...)

	decoder := decodeSMFEvents(
		smfFile(1, division, tempoTrack, first, second), 44_100,
	)
	if decoder == nil {
		t.Fatal("decodeSMFEvents returned nil")
	}
	starts := map[int]uint64{}
	for _, event := range decoder.events {
		if event.kind == smafNoteOn {
			starts[event.channel] = event.sample
		}
	}
	if len(starts) != 2 {
		t.Fatalf("notes started on %d channels, want 2", len(starts))
	}
	if starts[0] != starts[1] {
		t.Errorf(
			"the two tracks start at %d and %d, want the same sample",
			starts[0], starts[1],
		)
	}
}

// TestSMFUsesDrumsOnTheReservedChannel checks the one channel General MIDI
// spells differently: channel 10, index 9, is a drum kit, so a note there
// names a percussion instrument rather than a pitch.
func TestSMFUsesDrumsOnTheReservedChannel(t *testing.T) {
	track := []byte{0x00, 0x99, 38, 100, 0x60, 0x89, 38, 0}
	track = append(track, smfEndOfTrack()...)
	decoder := decodeSMFEvents(smfFile(0, 480, track), 44_100)
	if decoder == nil {
		t.Fatal("decodeSMFEvents returned nil")
	}
	if !decoder.channels[smfPercussionChannel].rhythm {
		t.Fatal("the reserved channel is not marked as a drum kit")
	}
}

// TestSMFRejectsWhatIsNotAnSMF keeps the sniff from claiming bytes another
// decoder owns, which would turn a working SMAF score into silence.
func TestSMFRejectsWhatIsNotAnSMF(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty":     nil,
		"short":     []byte("MThd"),
		"wrong id":  append([]byte("MMMD"), make([]byte, 32)...),
		"wave":      pcmWave(8_000, 1, []int16{1, 2, 3, 4}),
		"bad chunk": append([]byte("MThd\x00\x00\x00\x08"), make([]byte, 32)...),
	} {
		if looksLikeSMF(data) {
			t.Errorf("%s was taken for a MIDI file", name)
		}
		if decodeSMFEvents(data, 44_100) != nil {
			t.Errorf("%s decoded as a MIDI file", name)
		}
	}
}

// TestSMFSurvivesTruncation checks that a chunk cut short stops the track it
// belongs to rather than the process. Guest content is arbitrary bytes.
func TestSMFSurvivesTruncation(t *testing.T) {
	const division = 480
	track := append([]byte{}, smfTempo(500_000)...)
	track = append(track, 0x00, 0x90, 60, 100)
	track = append(track, smfVLQ(division)...)
	track = append(track, 0x80, 60, 0)
	track = append(track, smfEndOfTrack()...)
	whole := smfFile(0, division, track)
	for cut := len(whole) - 1; cut >= 0; cut-- {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("%d bytes panicked: %v", cut, recovered)
				}
			}()
			_ = decodeSMFEvents(whole[:cut], 44_100)
		}()
	}
}

// TestMediaPlaysAnSMFClip pins the wiring. The media service sniffs a clip by
// content, so a Java Clip or a WIPI-C media handle holding SMF bytes has to
// reach the synthesiser without either caller knowing what a MIDI file is.
func TestMediaPlaysAnSMFClip(t *testing.T) {
	const division = 480
	track := append([]byte{}, smfTempo(500_000)...)
	track = append(track, 0x00, 0xc0, 0x00, 0x00, 0x90, 69, 110)
	track = append(track, smfVLQ(division*2)...)
	track = append(track, 0x80, 69, 0)
	track = append(track, smfEndOfTrack()...)
	source := smfFile(0, division, track)

	media, err := NewMedia(NewRegistry(32), DefaultMediaLimits())
	if err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(16, 32)
	clip, err := media.CreateClip(3, "audio/midi", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(3, clip, source); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(3, clip, 1); err != nil {
		t.Fatal(err)
	}
	info, err := media.Info(3, clip)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Decoded {
		t.Fatal("the clip did not decode, so it would play silently forever")
	}
	if info.Duration < 900*time.Millisecond {
		t.Errorf(
			"clip reports %v, want at least the second it plays for",
			info.Duration,
		)
	}
	if err := media.Advance(0, 500*time.Millisecond, bus); err != nil {
		t.Fatal(err)
	}
	audio := media.Drain()
	peak := 0.0
	for _, sample := range audio.PCM16 {
		if value := math.Abs(float64(sample)); value > peak {
			peak = value
		}
	}
	if peak < 500 {
		t.Fatalf("mixed audio peaks at %.0f, want the note to be audible", peak)
	}
}
