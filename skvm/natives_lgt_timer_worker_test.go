package skvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestLGTOneShotTimerRunsSleepingCallbackAcrossFrames(t *testing.T) {
	services, err := shared.NewServices(shared.Config{})
	check(t, err)
	vm, err := NewWithNativePolicy(map[string][]byte{"Game": sleepingTimerClass(t)}, services, 1, NativePolicyLGT)
	check(t, err)
	timer := vm.NewObject("java/util/Timer", nil)
	task := vm.NewObject("Game", &timerTaskState{})
	invokeTestNative(t, vm, "java/util/Timer", "<init>", "()V", timer)
	invokeTestNative(t, vm, "java/util/Timer", "schedule", "(Ljava/util/TimerTask;J)V", timer,
		ReferenceValue(task), LongValue(0))
	count := func() int32 {
		value := vm.classes["Game"].static[fieldStorageKey("Game", "counter", "I")]
		result, err := value.Int()
		check(t, err)
		return result
	}
	check(t, vm.Advance(context.Background(), 0, nil))
	if got := count(); got != 1 {
		t.Fatalf("first timer slice ran %d iterations, want 1", got)
	}
	if got := len(vm.services.Timers.Snapshot().Timers); got != 0 {
		t.Fatalf("one-shot timer retained %d service slots", got)
	}
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	check(t, vm.Advance(context.Background(), time.Millisecond, nil))
	if got := count(); got != 2 {
		t.Fatalf("second timer slice ran %d iterations, want 2", got)
	}
}

func sleepingTimerClass(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	u2 := func(v uint16) { t.Helper(); check(t, binary.Write(&out, binary.BigEndian, v)) }
	u4 := func(v uint32) { t.Helper(); check(t, binary.Write(&out, binary.BigEndian, v)) }
	utf := func(s string) { out.WriteByte(constantUTF8); u2(uint16(len(s))); out.WriteString(s) }
	class := func(name uint16) { out.WriteByte(constantClass); u2(name) }
	nameType := func(name, descriptor uint16) { out.WriteByte(constantNameAndType); u2(name); u2(descriptor) }
	member := func(tag byte, owner, nameType uint16) { out.WriteByte(tag); u2(owner); u2(nameType) }
	u4(0xcafebabe)
	u2(3)
	u2(45)
	u2(18)
	utf("Game")                       // 1
	class(1)                          // 2
	utf("java/lang/Object")           // 3
	class(3)                          // 4
	utf("run")                        // 5
	utf("()V")                        // 6
	utf("Code")                       // 7
	utf("counter")                    // 8
	utf("I")                          // 9
	nameType(8, 9)                    // 10
	member(constantFieldref, 2, 10)   // 11
	utf("java/lang/Thread")           // 12
	class(12)                         // 13
	utf("sleep")                      // 14
	utf("(J)V")                       // 15
	nameType(14, 15)                  // 16
	member(constantMethodref, 13, 16) // 17
	u2(AccessPublic)
	u2(2)
	u2(4)
	u2(0) // interfaces
	u2(1) // fields
	u2(AccessPublic | AccessStatic)
	u2(8)
	u2(9)
	u2(0)
	u2(1) // methods
	u2(AccessPublic)
	u2(5)
	u2(6)
	u2(1)
	u2(7)
	code := []byte{0xb2, 0, 11, 0x04, 0x60, 0xb3, 0, 11, 0x0a, 0xb8, 0, 17, 0xa7, 0xff, 0xf4}
	u4(uint32(12 + len(code)))
	u2(2) // max stack
	u2(1) // max locals
	u4(uint32(len(code)))
	out.Write(code)
	u2(0) // handlers
	u2(0) // code attributes
	u2(0) // class attributes
	return out.Bytes()
}
