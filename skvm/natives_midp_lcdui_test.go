package skvm

import (
	"context"
	"testing"
)

func TestMIDPTextFieldMaintainsUTF16Content(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	field := vm.NewObject("javax/microedition/lcdui/TextField", nil)
	invokeTestNative(t, vm, "javax/microedition/lcdui/TextField", "<init>", "(Ljava/lang/String;Ljava/lang/String;II)V", field,
		ReferenceValue(vm.NewString("name")), ReferenceValue(vm.NewString("A😀")), IntValue(8), IntValue(0))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/TextField", "size", "()I", field)); got != 3 {
		t.Fatalf("TextField.size() = %d, want 3 UTF-16 units", got)
	}
	invokeTestNative(t, vm, "javax/microedition/lcdui/TextField", "insert", "(Ljava/lang/String;I)V", field,
		ReferenceValue(vm.NewString("B")), IntValue(1))
	value := invokeTestNative(t, vm, "javax/microedition/lcdui/TextField", "getString", "()Ljava/lang/String;", field)
	reference, err := value.Reference()
	check(t, err)
	text, err := vm.String(reference)
	check(t, err)
	if text != "AB😀" {
		t.Fatalf("TextField content = %q", text)
	}
}

func TestMIDPChoiceAndFormMaintainItems(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	choice := vm.NewObject("javax/microedition/lcdui/ChoiceGroup", nil)
	invokeTestNative(t, vm, "javax/microedition/lcdui/ChoiceGroup", "<init>", "(Ljava/lang/String;I)V", choice,
		ReferenceValue(vm.NewString("difficulty")), IntValue(1))
	for _, label := range []string{"easy", "hard"} {
		invokeTestNative(t, vm, "javax/microedition/lcdui/ChoiceGroup", "append", "(Ljava/lang/String;Ljavax/microedition/lcdui/Image;)I", choice,
			ReferenceValue(vm.NewString(label)), ReferenceValue(0))
	}
	invokeTestNative(t, vm, "javax/microedition/lcdui/ChoiceGroup", "setSelectedIndex", "(IZ)V", choice, IntValue(1), IntValue(1))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/ChoiceGroup", "getSelectedIndex", "()I", choice)); got != 1 {
		t.Fatalf("selected index = %d", got)
	}
	form := vm.NewObject("javax/microedition/lcdui/Form", nil)
	invokeTestNative(t, vm, "javax/microedition/lcdui/Form", "<init>", "(Ljava/lang/String;)V", form, ReferenceValue(vm.NewString("settings")))
	invokeTestNative(t, vm, "javax/microedition/lcdui/Form", "append", "(Ljavax/microedition/lcdui/Item;)I", form, ReferenceValue(choice))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/Form", "size", "()I", form)); got != 1 {
		t.Fatalf("Form.size() = %d", got)
	}
}

func TestMIDPDisplayTracksCurrentDisplayable(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	display := vm.NewObject("javax/microedition/lcdui/Display", nil)
	form := vm.NewObject("javax/microedition/lcdui/Form", nil)
	invokeTestNative(t, vm, "javax/microedition/lcdui/Display", "setCurrent", "(Ljavax/microedition/lcdui/Displayable;)V", display, ReferenceValue(form))
	current := invokeTestNative(t, vm, "javax/microedition/lcdui/Display", "getCurrent", "()Ljavax/microedition/lcdui/Displayable;", display)
	reference, err := current.Reference()
	check(t, err)
	if reference != form {
		t.Fatalf("current display = %d, want %d", reference, form)
	}
	if shown := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/Displayable", "isShown", "()Z", form)); shown != 1 {
		t.Fatalf("isShown = %d", shown)
	}
}

func TestMIDPImageMutabilityAndFontAttributes(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	mutable := mustReference(t, invokeTestNative(t, vm, "javax/microedition/lcdui/Image", "createImage", "(II)Ljavax/microedition/lcdui/Image;", 0, IntValue(2), IntValue(2)))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/Image", "isMutable", "()Z", mutable)); got != 1 {
		t.Fatalf("blank image mutability = %d", got)
	}
	pixels := vm.newArray("[I", []Value{IntValue(-1)})
	immutable := mustReference(t, invokeTestNative(t, vm, "javax/microedition/lcdui/Image", "createRGBImage", "([IIIZ)Ljavax/microedition/lcdui/Image;", 0,
		ReferenceValue(pixels), IntValue(1), IntValue(1), IntValue(1)))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/Image", "isMutable", "()Z", immutable)); got != 0 {
		t.Fatalf("RGB image mutability = %d", got)
	}
	native := vm.natives[nativeKey{"javax/microedition/lcdui/Image", "getGraphics", "()Ljavax/microedition/lcdui/Graphics;"}]
	_, _, err = native(context.Background(), vm, immutable, nil)
	if thrown, ok := err.(*thrown); !ok || thrown.class != "java/lang/IllegalStateException" {
		t.Fatalf("immutable getGraphics error = %v", err)
	}
	font := mustReference(t, invokeTestNative(t, vm, "javax/microedition/lcdui/Font", "getFont", "(III)Ljavax/microedition/lcdui/Font;", 0,
		IntValue(32), IntValue(1|4), IntValue(16)))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/Font", "getFace", "()I", font)); got != 32 {
		t.Fatalf("font face = %d", got)
	}
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/Font", "isBold", "()Z", font)); got != 1 {
		t.Fatalf("font bold = %d", got)
	}
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/Font", "isItalic", "()Z", font)); got != 0 {
		t.Fatalf("font italic = %d", got)
	}
}
