package ime

// Hangul (천지인 / Cheonjiin) composition for the XCE input-method automata.
//
// The SPH-W8300 firmware reverse showed the Korean "KO" mode is a thin wrapper
// (ADH_M2AAutoMata) around the handset's BREW/AEE system text control, so there
// is no discrete jamo table to lift byte-for-byte. What the reverse did settle
// is the engine: this is a 천지인 (Cheonjiin) handset. This file therefore
// implements the documented 천지인 layout and syllable automaton.
//
// Layout (keypad -> jamo):
//
//	1: ㅣ   2: ㆍ(dot)   3: ㅡ            (the three vowel strokes)
//	4: ㄱㅋㄲ  5: ㄴㄹ  6: ㄷㅌㄸ  7: ㅂㅍㅃ
//	8: ㅅㅎㅆ  9: ㅈㅊㅉ  0: ㅇㅁ         (consonants, multi-tap)
//
// Vowels are built by combining the three strokes (ㅣ+ㆍ=ㅏ, ㆍ+ㅣ=ㅓ, ...).
// A finished syllable that gains a trailing consonant (받침) and then a vowel
// hands that consonant to the next syllable (연음), e.g. 강 + ㅏ -> 가아.

const (
	hangulSyllableBase = 0xAC00
	hangulJungCompat   = 0x314F // ㅏ; jungseong index n renders as base+n.

	vowelNone rune = -1 // no medial yet
	vowelDot1 rune = -2 // one pending ㆍ, not yet a vowel
	vowelDot2 rune = -3 // two pending ㆍ

	strokeI = 1 // ㅣ (key 1)
	strokeD = 2 // ㆍ (key 2)
	strokeU = 3 // ㅡ (key 3)
)

// choseongCompat maps a leading-consonant index to its compatibility jamo, shown
// while a syllable has a consonant but no vowel yet.
var choseongCompat = []rune{
	0x3131, 0x3132, 0x3134, 0x3137, 0x3138, 0x3139, 0x3141, 0x3142, 0x3143,
	0x3145, 0x3146, 0x3147, 0x3148, 0x3149, 0x314A, 0x314B, 0x314C, 0x314D,
	0x314E,
}

// consonantChoseong/consonantJongseong give the multi-tap cycles for each keypad
// consonant. Choseong values index the 19 leading consonants; jongseong values
// are packed-syllable trailing indices (0 = none). Trailing cycles omit forms
// that cannot be a 받침 (ㄸㅃㅉ).
var consonantChoseong = map[int32][]int{
	'4': {0, 15, 1},   // ㄱ ㅋ ㄲ
	'5': {2, 5},       // ㄴ ㄹ
	'6': {3, 16, 4},   // ㄷ ㅌ ㄸ
	'7': {7, 17, 8},   // ㅂ ㅍ ㅃ
	'8': {9, 18, 10},  // ㅅ ㅎ ㅆ
	'9': {12, 14, 13}, // ㅈ ㅊ ㅉ
	'0': {11, 6},      // ㅇ ㅁ
}

var consonantJongseong = map[int32][]int{
	'4': {1, 24, 2},   // ㄱ ㅋ ㄲ
	'5': {4, 8},       // ㄴ ㄹ
	'6': {7, 25},      // ㄷ ㅌ
	'7': {17, 26},     // ㅂ ㅍ
	'8': {19, 27, 20}, // ㅅ ㅎ ㅆ
	'9': {22, 23},     // ㅈ ㅊ
	'0': {21, 16},     // ㅇ ㅁ
}

// jongseongToChoseong maps a trailing-consonant packed index to the leading
// consonant it becomes when 연음 moves it onto the next syllable.
var jongseongToChoseong = map[int]int{
	1: 0, 2: 1, 4: 2, 7: 3, 8: 5, 16: 6, 17: 7,
	19: 9, 20: 10, 21: 11, 22: 12, 23: 14, 24: 15, 25: 16, 26: 17, 27: 18,
}

// vowelStep advances the 천지인 vowel automaton by one stroke. It reports the new
// medial state (a real jungseong index or a pending-dot sentinel) and whether the
// stroke combined at all.
func vowelStep(state rune, stroke int) (rune, bool) {
	type key struct {
		state  rune
		stroke int
	}
	transitions := map[key]rune{
		{vowelNone, strokeI}: 20, // ㅣ
		{vowelNone, strokeD}: vowelDot1,
		{vowelNone, strokeU}: 18, // ㅡ

		{20, strokeD}: 0, // ㅣ+ㆍ -> ㅏ
		{0, strokeD}:  2, // ㅏ+ㆍ -> ㅑ
		{0, strokeI}:  1, // ㅏ+ㅣ -> ㅐ
		{2, strokeI}:  3, // ㅑ+ㅣ -> ㅒ

		{vowelDot1, strokeD}: vowelDot2,
		{vowelDot1, strokeI}: 4,  // ㆍㅣ -> ㅓ
		{vowelDot1, strokeU}: 8,  // ㆍㅡ -> ㅗ
		{vowelDot2, strokeI}: 6,  // ㆍㆍㅣ -> ㅕ
		{vowelDot2, strokeU}: 12, // ㆍㆍㅡ -> ㅛ

		{4, strokeI}: 5, // ㅓ+ㅣ -> ㅔ
		{4, strokeD}: 6, // ㅓ+ㆍ -> ㅕ
		{6, strokeI}: 7, // ㅕ+ㅣ -> ㅖ

		{8, strokeI}:  11, // ㅗ+ㅣ -> ㅚ
		{18, strokeD}: 13, // ㅡ+ㆍ -> ㅜ
		{18, strokeI}: 19, // ㅡ+ㅣ -> ㅢ
		{13, strokeD}: 17, // ㅜ+ㆍ -> ㅠ
		{13, strokeI}: 16, // ㅜ+ㅣ -> ㅟ
	}
	next, ok := transitions[key{state, stroke}]
	return next, ok
}

