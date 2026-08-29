package skvm

import "github.com/mirusu400/aram-core/internal/ime"

// textComponentHandlerState is the native state on the singleton
// com/xce/lcdui/TextComponentHandler. It owns the keypad input method and the
// reference of the guest TextComponent currently registered through
// setTextComponent, which is the object the composed characters are pushed
// into.
type textComponentHandlerState struct {
	automata  ime.Automata
	component uint32
}

// newTextComponentHandlerState builds a handler whose input method starts in
// Hangul: these are Korean titles and the field shows no mode indicator, so
// that is what the handset comes up in. '*' cycles to the English and numeric
// modes.
func newTextComponentHandlerState() *textComponentHandlerState {
	return &textComponentHandlerState{automata: ime.New(ime.ModeKorean)}
}
