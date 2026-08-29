package skvm

// Hangul (천지인) composition for the XCE input-method automata.
//
// MHIma.c punts its "KO" mode to a separate platform component
// (send_platform_event(3, ...)), so the exact keypad-to-jamo layout and the
// syllable automata are being recovered from the SPH-W8300 firmware dump. Until
// those tables are wired in, Korean mode reports keys as not-composed so the
// English and numeric modes remain usable; it never fabricates characters.

// koreanAutomata holds the in-progress Hangul syllable. The fields are exported
// through the handler's native-state serialisation so a half-composed syllable
// survives a save-state.
type koreanAutomata struct {
	// choseong/jungseong/jongseong index the current syllable's leading
	// consonant, vowel, and trailing consonant (-1 when absent). composing is
	// true while a syllable is being built and shown through imeReplace.
	choseong  int
	jungseong int
	jongseong int
	composing bool
	// lastKey/lastIndex track the multi-tap rotation on the active jamo key.
	lastKey   int32
	lastIndex int
}

func (k *koreanAutomata) reset() {
	k.choseong = -1
	k.jungseong = -1
	k.jongseong = -1
	k.composing = false
	k.lastKey = 0
	k.lastIndex = 0
}

// press feeds one keypad code to the Hangul automata. It is a placeholder until
// the firmware 천지인 tables are extracted; returning (nil, false) leaves the key
// for the caller rather than inventing a glyph.
func (k *koreanAutomata) press(key int32) ([]imeOp, bool) {
	_ = key
	return nil, false
}
