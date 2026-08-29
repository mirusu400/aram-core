package ktf

import (
	"github.com/mirusu400/aram-core/internal/ime"
	"github.com/mirusu400/aram-core/profile"
)

// KWIS delivers a key to a component three ways; only the press composes, so a
// release or an auto-repeat must not type the character a second time.
const (
	ktfLWCKeyPressed  = 1
	ktfLWCKeyReleased = 2
)

// ktfLWCTextInputClasses are the LWC components that own an editable field. A
// title hands the component every keypad press through keyNotify and expects
// the platform - not the title - to turn those presses into text, then reads
// the result back through getString.
var ktfLWCTextInputClasses = map[string]bool{
	"org/kwis/msp/lwc/TextComponent":      true,
	"org/kwis/msp/lwc/TextFieldComponent": true,
	"org/kwis/msp/lwc/TextBoxComponent":   true,
}

// editLWCText runs one keypad press through the field's input method and
// reports whether the input method consumed it.
//
// 프린세스메이커4 asks for two names before it will start, and forwarded every
// press to TextFieldComponent.keyNotify - '1' as 49, '2' as 50, the clear key
// as -16 - to a handler that returned "not consumed" and changed nothing, so
// the fields stayed empty and the game could not be started (issue #88). The
// composition itself is the shared handset automata the SKT backend already
// drives: Hangul 천지인 by default, with '*' cycling to the English and numeric
// modes and '#' inserting a space.
//
// The caret is always at the end of the field. Neither the LWC field nor the
// automata models an interior cursor: every op it emits appends, rewrites the
// glyph being multi-tapped, or removes the last one.
func (r *Runtime) editLWCText(
	instance uint32,
	state *ktfLWCComponent,
	keyType, key int32,
) (bool, error) {
	if keyType != ktfLWCKeyPressed {
		// A release of a key the field composed with is still the field's, so
		// the title does not also act on it.
		return keyType == ktfLWCKeyReleased && r.lwcTextInputKey(instance, key), nil
	}
	automata := r.lwcTextInputAutomata(instance)
	runes := []rune(ktfLWCFieldText(r, state))
	updated := runes
	switch profile.KeyCode(key) {
	case profile.KeyClear:
		automata.Commit()
		if len(runes) == 0 {
			// An empty field passes the clear key back so the title can use it
			// to leave the screen, which is what a handset does.
			return false, nil
		}
		updated = runes[:len(runes)-1]
	default:
		ops, handled := automata.Press(key)
		if !handled {
			return false, nil
		}
		updated = applyIMEOps(runes, ops)
	}
	// A field that is already full still rotates a multi-tap glyph but cannot
	// grow; the handset simply ignores the press that would overflow it.
	if limit := r.lwcMaxLengths[instance]; limit > 0 && int32(len(updated)) > limit {
		return true, nil
	}
	text, err := r.NewJavaString(string(updated))
	if err != nil {
		return false, err
	}
	state.text = text
	r.initializeLWCTextSize(state, text, true)
	r.invalidateLWC(instance)
	r.markLWCRepaint(instance)
	return true, nil
}

// ktfLWCFieldText reads a field's current text. A field the title never gave a
// string to holds a null reference, which javaStringValue renders as "null" -
// the text of the Java expression, not of the field.
func ktfLWCFieldText(r *Runtime, state *ktfLWCComponent) string {
	if state == nil || state.text == 0 {
		return ""
	}
	return r.javaStringValue(state.text)
}

// applyIMEOps folds the automata's edits into the field, which only ever
// changes at its end.
func applyIMEOps(runes []rune, ops []ime.Op) []rune {
	for _, op := range ops {
		switch op.Kind {
		case ime.OpInsert:
			runes = append(runes, op.Char)
		case ime.OpReplace:
			if len(runes) == 0 {
				runes = append(runes, op.Char)
				continue
			}
			runes[len(runes)-1] = op.Char
		case ime.OpDelete:
			if len(runes) > 0 {
				runes = runes[:len(runes)-1]
			}
		}
	}
	return runes
}

// lwcTextInputKey reports whether the field's input method takes this key at
// all, without composing anything with it.
func (r *Runtime) lwcTextInputKey(instance uint32, key int32) bool {
	if profile.KeyCode(key) == profile.KeyClear {
		return ktfLWCFieldText(r, r.lwcComponent(instance)) != ""
	}
	return key >= '0' && key <= '9' ||
		key == int32(profile.KeyAsterisk) || key == int32(profile.KeyPound)
}

// lwcTextInputAutomata returns the field's input method, creating it on first
// use. The half-composed syllable it holds is a keystroke away from being
// rebuilt, so it is deliberately not part of the save state: a state loaded
// mid-syllable starts the next one fresh and keeps the text, which is the part
// the title reads.
func (r *Runtime) lwcTextInputAutomata(instance uint32) *ime.Automata {
	if automata := r.lwcTextInput[instance]; automata != nil {
		return automata
	}
	// Korean titles with no mode indicator on the field come up in Hangul.
	automata := ime.New(ime.ModeKorean)
	r.lwcTextInput[instance] = &automata
	return &automata
}

// resetLWCTextInput ends any composition on a field whose text the title
// replaced, so the next press starts a new glyph instead of rotating one that
// is no longer there.
func (r *Runtime) resetLWCTextInput(instance uint32) {
	if automata := r.lwcTextInput[instance]; automata != nil {
		automata.Commit()
	}
}
