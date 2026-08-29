package ime

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
// The automata is kept as a pure state machine that emits a list of Op
// callbacks. The VM side (natives_xce.go) turns each op into an InvokeVirtual on
// the registered TextComponent, and the automata never touches the VM, so it is
// exhaustively unit-testable on its own.

// Mode indexes the four MHIma input modes in their supported-modes order.
type Mode int32

const (
	ModeENLower Mode = iota // "EN/S" — lower-case multi-tap
	ModeENUpper             // "EN/L" — upper-case multi-tap
	ModeNumeric             // "N123" — direct digits
	ModeKorean              // "KO"   — Hangul multi-tap
	ModeCount
)

// OpKind is one callback the handler must make on the guest TextComponent.
type OpKind int

const (
	// OpInsert commits a new character at the caret (TextComponent.insert).
	OpInsert OpKind = iota
	// OpReplace rewrites the character the caret sits after, which is how a
	// multi-tap rotation updates the still-composing glyph in place
	// (TextComponent.replace).
	OpReplace
	// OpDelete removes the character before the caret (TextComponent.delete).
	OpDelete
)

type Op struct {
	Kind OpKind
	Char rune
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
	// ModeKey cycles the input mode (KO -> EN/S -> EN/L -> N123 -> KO). Real
	// handsets dedicate a key to this; the '*' keypad key is used here.
	ModeKey int32 = '*'
	// SpaceKey inserts a literal space regardless of mode.
	SpaceKey int32 = '#'
)

// Automata is the pure multi-tap state machine behind TextComponentHandler.
// It is serialised as part of the handler's native state so a save-state keeps
// a half-composed field intact.
type Automata struct {
	mode Mode

	// composingKey is the ASCII key whose candidate list is currently cycling,
	// or 0 when nothing is mid-composition. composingIndex is the rotation
	// position within that key's candidate string.
	composingKey   int32
	composingIndex int

	// korean holds the Hangul (천지인) composition state; it is only touched in
	// ModeKorean.
	korean koreanAutomata
}

// New builds an automata in the given mode with its Hangul sub-state cleared:
// the zero value would read as "leading consonant 0 already present".
func New(mode Mode) Automata {
	automata := Automata{}
	automata.SetMode(mode)
	return automata
}

// CurrentMode reports the active input mode.
func (a *Automata) CurrentMode() Mode { return a.mode }

// State is the serialisable form of an automata, so a save state keeps a
// half-composed field intact.
type State struct {
	Mode           int32
	ComposingKey   int32
	ComposingIndex int32
	Choseong       int32
	Jungseong      int32
	Jongseong      int32
	Composing      bool
	LastKey        int32
	LastIndex      int32
}

// Snapshot captures the automata.
func (a *Automata) Snapshot() State {
	return State{
		Mode:           int32(a.mode),
		ComposingKey:   a.composingKey,
		ComposingIndex: int32(a.composingIndex),
		Choseong:       int32(a.korean.choseong),
		Jungseong:      int32(a.korean.jungseong),
		Jongseong:      int32(a.korean.jongseong),
		Composing:      a.korean.composing,
		LastKey:        a.korean.lastKey,
		LastIndex:      int32(a.korean.lastIndex),
	}
}

// Restore puts back a snapshot. An out-of-range mode becomes Korean, the mode a
// handset with no indicator starts in, so a corrupt state cannot leave the
// automata in a mode Press does not handle.
func (a *Automata) Restore(state State) {
	a.mode = Mode(state.Mode)
	if a.mode < 0 || a.mode >= ModeCount {
		a.mode = ModeKorean
	}
	a.composingKey = state.ComposingKey
	a.composingIndex = int(state.ComposingIndex)
	a.korean.choseong = int(state.Choseong)
	a.korean.jungseong = int(state.Jungseong)
	a.korean.jongseong = int(state.Jongseong)
	a.korean.composing = state.Composing
	a.korean.lastKey = state.LastKey
	a.korean.lastIndex = int(state.LastIndex)
}

// Reset clears any in-progress composition. It mirrors MHIma initAutomata and
// backs TextComponentHandler.clear.
func (a *Automata) Reset() {
	a.composingKey = 0
	a.composingIndex = 0
	a.korean.reset()
}

// Commit ends the current composition so the next glyph starts fresh, without
// altering already-committed text.
func (a *Automata) Commit() {
	a.composingKey = 0
	a.composingIndex = 0
	a.korean.reset()
}

// SetMode switches the active mode and ends any composition, matching
// MH_IMAsetCurrentMode which calls initAutomata on every switch.
func (a *Automata) SetMode(mode Mode) {
	if mode < 0 || mode >= ModeCount {
		return
	}
	a.mode = mode
	a.Reset()
}

// Press feeds one keypad code to the automata and returns the edits the caller
// must apply to its field, plus whether the key was consumed by the input
// method.
func (a *Automata) Press(key int32) (ops []Op, handled bool) {
	switch key {
	case ModeKey:
		a.Commit()
		a.mode = (a.mode + 1) % ModeCount
		return nil, true
	case SpaceKey:
		a.Commit()
		return []Op{{Kind: OpInsert, Char: ' '}}, true
	}

	switch a.mode {
	case ModeNumeric:
		return a.pressNumeric(key)
	case ModeENLower:
		return a.pressEnglish(key, false)
	case ModeENUpper:
		return a.pressEnglish(key, true)
	case ModeKorean:
		return a.korean.press(key)
	}
	return nil, false
}

func (a *Automata) pressNumeric(key int32) ([]Op, bool) {
	if key < '0' || key > '9' {
		return nil, false
	}
	a.Commit()
	return []Op{{Kind: OpInsert, Char: rune(key)}}, true
}

// pressEnglish runs the multi-tap rotation: the first press of a key inserts its
// first candidate, a repeat of the same key replaces the composing glyph with
// the next candidate (wrapping), and a different key commits the previous glyph
// before starting the new one. This mirrors MHIma handleENG states 0 and 1.
func (a *Automata) pressEnglish(key int32, upper bool) ([]Op, bool) {
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
		return []Op{{Kind: OpReplace, Char: runes[a.composingIndex]}}, true
	}
	a.composingKey = key
	a.composingIndex = 0
	return []Op{{Kind: OpInsert, Char: runes[0]}}, true
}
