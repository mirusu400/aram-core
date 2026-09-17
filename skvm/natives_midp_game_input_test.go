package skvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

// Entire class is synthetic. No host object/field writes or direct native calls.
func sdkGameProbeClass() []byte {
	return sdkInputProbeClass("SDKGameProbe", "javax/microedition/lcdui/game/GameCanvas")
}

func sdkInputProbeClass(name, parent string) []byte {
	var cp bytes.Buffer
	var count uint16 = 1
	put2 := func(b *bytes.Buffer, v uint16) { binary.Write(b, binary.BigEndian, v) }
	utf := func(s string) uint16 {
		i := count
		count++
		cp.WriteByte(1)
		put2(&cp, uint16(len(s)))
		cp.WriteString(s)
		return i
	}
	cls := func(s string) uint16 {
		n := utf(s)
		i := count
		count++
		cp.WriteByte(7)
		put2(&cp, n)
		return i
	}
	ref := func(c uint16, n, d string) uint16 {
		ni, di := utf(n), utf(d)
		nat := count
		count++
		cp.WriteByte(12)
		put2(&cp, ni)
		put2(&cp, di)
		i := count
		count++
		cp.WriteByte(10)
		put2(&cp, c)
		put2(&cp, nat)
		return i
	}
	self := cls(name)
	super := cls(parent)
	record := ref(cls("javax/microedition/lcdui/Canvas"), "recordKey", "(I)V")
	display := cls("javax/microedition/lcdui/Display")
	initSuper := ref(super, "<init>", "(Z)V")
	initSelf := ref(self, "<init>", "()V")
	poll := ref(super, "getKeyStates", "()I")
	getDisplay := ref(display, "getDisplay", "(Ljavax/microedition/midlet/MIDlet;)Ljavax/microedition/lcdui/Display;")
	setCurrent := ref(display, "setCurrent", "(Ljavax/microedition/lcdui/Displayable;)V")
	shown := ref(super, "isShown", "()Z")
	codeName := utf("Code")
	op := func(o byte, i uint16) []byte { return []byte{o, byte(i >> 8), byte(i)} }
	type method struct {
		name, desc uint16
		flags      uint16
		code       []byte
	}
	methods := []method{}
	add := func(n, d string, flags uint16, c []byte) { methods = append(methods, method{utf(n), utf(d), flags, c}) }
	for _, method := range []string{"keyPressed", "keyReleased"} {
		add(method, "(I)V", 1, append(append([]byte{0x1b}, op(0xb8, record)...), 0xb1))
	}
	add("<init>", "()V", 1, append(append([]byte{0x2a, 0x04}, op(0xb7, initSuper)...), 0xb1))
	c := append(op(0xbb, self), 0x59)
	c = append(c, op(0xb7, initSelf)...)
	c = append(c, 0xb0)
	add("create", "()LSDKGameProbe;", 9, c)
	add("poll", "(LSDKGameProbe;)I", 9, append(append([]byte{0x2a}, op(0xb6, poll)...), 0xac))
	c = append([]byte{0x01}, op(0xb8, getDisplay)...)
	c = append(c, 0x2a)
	c = append(c, op(0xb6, setCurrent)...)
	c = append(c, 0xb1)
	add("show", "(LSDKGameProbe;)V", 9, c)
	add("shown", "(LSDKGameProbe;)Z", 9, append(append([]byte{0x2a}, op(0xb6, shown)...), 0xac))
	var out bytes.Buffer
	u2 := func(v uint16) { put2(&out, v) }
	u4 := func(v uint32) { binary.Write(&out, binary.BigEndian, v) }
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
		u2(m.name)
		u2(m.desc)
		u2(1)
		u2(codeName)
		u4(uint32(12 + len(m.code)))
		u2(4)
		u2(2)
		u4(uint32(len(m.code)))
		out.Write(m.code)
		u2(0)
		u2(0)
	}
	u2(0)
	return out.Bytes()
}

