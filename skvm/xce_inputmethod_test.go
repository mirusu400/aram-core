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
	check(t, err)
	vm, err := New(map[string][]byte{"IMEProbe": data})
	check(t, err)
	handlerValue := invokeTestNative(
		t, vm,
		"com/xce/lcdui/TextComponentHandler", "getTextComponentHandler",
		"()Lcom/xce/lcdui/TextComponentHandler;", 0,
	)
	handler, err := handlerValue.Reference()
	check(t, err)
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
	check(t, err)
	runes := make([]rune, 0, length)
	for index := int32(0); index < length; index++ {
		charValue, _, err := vm.InvokeStatic(
			context.Background(), "IMEProbe", "at", "(I)I", IntValue(index),
		)
		if err != nil {
			t.Fatal(err)
		}
		char, err := charValue.Int()
		check(t, err)
		runes = append(runes, rune(char))
	}
	return string(runes)
}

func TestTextComponentHandlerLoadAndCurrentComponent(t *testing.T) {
	vm, err := New(nil)
	check(t, err)
	loaded := invokeTestNative(t, vm,
		"com/xce/lcdui/TextComponentHandler", "isLoaded", "()Z", 0)
	loadedValue, err := loaded.Int()
	check(t, err)
	if loadedValue != 1 {
		t.Fatalf("input handler loaded = %d, want 1", loadedValue)
	}
	handlerValue := invokeTestNative(t, vm,
		"com/xce/lcdui/TextComponentHandler", "getTextComponentHandler",
		"()Lcom/xce/lcdui/TextComponentHandler;", 0)
	handler, err := handlerValue.Reference()
	check(t, err)
	getComponent := func() uint32 {
		t.Helper()
		value := invokeTestNative(t, vm,
			"com/xce/lcdui/TextComponentHandler", "getTextComponent",
			"()Lcom/xce/lcdui/TextComponent;", handler)
		reference, err := value.Reference()
		check(t, err)
		return reference
	}
	if got := getComponent(); got != 0 {
		t.Fatalf("initial text component = %d, want null", got)
	}
	component := vm.NewObject("com/xce/lcdui/TextComponent", nil)
	invokeTestNative(t, vm,
		"com/xce/lcdui/TextComponentHandler", "setTextComponent",
		"(Lcom/xce/lcdui/TextComponent;)V", handler, ReferenceValue(component))
	if got := getComponent(); got != component {
		t.Fatalf("current text component = %d, want %d", got, component)
	}
	invokeTestNative(t, vm,
		"com/xce/lcdui/TextComponentHandler", "setTextComponent",
		"(Lcom/xce/lcdui/TextComponent;)V", handler, ReferenceValue(0))
	if got := getComponent(); got != 0 {
		t.Fatalf("cleared text component = %d, want null", got)
	}
}

func TestTextComponentHandlerDrivesGuestComponent(t *testing.T) {
	// The field starts in KO, so ㄴ(5)+ㅣ(1) composes 니 into the guest field.
	if got := driveHandler(t, '5', '1'); got != "니" {
		t.Fatalf("korean compose = %q, want %q", got, "니")
	}
	// One '*' reaches EN/S: '2','2' rotates a->b, then '3' commits and inserts 'd'.
	if got := driveHandler(t, '*', '2', '2', '3'); got != "bd" {
		t.Fatalf("english compose = %q, want %q", got, "bd")
	}
	// KO -> EN/S -> EN/L -> N123 needs three '*'; digits insert literally, '#' spaces.
	if got := driveHandler(t, '*', '*', '*', '5', '#', '9'); got != "5 9" {
		t.Fatalf("numeric compose = %q, want %q", got, "5 9")
	}
}

