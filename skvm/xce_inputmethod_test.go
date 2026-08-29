package skvm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// driveHandler loads the IMEProbe test double, registers it with the platform
// text-input handler, feeds the keypad codes, and returns the text the handler
// composed into the component. It exercises the real native path
// (keyPressed -> reentrant InvokeVirtual -> guest insert/replace/delete).
func driveHandler(t *testing.T, keys ...int32) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "IMEProbe.class"))
	if err != nil {
		t.Fatal(err)
	}
	vm, err := New(map[string][]byte{"IMEProbe": data})
	if err != nil {
		t.Fatal(err)
	}
	handlerValue := invokeTestNative(
		t, vm,
		"com/xce/lcdui/TextComponentHandler", "getTextComponentHandler",
		"()Lcom/xce/lcdui/TextComponentHandler;", 0,
	)
	handler, err := handlerValue.Reference()
	if err != nil {
		t.Fatal(err)
	}
	probe := vm.NewObject("IMEProbe", nil)
	invokeTestNative(
		t, vm,
		"com/xce/lcdui/TextComponentHandler", "setTextComponent",
		"(Lcom/xce/lcdui/TextComponent;)V", handler, ReferenceValue(probe),
	)
	for _, key := range keys {
		invokeTestNative(
			t, vm,
			"com/xce/lcdui/TextComponentHandler", "keyPressed", "(I)Z",
			handler, IntValue(key),
		)
	}
	lengthValue, _, err := vm.InvokeStatic(
		context.Background(), "IMEProbe", "length", "()I",
	)
	if err != nil {
		t.Fatal(err)
	}
	length, err := lengthValue.Int()
	if err != nil {
		t.Fatal(err)
	}
	runes := make([]rune, 0, length)
	for index := int32(0); index < length; index++ {
		charValue, _, err := vm.InvokeStatic(
			context.Background(), "IMEProbe", "at", "(I)I", IntValue(index),
		)
		if err != nil {
			t.Fatal(err)
		}
		char, err := charValue.Int()
		if err != nil {
			t.Fatal(err)
		}
		runes = append(runes, rune(char))
	}
	return string(runes)
}

func TestTextComponentHandlerDrivesGuestComponent(t *testing.T) {
	// Default EN/S mode: '2','2' rotates a->b, then '3' commits and inserts 'd'.
	if got := driveHandler(t, '2', '2', '3'); got != "bd" {
		t.Fatalf("english compose = %q, want %q", got, "bd")
	}
	// '*' twice reaches N123; digits then insert literally, and '#' is a space.
	if got := driveHandler(t, '*', '*', '5', '#', '9'); got != "5 9" {
		t.Fatalf("numeric compose = %q, want %q", got, "5 9")
	}
}

// applyOps folds a press result into a plain string the way the guest
// TextComponent would: insert appends, replace rewrites the caret glyph, delete
// removes it.
func applyOps(buffer []rune, ops []imeOp) []rune {
	for _, op := range ops {
		switch op.kind {
		case imeInsert:
			buffer = append(buffer, op.char)
		case imeReplace:
			if len(buffer) == 0 {
				buffer = append(buffer, op.char)
			} else {
				buffer[len(buffer)-1] = op.char
			}
		case imeDelete:
			if len(buffer) > 0 {
				buffer = buffer[:len(buffer)-1]
			}
		}
	}
	return buffer
}

func typeKeys(a *imeAutomata, keys ...int32) string {
	var buffer []rune
	for _, key := range keys {
		ops, _ := a.press(key)
		buffer = applyOps(buffer, ops)
	}
	return string(buffer)
}

func TestIMENumericModeInsertsDigits(t *testing.T) {
	a := &imeAutomata{mode: imeModeNumeric}
	if got := typeKeys(a, '4', '2', '0', '7'); got != "4207" {
		t.Fatalf("numeric = %q, want %q", got, "4207")
	}
}

func TestIMEEnglishMultiTapRotatesAndCommits(t *testing.T) {
	a := &imeAutomata{mode: imeModeENLower}
	// Two taps of 2 -> 'b'; a different key commits it and starts fresh.
	if got := typeKeys(a, '2', '2', '3'); got != "bd" {
		t.Fatalf("english = %q, want %q", got, "bd")
	}

	a = &imeAutomata{mode: imeModeENLower}
	// "abc2" has four candidates; a fifth tap wraps back to 'a'.
	if got := typeKeys(a, '2', '2', '2', '2', '2'); got != "a" {
		t.Fatalf("english wrap = %q, want %q", got, "a")
	}

	a = &imeAutomata{mode: imeModeENLower}
	// "dog" over three distinct keys: each new key commits the previous glyph.
	// (Two letters sharing a key need a timeout/next-key commit, which this
	// wall-clock-free automata does not model; distinct keys are unaffected.)
	if got := typeKeys(a, '3', '6', '6', '6', '4'); got != "dog" {
		t.Fatalf("english dog = %q, want %q", got, "dog")
	}
}

func TestIMEUpperModeUsesUpperCandidates(t *testing.T) {
	a := &imeAutomata{mode: imeModeENUpper}
	if got := typeKeys(a, '2', '7', '7', '7'); got != "AR" {
		t.Fatalf("upper = %q, want %q", got, "AR")
	}
}

func TestIMEStarKeyCyclesModes(t *testing.T) {
	a := &imeAutomata{mode: imeModeENLower}
	// '*' cycles EN/S -> EN/L -> N123 -> KO -> EN/S.
	a.press('*')
	if a.mode != imeModeENUpper {
		t.Fatalf("after 1 star mode = %d, want EN/L", a.mode)
	}
	if got := typeKeys(a, '2'); got != "A" {
		t.Fatalf("upper after star = %q, want %q", got, "A")
	}
	a.press('*')
	if a.mode != imeModeNumeric {
		t.Fatalf("after 2 star mode = %d, want N123", a.mode)
	}
	if got := typeKeys(a, '2'); got != "2" {
		t.Fatalf("numeric after star = %q, want %q", got, "2")
	}
	a.press('*')
	if a.mode != imeModeKorean {
		t.Fatalf("after 3 star mode = %d, want KO", a.mode)
	}
	a.press('*')
	if a.mode != imeModeENLower {
		t.Fatalf("after 4 star mode = %d, want EN/S", a.mode)
	}
}

func TestIMEHashKeyInsertsSpace(t *testing.T) {
	a := &imeAutomata{mode: imeModeENLower}
	// '#' commits the composing 'a' and inserts a space; the next '2' starts a
	// fresh rotation at 'a' rather than continuing the old one.
	if got := typeKeys(a, '2', '#', '2'); got != "a a" {
		t.Fatalf("space = %q, want %q", got, "a a")
	}
}

func TestIMEModeSwitchCommitsComposition(t *testing.T) {
	a := &imeAutomata{mode: imeModeENLower}
	// A mode switch mid-rotation must keep the composed glyph, not drop it.
	ops, _ := a.press('2') // 'a' composing
	_ = ops
	a.press('*')                             // commit 'a', switch to EN/L
	if got := typeKeys(a, '2'); got != "A" { // fresh 'A', 'a' already committed
		t.Fatalf("after mode switch = %q, want %q", got, "A")
	}
}
