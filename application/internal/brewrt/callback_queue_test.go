package brewrt

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
)

func timerMutationRuntime(t *testing.T, replace bool) *Runtime {
	t.Helper()
	module := make([]byte, 0x60)
	var instructions []uint32
	if replace {
		instructions = []uint32{
			0xe92d4010, // push {r4, lr}
			0xe1a03000, // mov r3, r0: callback context
			0xe3a01019, // mov r1, #25: replacement delay
			0xe59f2010, // ldr r2, [pc, #16]: callback B
			0xe59f0010, // ldr r0, [pc, #16]: shell
			0xe59f4010, // ldr r4, [pc, #16]: SetTimer
			0xe12fff34, // blx r4
			0xe8bd4010, // pop {r4, lr}
			0xe12fff1e, // bx lr
			moduleBase + 0x40,
			shellObject,
			shellMethodTrapBase + 11*2 | 1,
		}
	} else {
		instructions = []uint32{
			0xe92d4010, // push {r4, lr}
			0xe3a01000, // mov r1, #0: any function
			0xe1a02000, // mov r2, r0: callback context
			0xe59f000c, // ldr r0, [pc, #12]: shell
			0xe59f300c, // ldr r3, [pc, #12]: CancelTimer
			0xe12fff33, // blx r3
			0xe8bd4010, // pop {r4, lr}
			0xe12fff1e, // bx lr
			shellObject,
			shellMethodTrapBase + 12*2 | 1,
		}
	}
	for index, instruction := range instructions {
		binary.LittleEndian.PutUint32(module[index*4:], instruction)
	}
	for index, instruction := range []uint32{
		0xe5901000, // ldr r1, [r0]
		0xe2811001, // add r1, r1, #1
		0xe5801000, // str r1, [r0]
		0xe12fff1e, // bx lr
	} {
		binary.LittleEndian.PutUint32(module[0x40+index*4:], instruction)
	}
	runtime, err := New(Package{Module: module})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func callbackCount(t *testing.T, runtime *Runtime) uint32 {
	t.Helper()
	var encoded [4]byte
	if err := runtime.cpu.ReadMemory(outputAddr, encoded[:]); err != nil {
		t.Fatal(err)
	}
	return binary.LittleEndian.Uint32(encoded[:])
}

func TestCallbackCanCancelAnotherTimerDueInSameFrame(t *testing.T) {
	runtime := timerMutationRuntime(t, false)
	runtime.timers = []brewCallback{
		{function: moduleBase, context: outputAddr},
		{function: moduleBase + 0x40, context: outputAddr},
	}
	if err := runtime.RunCallbacks(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if got := callbackCount(t, runtime); got != 0 || len(runtime.timers) != 0 {
		t.Fatalf("canceled callback count=%d pending=%d", got, len(runtime.timers))
	}
}

func TestCallbackCanReplaceAnotherTimerDueInSameFrame(t *testing.T) {
	runtime := timerMutationRuntime(t, true)
	runtime.timers = []brewCallback{
		{function: moduleBase, context: outputAddr},
		{function: moduleBase + 0x40, context: outputAddr},
	}
	if err := runtime.RunCallbacks(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if got := callbackCount(t, runtime); got != 0 || len(runtime.timers) != 1 || runtime.timers[0].remaining != 25*time.Millisecond {
		t.Fatalf("replacement count=%d timers=%+v", got, runtime.timers)
	}
	if err := runtime.RunCallbacks(context.Background(), 24*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := callbackCount(t, runtime); got != 0 {
		t.Fatalf("replacement fired early: count=%d", got)
	}
	if err := runtime.RunCallbacks(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := callbackCount(t, runtime); got != 1 || len(runtime.timers) != 0 {
		t.Fatalf("replacement count=%d pending=%d", got, len(runtime.timers))
	}
}

func TestCleanupCallbackCancelsTimersButNotOtherOwnershipCleanups(t *testing.T) {
	runtime := timerMutationRuntime(t, false)
	runtime.cleanupCallbacks = []brewCallback{
		{function: moduleBase, context: outputAddr},
		{function: moduleBase + 0x40, context: outputAddr},
	}
	runtime.timers = []brewCallback{{function: moduleBase + 0x40, context: outputAddr}}
	if err := runtime.RunCallbacks(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if got := callbackCount(t, runtime); got != 1 || len(runtime.timers) != 0 || len(runtime.cleanupCallbacks) != 0 {
		t.Fatalf("cleanup count=%d timers=%d cleanups=%d", got, len(runtime.timers), len(runtime.cleanupCallbacks))
	}
}

func TestPollingTimerExpirationAdvancesAllQueuedTimers(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.timers = []brewCallback{
		{function: moduleBase, context: 1, remaining: 5 * time.Millisecond},
		{function: moduleBase + 4, context: 2, remaining: 5 * time.Millisecond},
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: moduleBase, cpu.RegisterR2: 1, cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 13*2 + 2); err != nil {
		t.Fatal(err)
	}
	for _, timer := range runtime.timers {
		if timer.remaining != 4*time.Millisecond {
			t.Fatalf("polling advanced only part of the queue: %+v", runtime.timers)
		}
	}
}
