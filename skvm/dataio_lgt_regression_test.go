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
