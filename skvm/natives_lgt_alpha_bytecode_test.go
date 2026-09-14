package skvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

// This fixture enters through exported InvokeStatic and executes real
// checkcast/invokevirtual/exception-table bytecodes. Its inherited methodrefs
// deliberately name GraphicsX, exercising the host superclass resolution.
func lgtAlphaProbeClass(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	u2 := func(v uint16) { check(t, binary.Write(&out, binary.BigEndian, v)) }
	u4 := func(v uint32) { check(t, binary.Write(&out, binary.BigEndian, v)) }
	utf := func(s string) { out.WriteByte(constantUTF8); u2(uint16(len(s))); out.WriteString(s) }
	class := func(n uint16) { out.WriteByte(constantClass); u2(n) }
	nat := func(n, d uint16) { out.WriteByte(constantNameAndType); u2(n); u2(d) }
	method := func(n uint16) { out.WriteByte(constantMethodref); u2(9); u2(n) }
	u4(0xcafebabe)
	u2(3)
	u2(45)
	u2(23)
	utf("AlphaProbe")
	class(1)
	utf("java/lang/Object")
	class(3)
	utf("apply")
	utf("(Ljavax/microedition/lcdui/Graphics;I)I")
	utf("Code")
	utf(lgtGraphicsClass)
	class(8)
	utf("setAlpha")
	utf("(I)V")
	nat(10, 11)
	method(12)
	utf("setColor")
	nat(14, 11)
	method(15)
	utf("fillRect")
	utf("(IIII)V")
	nat(17, 18)
	method(19)
	utf("java/lang/IllegalArgumentException")
	class(21)
	u2(AccessPublic)
	u2(2)
	u2(4)
	u2(0)
	u2(0)
	u2(1)
	code := []byte{
		0x2a, 0xc0, 0, 9, 0x1b, 0xb6, 0, 13,
		0x2a, 0x02, 0xb6, 0, 16,
		0x2a, 0x03, 0x03, 0x04, 0x04, 0xb6, 0, 20,
		0x03, 0xac, 0x57, 0x04, 0xac,
	}
	u2(AccessPublic | AccessStatic)
	u2(5)
	u2(6)
	u2(1)
	u2(7)
	u4(uint32(12 + len(code) + 8))
	u2(5)
	u2(2)
	u4(uint32(len(code)))
	out.Write(code)
	u2(1)
	u2(0)
	u2(8)
	u2(23)
	u2(22)
	u2(0)
	u2(0)
	return out.Bytes()
}

func TestLGTGraphicsAlphaPublicBytecodeDispatch(t *testing.T) {
	services, err := shared.NewServices(shared.Config{})
	check(t, err)
	vm, err := NewWithNativePolicy(map[string][]byte{"AlphaProbe": lgtAlphaProbeClass(t)}, services, 1, NativePolicyLGT)
	check(t, err)
	ref := vm.ScreenGraphics()
	for _, tc := range []struct {
		alpha int32
		want  uint8
	}{{0, 0}, {1, 1}, {64, 64}, {128, 128}, {255, 254}, {256, 255}} {
		check(t, services.Graphics.Clear(1, vm.screenSurface, shared.Color{A: 255}))
		result, returns, err := vm.InvokeStatic(context.Background(), "AlphaProbe", "apply", "(Ljavax/microedition/lcdui/Graphics;I)I", ReferenceValue(ref), IntValue(tc.alpha))
		check(t, err)
		value, err := result.Int()
		check(t, err)
		if !returns || value != 0 {
			t.Fatal("valid invocation failed")
		}
		pixel := vm.FrameRGBA()[:4]
		if !bytes.Equal(pixel, []byte{tc.want, tc.want, tc.want, 255}) {
			t.Fatalf("alpha%d public frame pixel = %v", tc.alpha, pixel)
		}
	}
	for _, alpha := range []int32{-1, 257} {
		result, returns, err := vm.InvokeStatic(context.Background(), "AlphaProbe", "apply", "(Ljavax/microedition/lcdui/Graphics;I)I", ReferenceValue(ref), IntValue(alpha))
		check(t, err)
		value, err := result.Int()
		check(t, err)
		if !returns || value != 1 {
			t.Fatalf("invalid alpha%d not caught in guest", alpha)
		}
	}
}
