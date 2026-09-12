package skvm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	shared "github.com/mirusu400/aram-core/runtime"
	"math"
	"testing"
)

// Independently authored guest bytecode, never SDK class bytes. Methodrefs on
// this subclass exercise inherited invokevirtual resolution, not the native map.
func flushProbeClass() []byte {
	var cp bytes.Buffer
	count := uint16(1)
	put := func(b *bytes.Buffer, v uint16) { _ = binary.Write(b, binary.BigEndian, v) }
	utf := func(s string) uint16 {
		i := count
		count++
		cp.WriteByte(1)
		put(&cp, uint16(len(s)))
		cp.WriteString(s)
		return i
	}
	cls := func(s string) uint16 {
		n := utf(s)
		i := count
		count++
		cp.WriteByte(7)
		put(&cp, n)
		return i
	}
	ref := func(c uint16, n, d string) uint16 {
		ni, di := utf(n), utf(d)
		nat := count
		count++
		cp.WriteByte(12)
		put(&cp, ni)
		put(&cp, di)
		i := count
		count++
		cp.WriteByte(10)
		put(&cp, c)
		put(&cp, nat)
		return i
	}
	self, super := cls("FlushProbe"), cls("javax/microedition/lcdui/game/GameCanvas")
	graphics, display := cls("javax/microedition/lcdui/Graphics"), cls("javax/microedition/lcdui/Display")
	op := func(o byte, i uint16) []byte { return []byte{o, byte(i >> 8), byte(i)} }
	type method struct {
		n, d  uint16
		flags uint16
		code  []byte
	}
	var methods []method
	add := func(n, d string, flags uint16, code []byte) {
		methods = append(methods, method{utf(n), utf(d), flags, code})
	}
	codeName := utf("Code")
	c := append([]byte{0x2a, 0x04}, op(0xb7, ref(super, "<init>", "(Z)V"))...)
	add("<init>", "()V", 1, append(c, 0xb1))
	c = append(op(0xbb, self), 0x59)
	c = append(c, op(0xb7, ref(self, "<init>", "()V"))...)
	add("create", "()LFlushProbe;", 9, append(c, 0xb0))
	c = append([]byte{0x01}, op(0xb8, ref(display, "getDisplay", "(Ljavax/microedition/midlet/MIDlet;)Ljavax/microedition/lcdui/Display;"))...)
	c = append(c, 0x2a)
	c = append(c, op(0xb6, ref(display, "setCurrent", "(Ljavax/microedition/lcdui/Displayable;)V"))...)
	add("show", "(LFlushProbe;)V", 9, append(c, 0xb1))
	for _, m := range []struct {
		n, d, target, td string
		loads            []byte
		ret              byte
	}{
		{"flush", "(LFlushProbe;)V", "flushGraphics", "()V", []byte{0x2a}, 0xb1},
		{"rect", "(LFlushProbe;IIII)V", "flushGraphics", "(IIII)V", []byte{0x2a, 0x1b, 0x1c, 0x1d, 0x15, 0x04}, 0xb1},
		{"graphics", "(LFlushProbe;)Ljavax/microedition/lcdui/Graphics;", "getGraphics", "()Ljavax/microedition/lcdui/Graphics;", []byte{0x2a}, 0xb0},
		{"width", "(LFlushProbe;)I", "getWidth", "()I", []byte{0x2a}, 0xac},
		{"height", "(LFlushProbe;)I", "getHeight", "()I", []byte{0x2a}, 0xac},
	} {
		c = append(m.loads, op(0xb6, ref(self, m.target, m.td))...)
		add(m.n, m.d, 9, append(c, m.ret))
	}
	for _, m := range []struct {
		n, d  string
		loads []byte
	}{
		{"setColor", "(I)V", []byte{0x2a, 0x1b}},
		{"fillRect", "(IIII)V", []byte{0x2a, 0x1b, 0x1c, 0x1d, 0x15, 0x04}},
		{"translate", "(II)V", []byte{0x2a, 0x1b, 0x1c}},
		{"setClip", "(IIII)V", []byte{0x2a, 0x1b, 0x1c, 0x1d, 0x15, 0x04}},
	} {
		c = append(m.loads, op(0xb6, ref(graphics, m.n, m.d))...)
		add(m.n, "(Ljavax/microedition/lcdui/Graphics;"+m.d[1:], 9, append(c, 0xb1))
	}
	var out bytes.Buffer
	u2 := func(v uint16) { put(&out, v) }
	u4 := func(v uint32) { _ = binary.Write(&out, binary.BigEndian, v) }
	u4(0xcafebabe)
	u2(3)
	u2(45)
	u2(count)
	out.Write(cp.Bytes())
	u2(1)
	u2(self)
	u2(super)
	u2(0)
	u2(0)
	u2(uint16(len(methods)))
	for _, m := range methods {
		u2(m.flags)
		u2(m.n)
		u2(m.d)
		u2(1)
		u2(codeName)
		u4(uint32(12 + len(m.code)))
		u2(6)
		u2(5)
		u4(uint32(len(m.code)))
		out.Write(m.code)
		u2(0)
		u2(0)
	}
	u2(0)
	return out.Bytes()
}

