package skvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

// uncaughtProbeClass is a synthetic Canvas whose callbacks throw:
//
//	keyPressed(k): record(k); if k == 7 throw null;
//	               if k == 8 { try { boom(); } catch (any) {} }
//	paint(g):      record(100); throw null;
//	run():         record(101); throw null;
//	static boom(): throw null;
//
// record is a host observer registered on Canvas, so the test sees how far
// each callback got.
func uncaughtProbeClass() []byte {
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
	self := cls("UncaughtProbe")
	super := cls("javax/microedition/lcdui/Canvas")
	record := ref(super, "uncaughtProbeRecord", "(I)V")
	boom := ref(self, "boom", "()V")
	codeName := utf("Code")
	type handler struct{ start, end, target uint16 }
	type method struct {
		name, desc uint16
		flags      uint16
		code       []byte
		handlers   []handler
	}
	var methods []method
	add := func(n, d string, flags uint16, code []byte, handlers ...handler) {
		methods = append(methods, method{utf(n), utf(d), flags, code, handlers})
	}
	add("keyPressed", "(I)V", 1, []byte{
		0x1b,                                  // 0: iload_1
		0xb8, byte(record >> 8), byte(record), // 1: invokestatic record
		0x1b,    // 4: iload_1
		0x10, 7, // 5: bipush 7
		0xa0, 0, 5, // 7: if_icmpne 12
		0x01,    // 10: aconst_null
		0xbf,    // 11: athrow
		0x1b,    // 12: iload_1
		0x10, 8, // 13: bipush 8
		0xa0, 0, 8, // 15: if_icmpne 23
		0xb8, byte(boom >> 8), byte(boom), // 18: invokestatic boom
		0xb1, // 21: return
		0x57, // 22: pop (handler)
		0xb1, // 23: return
	}, handler{start: 18, end: 21, target: 22})
	add("paint", "(Ljavax/microedition/lcdui/Graphics;)V", 1, []byte{
		0x10, 100,
		0xb8, byte(record >> 8), byte(record),
		0x01, 0xbf,
	})
	add("run", "()V", 1, []byte{
		0x10, 101,
		0xb8, byte(record >> 8), byte(record),
		0x01, 0xbf,
	})
	add("boom", "()V", 9, []byte{0x01, 0xbf})

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
		u4(uint32(12 + len(m.code) + 8*len(m.handlers)))
		u2(4)
		u2(2)
		u4(uint32(len(m.code)))
		out.Write(m.code)
		u2(uint16(len(m.handlers)))
		for _, h := range m.handlers {
			u2(h.start)
			u2(h.end)
			u2(h.target)
			u2(0)
		}
		u2(0)
	}
	u2(0)
	return out.Bytes()
}

func uncaughtProbeVM(t *testing.T) (*VM, *[]int32) {
	t.Helper()
	services, err := shared.NewServices(shared.Config{})
	check(t, err)
	vm, err := NewWithNativePolicy(
		map[string][]byte{"UncaughtProbe": uncaughtProbeClass()},
		services,
		1,
		NativePolicySKT,
	)
	check(t, err)
	recorded := new([]int32)
	vm.RegisterNative(
		"javax/microedition/lcdui/Canvas",
		"uncaughtProbeRecord",
		"(I)V",
		func(_ context.Context, _ *VM, _ uint32, args []Value) (Value, bool, error) {
			value, err := args[0].Int()
			*recorded = append(*recorded, value)
			return Value{}, false, err
		},
	)
	return vm, recorded
}

func requireUncaught(t *testing.T, vm *VM, wantCount uint64, callback, method string, pc int) {
	t.Helper()
	count, last, first := vm.UncaughtExceptions()
	if count != wantCount {
		t.Fatalf("uncaught count = %d, want %d (%+v)", count, wantCount, first)
	}
	if last.Class != "java/lang/NullPointerException" ||
		last.Callback != callback || last.Method != method || last.PC != pc {
		t.Fatalf(
			"last uncaught = %+v, want NullPointerException from %s at %s pc %d",
			last, callback, method, pc,
		)
	}
	if last.Text == "" {
		t.Fatal("uncaught record carries no fault text")
	}
}

