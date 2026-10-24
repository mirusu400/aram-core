package ktf

import "testing"

func TestKTFNativeTimerKeepsCletAliveForEvents(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	runtime.wipicTimers[0x10003000] = &ktfWIPICTimer{
		callback: 0x00101235,
		deadline: 40,
		active:   true,
	}

	if !runtime.CanAwaitEvents() {
		t.Fatal("active native timer did not keep Clet alive")
	}

	runtime.wipicTimers[0x10003000].active = false
	if runtime.CanAwaitEvents() {
		t.Fatal("inactive native timer kept Clet alive")
	}

	runtime.wipicTimers[0x10003000].active = true
	runtime.terminationRequested = true
	if runtime.CanAwaitEvents() {
		t.Fatal("terminated Clet remained alive for its native timer")
	}
}