func TestXTextFieldFocusInputAndModeImages(t *testing.T) {
	vm, err := New(nil)
	check(t, err)
	field := vm.NewObject("com/xce/lcdui/XTextField", nil)
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "<init>",
		"(Ljava/lang/String;IILjavax/microedition/lcdui/Canvas;)V", field,
		ReferenceValue(vm.NewString("")), IntValue(6), IntValue(0), ReferenceValue(0))
	handlerValue := invokeTestNative(t, vm,
		"com/xce/lcdui/TextComponentHandler", "getTextComponentHandler",
		"()Lcom/xce/lcdui/TextComponentHandler;", 0)
	handler, err := handlerValue.Reference()
	check(t, err)
	component := func() uint32 {
		t.Helper()
		value := invokeTestNative(t, vm,
			"com/xce/lcdui/TextComponentHandler", "getTextComponent",
			"()Lcom/xce/lcdui/TextComponent;", handler)
		result, err := value.Reference()
		check(t, err)
		return result
	}
	mode := func() int32 {
		t.Helper()
		value := invokeTestNative(t, vm,
			"com/xce/lcdui/TextComponentHandler", "getInputMode", "()I", handler)
		result, err := value.Int()
		check(t, err)
		return result
	}
	text := func() string {
		t.Helper()
		value := invokeTestNative(t, vm,
			"com/xce/lcdui/XTextField", "getText", "()Ljava/lang/String;", field)
		result, err := vm.stringArgument([]Value{value}, 0)
		check(t, err)
		return result
	}
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "setFocus", "(Z)V", field, IntValue(1))
	if got := component(); got != field {
		t.Fatalf("focused component = %d, want %d", got, field)
	}
	if got := mode(); got != 16 {
		t.Fatalf("initial XCE mode = %d, want Korean mode 16", got)
	}
	for _, key := range []int32{'*', '2', '2'} {
		invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "keyPressed", "(I)V", field, IntValue(key))
	}
	if got := mode(); got != 2 {
		t.Fatalf("XCE mode after star = %d, want lowercase mode 2", got)
	}
	if got := text(); got != "b" {
		t.Fatalf("composed text = %q, want b", got)
	}
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "keyPressed", "(I)V", field, IntValue(8))
	if got := text(); got != "" {
		t.Fatalf("text after backspace = %q, want empty", got)
	}
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "setText", "(Ljava/lang/String;)V",
		field, ReferenceValue(vm.NewString("123456789")))
	if got := text(); got != "123456" {
		t.Fatalf("max-length text = %q, want 123456", got)
	}
	for _, key := range []int32{'2', '2'} {
		invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "keyPressed", "(I)V", field, IntValue(key))
	}
	if got := text(); got != "123456" {
		t.Fatalf("full field changed during multi-tap: %q", got)
	}
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "keyPressed", "(I)V", field, IntValue(8))
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "keyPressed", "(I)V", field, IntValue('2'))
	if got := text(); got != "12345a" {
		t.Fatalf("text after making room = %q, want 12345a", got)
	}
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "setText", "(Ljava/lang/String;)V",
		field, ReferenceValue(vm.NewString("12345")))
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "inputChar", "(C)V", field, IntValue('z'))
	if got := text(); got != "12345z" {
		t.Fatalf("direct character input = %q, want 12345z", got)
	}
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "setText", "(Ljava/lang/String;)V",
		field, ReferenceValue(vm.NewString("123456")))
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "setBounds", "(IIII)V",
		field, IntValue(20), IntValue(30), IntValue(80), IntValue(16))
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "paint",
		"(Ljavax/microedition/lcdui/Graphics;)V", field,
		ReferenceValue(vm.ScreenGraphics()))
	interior, err := vm.services.Graphics.Pixel(vm.serviceOwner, vm.screenSurface, 21, 31)
	check(t, err)
	if interior.R != 0xff || interior.G != 0xff || interior.B != 0xff {
		t.Fatalf("field interior = %#v, want white", interior)
	}
	for _, name := range []string{"ueimImg", "leimImg", "simImg", "kimImg", "nimImg", "imHintImg"} {
		first := invokeTestNative(t, vm, "com/xce/lcdui/Toolkit", name,
			"()Ljavax/microedition/lcdui/Image;", 0)
		firstRef, err := first.Reference()
		check(t, err)
		if firstRef == 0 {
			t.Fatalf("%s returned null", name)
		}
		image, err := vm.image(firstRef)
		check(t, err)
		if image.width != 15 || image.height != 15 {
			t.Fatalf("%s dimensions = %dx%d, want 15x15", name, image.width, image.height)
		}
		second := invokeTestNative(t, vm, "com/xce/lcdui/Toolkit", name,
			"()Ljavax/microedition/lcdui/Image;", 0)
		secondRef, err := second.Reference()
		check(t, err)
		if secondRef != firstRef {
			t.Fatalf("%s image was not reused", name)
		}
	}
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	if got := component(); got != field {
		t.Fatalf("restored focused component = %d, want %d", got, field)
	}
	if got := text(); got != "123456" {
		t.Fatalf("restored text = %q, want 123456", got)
	}
	invokeTestNative(t, vm, "com/xce/lcdui/XTextField", "setFocus", "(Z)V", field, IntValue(0))
	if got := component(); got != 0 {
		t.Fatalf("unfocused component = %d, want null", got)
	}
}