// Contract evidence: SDK record 803 GameCanvas.html SHA-256
// 83dd0bd5ce22798777b905748b31b540cf34f873b61e8e097ac7fdf032b66007.
// Documentation only, not an SDK-runtime fidelity claim.
func TestGameCanvasFlushPublicContract(t *testing.T) {
	for _, policy := range []NativePolicy{NativePolicyJ2ME, NativePolicySKT, NativePolicyLGT} {
		t.Run(fmt.Sprint(policy), func(t *testing.T) {
			cases := []struct {
				name string
				rect []int32
			}{
				{"whole", nil}, {"partial", []int32{-2, -1, 5, 4}}, {"bottom_right", []int32{238, 318, 10, 10}},
				{"offscreen", []int32{-10, -10, 2, 2}}, {"zero_width", []int32{0, 0, 0, 3}}, {"zero_height", []int32{0, 0, 3, 0}},
				{"negative_width", []int32{0, 0, -1, 3}}, {"negative_height", []int32{0, 0, 3, -1}},
				{"min_dimensions", []int32{0, 0, math.MinInt32, math.MinInt32}},
				{"max_extent", []int32{1, 1, math.MaxInt32, math.MaxInt32}},
				{"max_origin", []int32{math.MaxInt32, math.MaxInt32, math.MaxInt32, math.MaxInt32}},
				{"min_origin", []int32{math.MinInt32, math.MinInt32, math.MaxInt32, math.MaxInt32}},
			}
			for _, tc := range cases {
				for _, hidden := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/hidden_%t", tc.name, hidden), func(t *testing.T) {
						services, err := shared.NewServices(shared.Config{})
						check(t, err)
						vm, err := NewWithNativePolicy(map[string][]byte{"FlushProbe": flushProbeClass()}, services, 1, policy)
						check(t, err)
						call := func(n, d string, args ...Value) Value {
							t.Helper()
							v, _, e := vm.InvokeStatic(t.Context(), "FlushProbe", n, d, args...)
							check(t, e)
							return v
						}
						canvas := call("create", "()LFlushProbe;")
						other := call("create", "()LFlushProbe;")
						w := mustInt(t, call("width", "(LFlushProbe;)I", canvas))
						h := mustInt(t, call("height", "(LFlushProbe;)I", canvas))
						rect := tc.rect
						if tc.name == "bottom_right" {
							rect = []int32{w - 2, h - 2, 10, 10}
						}
						show := func(v Value) { call("show", "(LFlushProbe;)V", v); check(t, vm.ShowCurrent(t.Context())) }
						fill := func(v Value, color int32) Value {
							g := call("graphics", "(LFlushProbe;)Ljavax/microedition/lcdui/Graphics;", v)
							call("setColor", "(Ljavax/microedition/lcdui/Graphics;I)V", g, IntValue(color))
							call("fillRect", "(Ljavax/microedition/lcdui/Graphics;IIII)V", g, IntValue(0), IntValue(0), IntValue(w), IntValue(h))
							return g
						}
						fill(other, 0x123456)
						show(other)
						call("flush", "(LFlushProbe;)V", other)
						fill(canvas, 0xabcdef)
						show(canvas)
						// Establish old presented pixels, then change only the backing buffer.
						call("flush", "(LFlushProbe;)V", canvas)
						g := fill(canvas, 0x375b91)
						if hidden {
							show(other)
						}
						before := append([]byte(nil), vm.FrameRGBA()...)
						// Drawing state of the backing Graphics is irrelevant to presentation.
						call("translate", "(Ljavax/microedition/lcdui/Graphics;II)V", g, IntValue(13), IntValue(17))
						call("setClip", "(Ljavax/microedition/lcdui/Graphics;IIII)V", g, IntValue(0), IntValue(0), IntValue(0), IntValue(0))
						screen := ReferenceValue(vm.ScreenGraphics())
						call("translate", "(Ljavax/microedition/lcdui/Graphics;II)V", screen, IntValue(-9), IntValue(11))
						call("setClip", "(Ljavax/microedition/lcdui/Graphics;IIII)V", screen, IntValue(0), IntValue(0), IntValue(0), IntValue(0))
						drawBefore, err := vm.Services().Graphics.DrawState(vm.ServiceOwner(), vm.ScreenSurface())
						check(t, err)
						snapshot, err := vm.MarshalBinary()
						check(t, err)
						flush := func() {
							if rect == nil {
								call("flush", "(LFlushProbe;)V", canvas)
							} else {
								args := []Value{canvas}
								for _, v := range rect {
									args = append(args, IntValue(v))
								}
								call("rect", "(LFlushProbe;IIII)V", args...)
							}
							drawAfter, e := vm.Services().Graphics.DrawState(vm.ServiceOwner(), vm.ScreenSurface())
							check(t, e)
							if drawAfter != drawBefore {
								t.Fatal("flush changed screen drawing state")
							}
						}
						flush()
						want := append([]byte(nil), before...)
						if !hidden {
							for y := int32(0); y < h; y++ {
								for x := int32(0); x < w; x++ {
									inside := true
									if rect != nil {
										r := rect
										inside = r[2] > 0 && r[3] > 0 && int64(x) >= int64(r[0]) && int64(y) >= int64(r[1]) && int64(x) < int64(r[0])+int64(r[2]) && int64(y) < int64(r[1])+int64(r[3])
									}
									if inside {
										i := (int(y)*vm.ScreenWidth + int(x)) * 4
										copy(want[i:i+4], []byte{0x37, 0x5b, 0x91, 255})
									}
								}
							}
						}
						got := vm.FrameRGBA()
						if !bytes.Equal(got, want) {
							for i := range want {
								if got[i] != want[i] {
									t.Fatalf("pixel (%d,%d) channel %d got %d want %d", i/4%vm.ScreenWidth, i/4/vm.ScreenWidth, i%4, got[i], want[i])
								}
							}
						}
						after, err := vm.MarshalBinary()
						check(t, err)
						check(t, vm.UnmarshalBinary(snapshot))
						flush()
						replay, err := vm.MarshalBinary()
						check(t, err)
						if !bytes.Equal(after, replay) || !bytes.Equal(vm.FrameRGBA(), want) {
							t.Fatal("flush save/replay differs")
						}
					})
				}
			}
		})
	}
}
