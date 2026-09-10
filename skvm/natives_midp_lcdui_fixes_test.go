package skvm

import (
	"testing"
)

func TestMIDPOffScreenImagesStayMutable(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	// com.skt.m.Graphics2D.createMaskableImage builds an off-screen buffer, so
	// a title draws into it the same way it draws into Image.createImage(int,
	// int). Both must survive Image.getGraphics.
	for _, test := range []struct {
		class      string
		name       string
		descriptor string
	}{
		{
			"javax/microedition/lcdui/Image", "createImage",
			"(II)Ljavax/microedition/lcdui/Image;",
		},
		{
			"com/skt/m/Graphics2D", "createMaskableImage",
			"(II)Ljavax/microedition/lcdui/Image;",
		},
	} {
		t.Run(test.class, func(t *testing.T) {
			value := invokeTestNative(
				t, vm, test.class, test.name, test.descriptor, 0,
				IntValue(8), IntValue(8),
			)
			image, err := value.Reference()
			check(t, err)
			if mutable := mustInt(t, invokeTestNative(
				t, vm, "javax/microedition/lcdui/Image", "isMutable", "()Z",
				image,
			)); mutable != 1 {
				t.Fatalf("isMutable() = %d, want 1", mutable)
			}
			graphics := invokeTestNative(
				t, vm, "javax/microedition/lcdui/Image", "getGraphics",
				"()Ljavax/microedition/lcdui/Graphics;", image,
			)
			if reference, err := graphics.Reference(); err != nil ||
				reference == 0 {
				t.Fatalf("getGraphics() = %v, %v", reference, err)
			}
		})
	}
}

func TestMIDPFontRequestsShareOneInstance(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	// MIDP fonts are immutable value objects and Font.getFont answers a shared
	// instance. Building a new one per call also builds a new host font, which
	// reached the runtime's font limit in a title that asked inside paint.
	request := func() uint32 {
		value := invokeTestNative(
			t, vm, "javax/microedition/lcdui/Font", "getFont",
			"(III)Ljavax/microedition/lcdui/Font;", 0,
			IntValue(0), IntValue(1), IntValue(8),
		)
		reference, err := value.Reference()
		check(t, err)
		return reference
	}
	first := request()
	if second := request(); second != first {
		t.Fatalf("Font.getFont answered 0x%08x then 0x%08x", first, second)
	}
	other := invokeTestNative(
		t, vm, "javax/microedition/lcdui/Font", "getFont",
		"(III)Ljavax/microedition/lcdui/Font;", 0,
		IntValue(0), IntValue(2), IntValue(8),
	)
	reference, err := other.Reference()
	check(t, err)
	if reference == first {
		t.Fatal("a different style answered the same Font instance")
	}
}

func TestMIDPNullImageThrowsNullPointerException(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	// A title that draws a null Image is making its own mistake, and MIDP
	// reports it as a NullPointerException it can catch.
	_, err = vm.image(0)
	raised, ok := err.(*thrown)
	if !ok || raised.class != "java/lang/NullPointerException" {
		t.Fatalf("null Image error = %v, want a NullPointerException", err)
	}
}
