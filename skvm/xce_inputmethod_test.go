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
