package skvm

import (
	"context"
	"errors"
	"testing"
)

func TestNullDataInputDelegateThrowsGuestException(t *testing.T) {
	vm, err := New(nil)
	check(t, err)
	receiver := vm.NewObject("java/io/DataInputStream", &dataInputState{})
	_, _, err = vm.natives[nativeKey{"java/io/DataInputStream", "read", "([BII)I"}](
		context.Background(), vm, receiver, nil,
	)
	var guest *thrown
	if !errors.As(err, &guest) || guest.class != "java/lang/NullPointerException" {
		t.Fatalf("null delegate error = %v, want guest NullPointerException", err)
	}
}

func TestNullByteArrayInputStreamDataThrowsGuestException(t *testing.T) {
	vm, err := New(nil)
	check(t, err)
	receiver := vm.NewObject("java/io/ByteArrayInputStream", nil)
	_, _, err = vm.natives[nativeKey{"java/io/ByteArrayInputStream", "<init>", "([B)V"}](
		context.Background(), vm, receiver, []Value{ReferenceValue(0)},
	)
	var guest *thrown
	if !errors.As(err, &guest) || guest.class != "java/lang/NullPointerException" {
		t.Fatalf("null byte array error = %v, want guest NullPointerException", err)
	}
}

// The three-argument constructor is registered separately from the one above.
// It exposed the host-level ByteArray error when 생존일기 passed a null record
// payload, faulting the machine before the guest's exception handler could run.
func TestNullByteArrayInputStreamSliceThrowsGuestException(t *testing.T) {
	vm, err := New(nil)
	check(t, err)
	receiver := vm.NewObject("java/io/ByteArrayInputStream", nil)
	_, _, err = vm.natives[nativeKey{"java/io/ByteArrayInputStream", "<init>", "([BII)V"}](
		context.Background(), vm, receiver,
		[]Value{ReferenceValue(0), IntValue(0), IntValue(0)},
	)
	var guest *thrown
	if !errors.As(err, &guest) || guest.class != "java/lang/NullPointerException" {
		t.Fatalf("null byte array slice error = %v, want guest NullPointerException", err)
	}
}

func TestDataInputStreamCanBeUsedAsInputStreamAfterRestore(t *testing.T) {
	vm, err := New(nil)
	check(t, err)
	data := testPNG(t, 0)
	inner := vm.NewObject("java/io/ByteArrayInputStream", &inputStreamState{data: data})
	outer := vm.NewObject("java/io/DataInputStream", &dataInputState{stream: inner})
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	value := invokeTestNative(t, vm, "javax/microedition/lcdui/Image", "createImage",
		"(Ljava/io/InputStream;)Ljavax/microedition/lcdui/Image;", 0, ReferenceValue(outer))
	reference, err := value.Reference()
	check(t, err)
	if _, err := vm.image(reference); err != nil {
		t.Fatalf("image from wrapped stream: %v", err)
	}
	stream, err := vm.inputStream(inner)
	check(t, err)
	if stream.offset != len(data) {
		t.Fatalf("wrapped stream consumed %d of %d bytes", stream.offset, len(data))
	}
}
