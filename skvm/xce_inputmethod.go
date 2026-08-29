package skvm

// XCE (SK-VM) input-method automata for com/xce/lcdui/TextComponentHandler.
//
// A SKT title routes every keypad press through the platform text-input
// handler (getTextComponentHandler -> setTextComponent -> keyPressed) and reads
// the composed characters back through the com/xce/lcdui/TextComponent callback
// interface. The handler owns a multi-tap automata whose English and numeric
// behaviour is reconstructed from the SPH-W8300 firmware InputMethod HAL
// (MHIma.c: modes "EN/S", "EN/L", "N123", "KO"); this file ports that automata
// so the composed text actually reaches the guest field.
//
// The automata is kept as a pure state machine that emits a list of imeOp
// callbacks. The VM side (natives_xce.go) turns each op into an InvokeVirtual on
// the registered TextComponent, and the automata never touches the VM, so it is
// exhaustively unit-testable on its own.

// textComponentHandlerState is the native state on the singleton
// com/xce/lcdui/TextComponentHandler. It owns the multi-tap automata and the
// reference of the guest TextComponent currently registered through
// setTextComponent, which is the object the composed characters are pushed into.
type textComponentHandlerState struct {
	automata  imeAutomata
	component uint32
}

// newTextComponentHandlerState builds a handler with a cleared automata; the
// Korean sub-state needs its absent-jamo sentinels initialised rather than the
// zero value, which would read as "leading consonant 0 already present".
func newTextComponentHandlerState() *textComponentHandlerState {
	state := &textComponentHandlerState{}
	// These are Korean titles and the field shows no mode indicator, so start in
	// Hangul; '*' cycles to the English and numeric modes.
	state.automata.mode = imeModeKorean
	state.automata.korean.reset()
	return state
}

// imeMode indexes the four MHIma input modes in their supported-modes order.
type imeMode int32

const (
	imeModeENLower imeMode = iota // "EN/S" — lower-case multi-tap
	imeModeENUpper                // "EN/L" — upper-case multi-tap
	imeModeNumeric                // "N123" — direct digits
	imeModeKorean                 // "KO"   — Hangul multi-tap
	imeModeCount
)

// imeOpKind is one callback the handler must make on the guest TextComponent.
type imeOpKind int

const (
	// imeInsert commits a new character at the caret (TextComponent.insert).
	imeInsert imeOpKind = iota
	// imeReplace rewrites the character the caret sits after, which is how a
	// multi-tap rotation updates the still-composing glyph in place
	// (TextComponent.replace).
	imeReplace
	// imeDelete removes the character before the caret (TextComponent.delete).
	imeDelete
)

type imeOp struct {
	kind imeOpKind
	char rune
}

// mhImaKey is one keypad row of the MHIma English keymap: the candidate glyphs
// a key cycles through in lower and upper mode. The rows come from the firmware
// table at 0x02CB6BD4 ("0"=space, "2"="abc"/"ABC", ... "9"="wxyz"/"WXYZ").
type mhImaKey struct {
	lower string
	upper string
}

// mhImaEnglishKeymap maps an ASCII keypad code ('0'..'9') to its candidates.
// The SK-VM delivers keypad presses to the guest as ASCII, so the automata is
// keyed on the ASCII code rather than the WIPI 0x130+n code the HAL uses
// internally. Verified against the MHIma keymap dump.
var mhImaEnglishKeymap = map[int32]mhImaKey{
	'1': {lower: ".,?!1", upper: ".,?!1"},
	'2': {lower: "abc2", upper: "ABC2"},
	'3': {lower: "def3", upper: "DEF3"},
	'4': {lower: "ghi4", upper: "GHI4"},
	'5': {lower: "jkl5", upper: "JKL5"},
	'6': {lower: "mno6", upper: "MNO6"},
	'7': {lower: "pqrs7", upper: "PQRS7"},
	'8': {lower: "tuv8", upper: "TUV8"},
	'9': {lower: "wxyz9", upper: "WXYZ9"},
	'0': {lower: " 0", upper: " 0"},
}

const (
	// imeModeKey cycles the input mode (KO -> EN/S -> EN/L -> N123 -> KO). Real
	// handsets dedicate a key to this; the '*' keypad key is used here.
	imeModeKey int32 = '*'
	// imeSpaceKey inserts a literal space regardless of mode.
	imeSpaceKey int32 = '#'
)

// imeAutomata is the pure multi-tap state machine behind TextComponentHandler.
// It is serialised as part of the handler's native state so a save-state keeps
// a half-composed field intact.
type imeAutomata struct {
	mode imeMode

	// composingKey is the ASCII key whose candidate list is currently cycling,
	// or 0 when nothing is mid-composition. composingIndex is the rotation
	// position within that key's candidate string.
	composingKey   int32
	composingIndex int

	// korean holds the Hangul (천지인) composition state; it is only touched in
	// imeModeKorean.
	korean koreanAutomata
}

// reset clears any in-progress composition. It mirrors MHIma initAutomata and
// backs TextComponentHandler.clear.
func (a *imeAutomata) reset() {
	a.composingKey = 0
	a.composingIndex = 0
	a.korean.reset()
}

// commit ends the current composition so the next glyph starts fresh, without
// altering already-committed text.
func (a *imeAutomata) commit() {
	a.composingKey = 0
	a.composingIndex = 0
	a.korean.reset()
}

// setMode switches the active mode and ends any composition, matching
// MH_IMAsetCurrentMode which calls initAutomata on every switch.
func (a *imeAutomata) setMode(mode imeMode) {
	if mode < 0 || mode >= imeModeCount {
		return
	}
	a.mode = mode
	a.reset()
}

// press feeds one keypad code to the automata and returns the callbacks the
// handler must apply to the guest TextComponent, plus whether the key was
// consumed by the input method.
func (a *imeAutomata) press(key int32) (ops []imeOp, handled bool) {
	switch key {
	case imeModeKey:
		a.commit()
		a.mode = (a.mode + 1) % imeModeCount
		return nil, true
	case imeSpaceKey:
		a.commit()
		return []imeOp{{kind: imeInsert, char: ' '}}, true
	}

	switch a.mode {
	case imeModeNumeric:
		return a.pressNumeric(key)
	case imeModeENLower:
		return a.pressEnglish(key, false)
	case imeModeENUpper:
		return a.pressEnglish(key, true)
	case imeModeKorean:
		return a.korean.press(key)
	}
	return nil, false
}

func (a *imeAutomata) pressNumeric(key int32) ([]imeOp, bool) {
	if key < '0' || key > '9' {
		return nil, false
	}
	a.commit()
	return []imeOp{{kind: imeInsert, char: rune(key)}}, true
}

// pressEnglish runs the multi-tap rotation: the first press of a key inserts its
// first candidate, a repeat of the same key replaces the composing glyph with
// the next candidate (wrapping), and a different key commits the previous glyph
// before starting the new one. This mirrors MHIma handleENG states 0 and 1.
func (a *imeAutomata) pressEnglish(key int32, upper bool) ([]imeOp, bool) {
	entry, ok := mhImaEnglishKeymap[key]
	if !ok {
		return nil, false
	}
	candidates := entry.lower
	if upper {
		candidates = entry.upper
	}
	runes := []rune(candidates)
	if len(runes) == 0 {
		return nil, false
	}
	if a.composingKey == key {
		a.composingIndex = (a.composingIndex + 1) % len(runes)
		return []imeOp{{kind: imeReplace, char: runes[a.composingIndex]}}, true
	}
	a.composingKey = key
	a.composingIndex = 0
	return []imeOp{{kind: imeInsert, char: runes[0]}}, true
}
