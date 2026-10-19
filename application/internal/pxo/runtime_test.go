package pxo

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	pxoloader "github.com/mirusu400/aram-core/loader/pxo"
)

func TestRuntimeCallsPXO1HostTableAndReturns(t *testing.T) {
	image := make([]byte, 0x100)
	descriptor := pxoloader.Descriptor{
		DataStart: 0x80,
		DataSize:  0x80,
		DataEnd:   0x100,
		ZeroSize:  0x20,
		DataLoad:  0x80,
		Entry:     0x21,
		GOT:       0x60,
	}
	for index, value := range []uint32{
		descriptor.DataStart,
		descriptor.DataSize,
		descriptor.DataEnd,
		descriptor.ZeroSize,
		descriptor.DataLoad,
		descriptor.Entry,
		descriptor.GOT,
	} {
		binary.LittleEndian.PutUint32(image[index*4:], value)
	}
	// Entry: preserve the loader return, issue one-argument service
	// (argument=42, group=0x40, ordinal=7), then return its result.
	copy(image[0x20:], []byte{
		0x00, 0xb5, // push {lr}
		0x2a, 0x20, // movs r0, #42
		0x40, 0x21, // movs r1, #0x40
		0x07, 0x22, // movs r2, #7
		0x01, 0x4b, // ldr r3, [pc, #4]
		0x98, 0x47, // blx r3
		0x00, 0xbd, // pop {pc}
		0x00, 0x46, // nop
	})
	binary.LittleEndian.PutUint32(image[0x30:], 0x41)
	// Slot-one veneer: save the call frame and tail-call GOT[1].
	copy(image[0x40:], []byte{
		0x70, 0xb5, // push {r4-r6, lr}
		0x03, 0x4d, // ldr r5, [pc, #12]
		0x2c, 0x68, // ldr r4, [r5]
		0x65, 0x68, // ldr r5, [r4, #4]
		0xaf, 0x46, // mov pc, r5
	})
	binary.LittleEndian.PutUint32(image[0x50:], 0x18)
	pkg := &pxoloader.Package{
		Descriptor: descriptor,
		Fixups: [pxoloader.FixupGroups][]uint32{
			{0x00, 0x08, 0x10, 0x14, 0x18, 0x30, 0x50},
		},
		Image: image,
	}
	backend := interpreter.New()
	defer backend.Close()
	dispatcher := DispatchFunc(func(_ context.Context, _ *Runtime, call Call) (uint32, error) {
		if call.Slot != 1 || call.Group != 0x40 || call.Ordinal != 7 ||
			len(call.Args) != 1 || call.Args[0] != 42 {
			t.Fatalf("call = %+v", call)
		}
		return 99, nil
	})
	runtime, err := NewRuntime(backend, pkg, dispatcher)
	check(t, err)
	result, err := runtime.CallEvent(context.Background(), 1, 2, 3, 1024)
	check(t, err)
	if result != 99 {
		t.Fatalf("result = %d, want 99", result)
	}
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