func TestGameCanvasDisplayTransitionsAndCallbacks(t *testing.T) {
	for _, policy := range []NativePolicy{NativePolicyJ2ME, NativePolicySKT, NativePolicyLGT} {
		for _, suppress := range []int32{0, 1} {
			services, err := shared.NewServices(shared.Config{})
			check(t, err)
			vm, err := NewWithNativePolicy(map[string][]byte{
				"SDKGameProbe":  sdkGameProbeClass(),
				"SDKPlainProbe": sdkInputProbeClass("SDKPlainProbe", "javax/microedition/lcdui/Canvas"),
			}, services, 1, policy)
			check(t, err)
			canvas := vm.NewObject("SDKGameProbe", nil)
			plain := vm.NewObject("SDKPlainProbe", nil)
			alert := vm.NewObject("javax/microedition/lcdui/Alert", nil)
			invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "<init>", "(Z)V", canvas, IntValue(suppress))
			var delivered []int32
			// Guest callback wrappers forward their raw key to this test observer.
			for _, class := range []string{"javax/microedition/lcdui/Canvas"} {
				vm.RegisterNative(class, "recordKey", "(I)V", func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
					key, _ := args[0].Int()
					delivered = append(delivered, key)
					return Value{}, false, nil
				})
			}
			show := func(ref uint32) {
				invokeTestNative(t, vm, "javax/microedition/lcdui/Display", "setCurrent", "(Ljavax/microedition/lcdui/Displayable;)V", 0, ReferenceValue(ref))
			}
			poll := func(want int32) {
				t.Helper()
				got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "getKeyStates", "()I", canvas))
				if got != want {
					t.Fatalf("policy %v suppress %d: poll = %d want %d", policy, suppress, got, want)
				}
			}
			show(plain)
			check(t, vm.KeyEvent(t.Context(), 141, true))
			show(canvas)
			poll(0) // A key held on an ordinary Canvas is suppressed on entry.
			check(t, vm.KeyEvent(t.Context(), 141, false))
			check(t, vm.KeyEvent(t.Context(), 141, true))
			poll(2)
			invokeTestNative(t, vm, "javax/microedition/lcdui/Display", "setCurrent", "(Ljavax/microedition/lcdui/Alert;Ljavax/microedition/lcdui/Displayable;)V", 0, ReferenceValue(alert), ReferenceValue(canvas))
			poll(0)
			show(canvas)
			poll(0)
			show(0)
			poll(0)
			if err := vm.KeyEvent(t.Context(), 141, false); err == nil {
				t.Fatal("no-display input must retain its existing error")
			}
			show(canvas)
			check(t, vm.KeyEvent(t.Context(), 141, true))
			check(t, vm.KeyEvent(t.Context(), 141, false))
			poll(2)
			poll(0)
			wantCallbacks := 1
			if suppress == 0 {
				wantCallbacks += 4
			}
			if len(delivered) != wantCallbacks {
				t.Fatalf("policy %v suppress %d: callbacks %v want count %d", policy, suppress, delivered, wantCallbacks)
			}
			for _, key := range delivered {
				if key != 141 {
					t.Fatalf("SKT callback key changed to %d", key)
				}
			}
		}
	}
}

func TestGameCanvasInputStateCompatibility(t *testing.T) {
	for _, policy := range []NativePolicy{NativePolicyJ2ME, NativePolicySKT, NativePolicyLGT} {
		vm := policyRegressionVM(t, policy)
		save := func(vm *VM) []byte {
			t.Helper()
			data, err := vm.MarshalBinary()
			check(t, err)
			return data
		}
		initial := save(vm)
		canvas := vm.NewObject("javax/microedition/lcdui/Canvas", nil)
		vm.setCurrentDisplay(canvas)
		check(t, vm.KeyEvent(t.Context(), 141, true))
		check(t, vm.UnmarshalBinary(initial))
		if !bytes.Equal(initial, save(vm)) {
			t.Fatal("pre-input snapshot restore changed state")
		}
		// Synthesize the exact missing-field shape of pre-fix snapshots.
		delete(vm.hostStatic, gameCanvasHeldKeys)
		legacy := save(vm)
		fresh := policyRegressionVM(t, policy)
		check(t, fresh.UnmarshalBinary(legacy))
		if !bytes.Equal(initial, save(fresh)) {
			t.Fatal("legacy missing-field migration differs from empty physical state")
		}
		// Migration adds only the known missing field, never repairs bad types
		// or admits unrelated fields. Rejection must leave the live VM intact.
		for _, malformed := range []func(){
			func() { vm.hostStatic[gameCanvasHeldKeys] = LongValue(0) },
			func() {
				vm.hostStatic[gameCanvasHeldKeys] = IntValue(0)
				vm.hostStatic["unexpected-input-field"] = IntValue(0)
			},
		} {
			malformed()
			if err := fresh.UnmarshalBinary(save(vm)); err == nil {
				t.Fatal("malformed input state accepted")
			}
			if !bytes.Equal(initial, save(fresh)) {
				t.Fatal("rejected input state mutated live VM")
			}
		}
	}
}