func isRealVowel(state rune) bool {
	return state >= 0 && state <= 20
}

// koreanAutomata holds the syllable under construction. choseong indexes the 19
// leading consonants (-1 = none); jungseong is the vowel-automaton state (a real
// medial index, or a pending-dot sentinel); jongseong is the packed trailing
// index (0 = none). lastKey/lastIndex track the active multi-tap rotation. The
// fields are serialised with the handler so a half-typed syllable survives a
// save-state.
type koreanAutomata struct {
	choseong  int
	jungseong int
	jongseong int
	composing bool
	lastKey   int32
	lastIndex int
}

func (k *koreanAutomata) reset() {
	k.choseong = -1
	k.jungseong = int(vowelNone)
	k.jongseong = 0
	k.composing = false
	k.lastKey = 0
	k.lastIndex = 0
}

// render returns the glyph for the syllable under construction, or 0 when there
// is nothing displayable yet (a lone pending dot).
func (k *koreanAutomata) render() rune {
	jung := rune(k.jungseong)
	switch {
	case k.choseong >= 0 && isRealVowel(jung):
		return hangulSyllableBase +
			rune((k.choseong*21+int(jung))*28+k.jongseong)
	case k.choseong >= 0:
		return choseongCompat[k.choseong]
	case isRealVowel(jung):
		return hangulJungCompat + jung
	default:
		return 0
	}
}

// emit renders the current syllable and returns the callback that shows it:
// replace while a syllable is already on screen, insert when starting a new one.
func (k *koreanAutomata) emit() []Op {
	glyph := k.render()
	if glyph == 0 {
		return nil
	}
	if k.composing {
		return []Op{{Kind: OpReplace, Char: glyph}}
	}
	k.composing = true
	return []Op{{Kind: OpInsert, Char: glyph}}
}

// startSyllable commits whatever is on screen and begins a fresh syllable, so
// the next emit inserts rather than replaces.
func (k *koreanAutomata) startSyllable() {
	k.choseong = -1
	k.jungseong = int(vowelNone)
	k.jongseong = 0
	k.composing = false
	k.lastKey = 0
	k.lastIndex = 0
}

func (k *koreanAutomata) press(key int32) ([]Op, bool) {
	if choCycle, ok := consonantChoseong[key]; ok {
		return k.pressConsonant(key, choCycle, consonantJongseong[key]), true
	}
	switch key {
	case '1':
		return k.pressVowel(strokeI), true
	case '2':
		return k.pressVowel(strokeD), true
	case '3':
		return k.pressVowel(strokeU), true
	}
	return nil, false
}

func (k *koreanAutomata) pressConsonant(key int32, choCycle, jongCycle []int) []Op {
	jung := rune(k.jungseong)
	switch {
	case k.choseong < 0 && jung == vowelNone:
		// Fresh syllable: this consonant is the 초성.
		k.choseong = choCycle[0]
		k.lastKey = key
		k.lastIndex = 0
	case k.choseong >= 0 && jung == vowelNone:
		// A leading consonant with no vowel yet: repeat cycles it, a new key
		// commits the lone consonant and starts another.
		if k.lastKey == key {
			k.lastIndex = (k.lastIndex + 1) % len(choCycle)
			k.choseong = choCycle[k.lastIndex]
		} else {
			k.startSyllable()
			k.choseong = choCycle[0]
			k.lastKey = key
		}
	case k.choseong >= 0 && isRealVowel(jung) && k.jongseong == 0:
		// A finished 초성+중성 gains a 받침.
		k.jongseong = jongCycle[0]
		k.lastKey = key
		k.lastIndex = 0
	case k.choseong >= 0 && isRealVowel(jung) && k.jongseong != 0:
		// A 받침 is present: repeat cycles it, a new key starts a syllable.
		if k.lastKey == key {
			k.lastIndex = (k.lastIndex + 1) % len(jongCycle)
			k.jongseong = jongCycle[k.lastIndex]
		} else {
			k.startSyllable()
			k.choseong = choCycle[0]
			k.lastKey = key
		}
	default:
		k.startSyllable()
		k.choseong = choCycle[0]
		k.lastKey = key
	}
	return k.emit()
}

func (k *koreanAutomata) pressVowel(stroke int) []Op {
	jung := rune(k.jungseong)

	// 연음: a syllable with a 받침 hands it to a new syllable led by that
	// consonant, e.g. 강 + ㅏ -> 가아.
	if k.choseong >= 0 && isRealVowel(jung) && k.jongseong != 0 {
		moved, ok := jongseongToChoseong[k.jongseong]
		if ok {
			k.jongseong = 0
			fix := Op{Kind: OpReplace, Char: k.render()}
			next, _ := vowelStep(vowelNone, stroke)
			k.startSyllable()
			k.choseong = moved
			k.jungseong = int(next)
			add := k.emit()
			return append([]Op{fix}, add...)
		}
	}

	// Extend the current medial, or if the stroke cannot combine, begin a new
	// syllable carrying just the vowel.
	if next, ok := vowelStep(jung, stroke); ok {
		k.jungseong = int(next)
		return k.emit()
	}
	k.startSyllable()
	next, _ := vowelStep(vowelNone, stroke)
	k.jungseong = int(next)
	return k.emit()
}
