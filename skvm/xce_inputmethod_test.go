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