func TestSDKGameCanvasPublicContract(t *testing.T) {
	for _, policy := range []NativePolicy{NativePolicyJ2ME, NativePolicySKT, NativePolicyLGT} {
		t.Run(map[NativePolicy]string{NativePolicyJ2ME: "J2ME", NativePolicySKT: "SKT", NativePolicyLGT: "LGT"}[policy], func(t *testing.T) {
			for _, scenario := range []string{"held_control", "released_between_polls", "hidden_stale_key", "reshown_held_key", "same_canvas", "first_show_held", "hidden_release", "replay_latch", "replay_suppression", "aliases_held", "aliases_suppressed"} {
				t.Run(scenario, func(t *testing.T) {
					services, err := shared.NewServices(shared.Config{})
					if err != nil {
						t.Fatal(err)
					}
					vm, err := NewWithNativePolicy(map[string][]byte{"SDKGameProbe": sdkGameProbeClass()}, services, 1, policy)
					if err != nil {
						t.Fatal(err)
					}
					ctx := context.Background()
					call := func(n, d string, args ...Value) Value {
						v, _, e := vm.InvokeStatic(ctx, "SDKGameProbe", n, d, args...)
						if e != nil {
							t.Fatalf("guest %s: %v", n, e)
						}
						return v
					}
					canvas := call("create", "()LSDKGameProbe;")
					other := call("create", "()LSDKGameProbe;")
					show := func(v Value) {
						call("show", "(LSDKGameProbe;)V", v)
						if err := vm.ShowCurrent(ctx); err != nil {
							t.Fatal(err)
						}
					}
					poll := func() int32 {
						v := call("poll", "(LSDKGameProbe;)I", canvas)
						i, e := v.Int()
						if e != nil {
							t.Fatal(e)
						}
						return i
					}
					want := func(label string, w int32) {
						g := poll()
						t.Logf("%s: got=%d want=%d", label, g, w)
						if g != w {
							t.Errorf("%s: got=%d want=%d", label, g, w)
						}
					}
					key := func(down bool) {
						if e := vm.KeyEvent(ctx, -1, down); e != nil {
							t.Fatal(e)
						}
					}
					save := func() []byte { t.Helper(); b, e := vm.MarshalBinary(); check(t, e); return b }
					restore := func(b []byte) { t.Helper(); check(t, vm.UnmarshalBinary(b)) }
					want("never shown", 0)
					show(canvas)
					want("initial showing", 0)
					key(true)
					switch scenario {
					case "aliases_held":
						check(t, vm.KeyEvent(ctx, '2', true))
						want("two physical UP keys held", 2)
						check(t, vm.KeyEvent(ctx, '2', false))
						want("other UP alias still held", 2)
						key(false)
						want("all UP aliases released", 0)
					case "aliases_suppressed":
						show(other)
						show(canvas)
						check(t, vm.KeyEvent(ctx, '2', true))
						want("fresh alias not suppressed", 2)
						check(t, vm.KeyEvent(ctx, '2', false))
						key(true)
						want("original alias remains suppressed", 0)
						key(false)
						key(true)
						want("original alias repressed", 2)
					case "held_control":
						want("held first poll", 2)
						want("held second poll", 2)
						key(false)
						want("released after observed press", 0)
					case "released_between_polls":
						key(false)
						want("press and release between polls latch", 2)
						want("latch cleared next poll", 0)
					case "hidden_stale_key":
						want("visible held", 2)
						show(other)
						v := call("shown", "(LSDKGameProbe;)Z", canvas)
						i, _ := v.Int()
						if i != 0 {
							t.Fatal("canvas did not hide")
						}
						want("hidden held key", 0)
					case "reshown_held_key":
						want("visible held", 2)
						show(other)
						show(canvas)
						want("reshown while held", 0)
						key(true)
						want("repeat of held key stays suppressed", 0)
						key(false)
						want("release after reshow", 0)
						key(true)
						want("new press after reshow", 2)
					case "same_canvas":
						show(canvas)
						want("reselecting current canvas preserves held key", 2)
						key(false)
						key(true)
						key(false)
						show(canvas)
						want("reselecting current canvas preserves latch", 2)
					case "first_show_held":
						show(other)
						canvas = other
						want("first showing while physical key held", 0)
						key(true)
						want("held repeat after first showing", 0)
						key(false)
						key(true)
						want("new press after first showing", 2)
					case "hidden_release":
						show(other)
						key(false)
						show(canvas)
						want("release delivered while hidden clears held state", 0)
						key(true)
						want("press after hidden release", 2)
					case "replay_latch":
						key(false)
						snapshot := save()
						freshServices, err := shared.NewServices(shared.Config{})
						check(t, err)
						fresh, err := NewWithNativePolicy(map[string][]byte{"SDKGameProbe": sdkGameProbeClass()}, freshServices, 1, policy)
						check(t, err)
						check(t, fresh.UnmarshalBinary(snapshot))
						freshBytes, err := fresh.MarshalBinary()
						check(t, err)
						if !bytes.Equal(snapshot, freshBytes) {
							t.Fatal("fresh VM restore changed snapshot")
						}
						want("latched tap before restore", 2)
						want("cleared latch before restore", 0)
						after := save()
						restore(snapshot)
						if !bytes.Equal(snapshot, save()) {
							t.Fatal("snapshot round trip changed bytes")
						}
						want("latched tap after restore", 2)
						want("cleared latch after restore", 0)
						if !bytes.Equal(after, save()) {
							t.Fatal("latch replay diverged")
						}
					case "replay_suppression":
						show(other)
						snapshot := save()
						replay := func() {
							show(canvas)
							want("suppressed after transition", 0)
							key(true)
							want("repeat suppressed", 0)
							key(false)
							key(true)
							want("fresh press", 2)
						}
						replay()
						after := save()
						restore(snapshot)
						if !bytes.Equal(snapshot, save()) {
							t.Fatal("suppression snapshot round trip changed bytes")
						}
						replay()
						if !bytes.Equal(after, save()) {
							t.Fatal("suppression replay diverged")
						}
					}
				})
			}
		})
	}
}
