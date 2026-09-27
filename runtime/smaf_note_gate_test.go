package runtime

import (
	"slices"
	"testing"
)

func TestSMAFNoteGatePairsSurviveTimelineSort(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		format   byte
		sequence []byte
	}{
		{"mobile", 2, []byte{0, 0x90, 60, 100, 100, 10, 0x90, 60, 100, 10}},
		{"handy-phone", 0, []byte{0, 0x20, 100, 10, 0x20, 10}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			d := &smafDecoder{rate: 8000, tracks: []smafTrack{{
				format: testCase.format, sequence: testCase.sequence,
			}}}
			if !d.buildEvents() || len(d.events) != 4 {
				t.Fatal("expected two gated notes")
			}
			first, second, shortOff, longOff := d.events[0], d.events[1], d.events[2], d.events[3]
			if first.noteID == 0 || first.noteID == second.noteID ||
				shortOff.noteID != second.noteID || longOff.noteID != first.noteID {
				t.Fatalf("gate identities lost: %+v", d.events)
			}
			if first.a != 60 || second.a != 60 || shortOff.sample != 160 || longOff.sample != 800 {
				t.Fatalf("unexpected notes or timing: %+v", d.events)
			}
			stream := newSMAFRenderStream(d)
			stream.render(nil, shortOff.sample+1, true)
			if !d.pool[0].keyDown || d.pool[1].keyDown {
				t.Fatal("shorter second gate must release only the second note")
			}
			stream.render(nil, longOff.sample+1, true)
			if d.pool[0].keyDown || d.pool[1].keyDown {
				t.Fatal("both gates must have ended")
			}
		})
	}
}

func TestSMAFNoteGateIdentityIsUniqueAcrossTracks(t *testing.T) {
	d := &smafDecoder{rate: 8000, tracks: []smafTrack{
		{format: 0, sequence: []byte{0, 0x20, 100}},
		{number: 1, format: 0, sequence: []byte{0, 0x20, 100}},
		{number: 2, format: 2, sequence: []byte{0, 0x90, 60, 100, 100}},
	}}
	if !d.buildEvents() {
		t.Fatal("no events")
	}
	ids := map[uint32]int{}
	for _, e := range d.events {
		if e.noteID == 0 {
			t.Fatal("missing gate identity")
		}
		ids[e.noteID]++
	}
	if len(ids) != 3 {
		t.Fatalf("got %d identities, want 3", len(ids))
	}
	for id, count := range ids {
		if count != 2 {
			t.Fatalf("gate %d has %d events", id, count)
		}
	}
}

func TestSMAFNoteGateDoesNotReleaseStolenReplacement(t *testing.T) {
	d := &smafDecoder{rate: 8000}
	for i := 0; i <= len(d.pool); i++ {
		d.fire(smafEvent{kind: smafNoteOn, channel: 0, a: 60, b: 100, noteID: uint32(i + 1)})
	}
	if d.pool[0].noteID != 33 {
		t.Fatal("expected oldest slot to be stolen")
	}
	d.fire(smafEvent{kind: smafNoteOff, channel: 0, a: 60, noteID: 1})
	if !d.pool[0].keyDown {
		t.Fatal("stolen note's gate released its replacement")
	}
	// Identity alone must not make a malformed channel or pitch match.
	d.fire(smafEvent{kind: smafNoteOff, channel: 1, a: 60, noteID: 33})
	d.fire(smafEvent{kind: smafNoteOff, channel: 0, a: 61, noteID: 33})
	if !d.pool[0].keyDown {
		t.Fatal("wrong channel or pitch released the note")
	}
	d.fire(smafEvent{kind: smafNoteOff, channel: 0, a: 60, noteID: 33})
	if d.pool[0].keyDown {
		t.Fatal("replacement's own gate was ignored")
	}
}

func TestSMAFNoteGateSkipsReleasedTailsAndXOF(t *testing.T) {
	for _, xof := range []bool{false, true} {
		d := &smafDecoder{rate: 8000}
		patch := defaultSMAFPatch()
		for i := range patch.operators {
			patch.operators[i].xof = xof
		}
		d.voices = []smafParsedVoice{{valid: true, patch: patch}}
		first := smafEvent{kind: smafNoteOn, channel: 0, a: 60, b: 100}
		d.fire(first)
		d.pool[0].tick(true)
		d.fire(smafEvent{kind: smafNoteOff, channel: 0, a: 60})
		if d.pool[0].keyDown || !d.pool[0].active {
			t.Fatal("expected an active released tail")
		}
		d.fire(first)
		d.fire(smafEvent{kind: smafNoteOff, channel: 0, a: 60})
		if d.pool[1].keyDown {
			t.Fatalf("XOF=%v: release tail consumed the next note's key-off", xof)
		}
	}
}

func TestSMAFNoteGateBudgetKeepsPairsWhole(t *testing.T) {
	d := &smafDecoder{events: make([]smafEvent, maxSMAFEvents-1)}
	d.addGatedNote(0, 1, 0, 60, 100)
	if len(d.events) != maxSMAFEvents-1 {
		t.Fatal("partial pair added")
	}
	d.events = d.events[:maxSMAFEvents-2]
	d.addGatedNote(0, 1, 0, 60, 100)
	if len(d.events) != maxSMAFEvents {
		t.Fatal("pair at budget boundary was dropped")
	}
	on, off := d.events[maxSMAFEvents-2], d.events[maxSMAFEvents-1]
	if on.noteID == 0 || on.noteID != off.noteID {
		t.Fatal("boundary pair lost its identity")
	}
	d.addGatedNote(1, 2, 0, 60, 100)
	if len(d.events) != maxSMAFEvents {
		t.Fatal("event budget exceeded")
	}
}

func TestSMAFNoteGatePreservesUnambiguousPCM(t *testing.T) {
	score := smafScore([]byte{
		0, 0x90, 60, 100, 100,
		10, 0x90, 64, 100, 10,
		10, 0x91, 60, 100, 10,
	})
	paired, unpaired := &smafDecoder{rate: 44_100}, &smafDecoder{rate: 44_100}
	for _, d := range []*smafDecoder{paired, unpaired} {
		if !d.parse(score) || !d.buildEvents() {
			t.Fatal("no events")
		}
	}
	for i := range unpaired.events {
		unpaired.events[i].noteID = 0
	}
	left, right := newSMAFRenderStream(paired), newSMAFRenderStream(unpaired)
	if !slices.Equal(left.renderUntil(nil, left.end), right.renderUntil(nil, right.end)) {
		t.Fatal("gate identity changed PCM where pitch/channel lookup is unambiguous")
	}
}
