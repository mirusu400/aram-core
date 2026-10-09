package brewrt

import (
	"context"
	"encoding/binary"
	"image"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestHandsetDisplayStrideSeparatesNativeFramebufferRows(t *testing.T) {
	for _, size := range []image.Point{{X: 120, Y: 160}, {X: 176, Y: 202}} {
		// A native handset consumer obtains the pixels through the OEM display
		// anchor and doubles the display's pixel stride to address the next row.
		module := make([]byte, 28)
		for i, word := range []uint32{
			0xe590100c, // ldr r1, [r0, #12]
			0xe5911010, // ldr r1, [r1, #16]
			0xe5902010, // ldr r2, [r0, #16]
			0xe0811082, // add r1, r1, r2, lsl #1
			0xe3a03001, // mov r3, #1
			0xe1c130b0, // strh r3, [r1]
			0xe12fff1e, // bx lr
		} {
			binary.LittleEndian.PutUint32(module[i*4:], word)
		}
		r, err := New(Package{Module: module, NativeCanvas: size})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close() })
		if err := r.cpu.WriteMemory(framebufferBase, make([]byte, size.X*4)); err != nil {
			t.Fatal(err)
		}
		if err := r.runCallback(context.Background(), brewCallback{function: moduleBase, context: displayObject}); err != nil {
			t.Fatal(err)
		}
		var row0, row1 [2]byte
		if err := r.cpu.ReadMemory(framebufferBase, row0[:]); err != nil {
			t.Fatal(err)
		}
		if err := r.cpu.ReadMemory(framebufferBase+uint32(size.X*2), row1[:]); err != nil {
			t.Fatal(err)
		}
		if first, second := binary.LittleEndian.Uint16(row0[:]), binary.LittleEndian.Uint16(row1[:]); first != 0 || second != 1 {
			t.Fatalf("canvas %v: row zero=%04x row one=%04x, want 0000/0001", size, first, second)
		}
		if got, err := r.cpu.ReadRegister(cpu.RegisterR2); err != nil || got != uint32(size.X) {
			t.Fatalf("canvas %v: native pixel stride=%d err=%v", size, got, err)
		}
	}
}
