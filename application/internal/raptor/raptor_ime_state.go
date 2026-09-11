package raptor

import (
	"unicode/utf8"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/internal/ime"
)

func writeInputMethodState(r *Runtime, w *guest.StateWriter) {
	state := r.inputMethod()
	s := state.automata.Snapshot()
	composing := int32(0)
	if s.Composing {
		composing = 1
	}
	for _, value := range []int32{s.Mode, s.ComposingKey, s.ComposingIndex, s.Choseong, s.Jungseong, s.Jongseong, composing, s.LastKey, s.LastIndex, int32(state.preedit)} {
		w.U32(uint32(value))
	}
}

func parseInputMethodState(d *guest.StateDecoder) *inputMethod {
	s := ime.State{Mode: int32(d.U32()), ComposingKey: int32(d.U32()), ComposingIndex: int32(d.U32()), Choseong: int32(d.U32()), Jungseong: int32(d.U32()), Jongseong: int32(d.U32())}
	composing := d.U32()
	s.Composing = composing != 0
	s.LastKey = int32(d.U32())
	s.LastIndex = int32(d.U32())
	preedit := rune(d.U32())
	if s.Mode < 0 || s.Mode >= int32(ime.ModeCount) ||
		(s.ComposingKey != 0 && (s.ComposingKey < '0' || s.ComposingKey > '9')) ||
		s.ComposingIndex < 0 || s.ComposingIndex > 4 || s.Choseong < -1 || s.Choseong > 18 ||
		s.Jungseong < -3 || s.Jungseong > 20 || s.Jongseong < 0 || s.Jongseong > 27 ||
		composing > 1 || (s.LastKey != 0 && (s.LastKey < '0' || s.LastKey > '9')) ||
		s.LastIndex < 0 || s.LastIndex > 2 || !utf8.ValidRune(preedit) {
		d.Fail("invalid Raptor input-method state")
		return nil
	}
	state := &inputMethod{preedit: preedit}
	state.automata.Restore(s)
	return state
}