// 팔콤영웅전설 EP1 throws ArrayIndexOutOfBoundsException out of keyPressed
// every time its volume option changes. The event loop drops that key and
// delivers the next one; the session does not end.
func TestUncaughtKeyPressedExceptionKeepsTheTitleRunning(t *testing.T) {
	vm, recorded := uncaughtProbeVM(t)
	ctx := context.Background()
	canvas := vm.NewObject("UncaughtProbe", nil)
	vm.setCurrentDisplay(canvas)

	if err := vm.KeyEvent(ctx, 7, true); err != nil {
		t.Fatalf("keyPressed that throws ended the session: %v", err)
	}
	requireUncaught(t, vm, 1, "keyPressed", "UncaughtProbe.keyPressed(I)V", 11)
	if err := vm.KeyEvent(ctx, 5, true); err != nil {
		t.Fatalf("next key after an uncaught exception: %v", err)
	}
	// An exception the title catches itself is not an uncaught one.
	if err := vm.KeyEvent(ctx, 8, true); err != nil {
		t.Fatalf("key whose exception the title catches: %v", err)
	}
	requireUncaught(t, vm, 1, "keyPressed", "UncaughtProbe.keyPressed(I)V", 11)
	if got := *recorded; len(got) != 3 || got[0] != 7 || got[1] != 5 || got[2] != 8 {
		t.Fatalf("keyPressed saw %v, want [7 5 8]", got)
	}
}

// A paint that throws has consumed its request: the event loop does not
// spin on it, and only the title's own next repaint() schedules another.
func TestUncaughtPaintExceptionConsumesTheRepaint(t *testing.T) {
	vm, recorded := uncaughtProbeVM(t)
	canvas := vm.NewObject("UncaughtProbe", nil)
	vm.setCurrentDisplay(canvas)
	vm.setRepaintPending(true)

	if err := vm.PaintCurrent(context.Background()); err != nil {
		t.Fatalf("paint that throws ended the session: %v", err)
	}
	if vm.RepaintPending() {
		t.Fatal("a paint that threw re-armed its own repaint request")
	}
	requireUncaught(t, vm, 1, "paint", "UncaughtProbe.paint(Ljavax/microedition/lcdui/Graphics;)V", 6)
	if got := *recorded; len(got) != 1 || got[0] != 100 {
		t.Fatalf("paint saw %v, want [100]", got)
	}
}

// An exception escaping run() ends that thread and nothing else.
func TestUncaughtThreadExceptionEndsOnlyThatThread(t *testing.T) {
	vm, recorded := uncaughtProbeVM(t)
	target := vm.NewObject("UncaughtProbe", nil)
	worker := &threadState{target: target, started: true, active: true}
	reference := vm.NewObject("java/lang/Thread", worker)

	if err := vm.runThread(context.Background(), reference, worker); err != nil {
		t.Fatalf("thread that throws ended the session: %v", err)
	}
	if worker.active {
		t.Fatal("thread is still active after its run() threw")
	}
	requireUncaught(t, vm, 1, "Thread.run", "UncaughtProbe.run()V", 6)
	if got := *recorded; len(got) != 1 || got[0] != 101 {
		t.Fatalf("run saw %v, want [101]", got)
	}
}

// A host fault is not a guest exception and still ends the session.
func TestAbsorbUncaughtPassesHostErrorsThrough(t *testing.T) {
	vm, _ := uncaughtProbeVM(t)
	hostErr := ErrMethodNotFound
	if err := vm.absorbUncaught("paint", hostErr); err != hostErr {
		t.Fatalf("absorbUncaught(host error) = %v, want it unchanged", err)
	}
	if count, _, _ := vm.UncaughtExceptions(); count != 0 {
		t.Fatalf("host error was recorded as an uncaught exception (%d)", count)
	}
}

// A title that throws from every paint must not grow the log without bound.
func TestUncaughtLogIsBounded(t *testing.T) {
	vm, _ := uncaughtProbeVM(t)
	for range maxUncaughtRecords * 3 {
		check(t, vm.absorbUncaught("paint", &thrown{class: "java/lang/Error"}))
	}
	count, _, first := vm.UncaughtExceptions()
	if count != maxUncaughtRecords*3 || len(first) != maxUncaughtRecords {
		t.Fatalf("log kept %d records of %d, want %d", len(first), count, maxUncaughtRecords)
	}
}
