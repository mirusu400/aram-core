package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/internal/ime"
	"github.com/mirusu400/aram-core/profile"
)

// TestKTFLWCTextFieldComposesKeypadInput is the 프린세스메이커4 report. The
// title will not start until two names are entered, and it forwards every
// keypad press to TextFieldComponent.keyNotify - the platform, not the title,
// owns the composition. keyNotify did nothing and answered "not consumed", so
// both fields stayed empty.
func TestKTFLWCTextFieldComposesKeypadInput(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	instance, err := runtime.newJavaInstance(
		"org/kwis/msp/lwc/TextFieldComponent",
		0,
	)
	check(t, err)
	state := runtime.lwcComponent(instance)

	press := func(key int32) bool {
		t.Helper()
		handled, editErr := runtime.editLWCText(
			instance,
			state,
			ktfLWCKeyPressed,
			key,
		)
		if editErr != nil {
			t.Fatal(editErr)
		}
		return handled
	}
	field := func() string {
		t.Helper()
		return runtime.javaStringValue(state.text)
	}

	// 천지인: ㄴ(5) ㅣ(1) then ㄱ(4) ㅏ=ㅣ(1)+ㆍ(2).
	for _, key := range []int32{'5', '1', '4', '1', '2'} {
		if !press(key) {
			t.Fatalf("key %q was not consumed by the field", key)
		}
	}
	if got := field(); got != "니가" {
		t.Fatalf("field = %q, want %q", got, "니가")
	}

	// The clear key removes the last character; on an empty field it belongs to
	// the title, which uses it to leave the screen.
	if !press(int32(profile.KeyClear)) {
		t.Fatal("clear key was not consumed by a non-empty field")
	}
	if got := field(); got != "니" {
		t.Fatalf("field after clear = %q, want %q", got, "니")
	}
	if press(int32(profile.KeyClear)); field() != "" {
		t.Fatalf("field after second clear = %q, want empty", field())
	}
	if press(int32(profile.KeyClear)) {
		t.Fatal("clear key on an empty field must pass back to the title")
	}

	// A soft key is not the input method's, so the title still sees it.
	if press(-6) {
		t.Fatal("soft key was consumed by the field")
	}
}

// TestKTFLWCTextFieldHonoursMaxLength pins the limit the title sets before it
// shows the field: 프린세스메이커4 allows four characters per name. A full
// field still rotates the glyph being multi-tapped, because that does not grow
// it.
func TestKTFLWCTextFieldHonoursMaxLength(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	instance, err := runtime.newJavaInstance(
		"org/kwis/msp/lwc/TextFieldComponent",
		0,
	)
	check(t, err)
	state := runtime.lwcComponent(instance)
	runtime.lwcMaxLengths[instance] = 2

	// '*' three times reaches the numeric mode, where each digit is literal.
	for _, key := range []int32{'*', '*', '*', '1', '2', '3'} {
		if _, err := runtime.editLWCText(
			instance,
			state,
			ktfLWCKeyPressed,
			key,
		); err != nil {
			t.Fatal(err)
		}
	}
	if got := runtime.javaStringValue(state.text); got != "12" {
		t.Fatalf("field = %q, want %q (the third digit overflows)", got, "12")
	}
}

func TestKTFLWCTextFieldOverflowKeepsComposition(t *testing.T) {
	for _, test := range []struct {
		name string
		mode ime.Mode
		keys []int32
		want []string
	}{
		{
			name: "English multi-tap",
			mode: ime.ModeENLower,
			keys: []int32{'2', '3', '3', '2'},
			want: []string{"a", "a", "a", "b"},
		},
		{
			name: "Hangul leading consonant",
			mode: ime.ModeKorean,
			keys: []int32{'4', '5', '5', '1'},
			want: []string{"ㄱ", "ㄱ", "ㄱ", "기"},
		},
		{
			name: "Hangul syllable split",
			mode: ime.ModeKorean,
			keys: []int32{'4', '1', '2', '5', '1', '5'},
			want: []string{"ㄱ", "기", "가", "간", "간", "갈"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := newTestRuntime(t)
			runtime.JvmContext = allocWords(t, runtime, 3+128)
			instance, err := runtime.newJavaInstance("org/kwis/msp/lwc/TextFieldComponent", 0)
			check(t, err)
			state := runtime.lwcComponent(instance)
			runtime.lwcMaxLengths[instance] = 1
			runtime.lwcTextInputAutomata(instance).SetMode(test.mode)

			for i, key := range test.keys {
				handled, err := runtime.editLWCText(instance, state, ktfLWCKeyPressed, key)
				check(t, err)
				if !handled {
					t.Fatalf("press %d (%q) was not consumed", i, key)
				}
				if got := ktfLWCFieldText(runtime, state); got != test.want[i] {
					t.Fatalf("press %d (%q): field = %q, want %q", i, key, got, test.want[i])
				}
			}
		})
	}
}
