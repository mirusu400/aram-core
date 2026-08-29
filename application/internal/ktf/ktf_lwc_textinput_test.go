package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
	"github.com/mirusu400/aram-core/profile"
)

// TestKTFLWCTextFieldComposesKeypadInput is the 프린세스메이커4 report. The
// title will not start until two names are entered, and it forwards every
// keypad press to TextFieldComponent.keyNotify - the platform, not the title,
// owns the composition. keyNotify did nothing and answered "not consumed", so
// both fields stayed empty.
func TestKTFLWCTextFieldComposesKeypadInput(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := runtime.newJavaInstance(
		"org/kwis/msp/lwc/TextFieldComponent",
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := runtime.newJavaInstance(
		"org/kwis/msp/lwc/TextFieldComponent",
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
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
