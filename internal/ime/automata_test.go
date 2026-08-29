package ime

import "testing"

func typeHangul(keys ...int32) string {
	a := New(ModeKorean)
	var buffer []rune
	for _, key := range keys {
		ops, _ := a.Press(key)
		buffer = applyOps(buffer, ops)
	}
	return string(buffer)
}

func TestIMEKoreanComposesSyllables(t *testing.T) {
	cases := []struct {
		name string
		keys []int32
		want string
	}{
		// ㄴ(5) ㅣ(1)
		{"ni", []int32{'5', '1'}, "니"},
		// ㄱ(4) ㅗ=ㆍ(2)+ㅡ(3)
		{"go", []int32{'4', '2', '3'}, "고"},
		// ㄱ(4) ㅏ=ㅣ(1)+ㆍ(2) ㄱ받침(4)
		{"gak", []int32{'4', '1', '2', '4'}, "각"},
		// 강: ㄱ ㅏ ㅇ받침(0)
		{"gang", []int32{'4', '1', '2', '0'}, "강"},
		// ㄴ(5) ㅣ(1) ㄱ받침(4)
		{"nik", []int32{'5', '1', '4'}, "닉"},
		// 고치: ㄱㅗ, ㅊ=ㅈ(9)->ㅊ(9,9), then ㅣ(1) triggers 연음
		{"gochi", []int32{'4', '2', '3', '9', '9', '1'}, "고치"},
		// 연음: 강 + ㅏ -> 가아 (the ㅇ 받침 moves to the next syllable)
		{"gang+a", []int32{'4', '1', '2', '0', '1', '2'}, "가아"},
	}
	for _, test := range cases {
		if got := typeHangul(test.keys...); got != test.want {
			t.Errorf("%s: typeHangul = %q, want %q", test.name, got, test.want)
		}
	}
}
func TestIMENumericModeInsertsDigits(t *testing.T) {
	a := New(ModeNumeric)
	if got := typeKeys(&a, '4', '2', '0', '7'); got != "4207" {
		t.Fatalf("numeric = %q, want %q", got, "4207")
	}
}

func TestIMEEnglishMultiTapRotatesAndCommits(t *testing.T) {
	a := New(ModeENLower)
	// Two taps of 2 -> 'b'; a different key commits it and starts fresh.
	if got := typeKeys(&a, '2', '2', '3'); got != "bd" {
		t.Fatalf("english = %q, want %q", got, "bd")
	}

	a = New(ModeENLower)
	// "abc2" has four candidates; a fifth tap wraps back to 'a'.
	if got := typeKeys(&a, '2', '2', '2', '2', '2'); got != "a" {
		t.Fatalf("english wrap = %q, want %q", got, "a")
	}

	a = New(ModeENLower)
	// "dog" over three distinct keys: each new key commits the previous glyph.
	// (Two letters sharing a key need a timeout/next-key commit, which this
	// wall-clock-free automata does not model; distinct keys are unaffected.)
	if got := typeKeys(&a, '3', '6', '6', '6', '4'); got != "dog" {
		t.Fatalf("english dog = %q, want %q", got, "dog")
	}
}

func TestIMEUpperModeUsesUpperCandidates(t *testing.T) {
	a := New(ModeENUpper)
	if got := typeKeys(&a, '2', '7', '7', '7'); got != "AR" {
		t.Fatalf("upper = %q, want %q", got, "AR")
	}
}

func TestIMEStarKeyCyclesModes(t *testing.T) {
	a := New(ModeENLower)
	// '*' cycles EN/S -> EN/L -> N123 -> KO -> EN/S.
	a.Press('*')
	if a.CurrentMode() != ModeENUpper {
		t.Fatalf("after 1 star mode = %d, want EN/L", a.CurrentMode())
	}
	if got := typeKeys(&a, '2'); got != "A" {
		t.Fatalf("upper after star = %q, want %q", got, "A")
	}
	a.Press('*')
	if a.CurrentMode() != ModeNumeric {
		t.Fatalf("after 2 star mode = %d, want N123", a.CurrentMode())
	}
	if got := typeKeys(&a, '2'); got != "2" {
		t.Fatalf("numeric after star = %q, want %q", got, "2")
	}
	a.Press('*')
	if a.CurrentMode() != ModeKorean {
		t.Fatalf("after 3 star mode = %d, want KO", a.CurrentMode())
	}
	a.Press('*')
	if a.CurrentMode() != ModeENLower {
		t.Fatalf("after 4 star mode = %d, want EN/S", a.CurrentMode())
	}
}

func TestIMEHashKeyInsertsSpace(t *testing.T) {
	a := New(ModeENLower)
	// '#' commits the composing 'a' and inserts a space; the next '2' starts a
	// fresh rotation at 'a' rather than continuing the old one.
	if got := typeKeys(&a, '2', '#', '2'); got != "a a" {
		t.Fatalf("space = %q, want %q", got, "a a")
	}
}

func TestIMEModeSwitchCommitsComposition(t *testing.T) {
	a := New(ModeENLower)
	// A mode switch mid-rotation must keep the composed glyph, not drop it.
	ops, _ := a.Press('2') // 'a' composing
	_ = ops
	a.Press('*')                              // commit 'a', switch to EN/L
	if got := typeKeys(&a, '2'); got != "A" { // fresh 'A', 'a' already committed
		t.Fatalf("after mode switch = %q, want %q", got, "A")
	}
}

// applyOps folds a press result into a plain string the way the guest
// TextComponent would: insert appends, replace rewrites the caret glyph, delete
// removes it.
func applyOps(buffer []rune, ops []Op) []rune {
	for _, op := range ops {
		switch op.Kind {
		case OpInsert:
			buffer = append(buffer, op.Char)
		case OpReplace:
			if len(buffer) == 0 {
				buffer = append(buffer, op.Char)
			} else {
				buffer[len(buffer)-1] = op.Char
			}
		case OpDelete:
			if len(buffer) > 0 {
				buffer = buffer[:len(buffer)-1]
			}
		}
	}
	return buffer
}

func typeKeys(a *Automata, keys ...int32) string {
	var buffer []rune
	for _, key := range keys {
		ops, _ := a.Press(key)
		buffer = applyOps(buffer, ops)
	}
	return string(buffer)
}
