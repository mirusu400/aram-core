package skvm

import (
	"bytes"
	"context"
	shared "github.com/mirusu400/aram-core/runtime"
	"strings"
	"testing"
	"time"
)

const mmppClass = "mmpp/media/MediaPlayer"

func mmppVM(t *testing.T, policy NativePolicy) *VM {
	t.Helper()
	s, e := shared.NewServices(shared.Config{})
	check(t, e)
	v, e := NewWithNativePolicy(nil, s, 1, policy)
	check(t, e)
	return v
}
func mmppNew(t *testing.T, v *VM) uint32 {
	t.Helper()
	r := v.NewObject(mmppClass, nil)
	invokeTestNative(t, v, mmppClass, "<init>", "()V", r)
	return r
}
func mmppCall(t *testing.T, v *VM, r uint32, n string) {
	t.Helper()
	invokeTestNative(t, v, mmppClass, n, "()V", r)
}
func mmppSource(t *testing.T, v *VM, r uint32) {
	t.Helper()
	data := synthesizeToneWAV(69, 100, 80)
	vals := make([]Value, len(data))
	for i, b := range data {
		vals[i] = IntValue(int32(int8(b)))
	}
	a := v.newArray("[B", vals)
	invokeTestNative(t, v, mmppClass, "setMediaSource", "([B)V", r, ReferenceValue(a))
}
func mmppInfo(t *testing.T, v *VM, r uint32) shared.ClipInfo {
	t.Helper()
	o, _ := v.Object(r)
	c, ok := o.Native.(*audioClipState)
	if !ok {
		t.Fatal("missing shared clip")
	}
	i, e := v.services.Media.Info(v.serviceOwner, c.clip)
	check(t, e)
	return i
}
func TestMMPPPCMContinuityLoopReplay(t *testing.T) {
	v := mmppVM(t, NativePolicyLGT)
	r := mmppNew(t, v)
	mmppSource(t, v, r)
	if got := mustInt(t, invokeTestNative(t, v, mmppClass, "isPlayBackLoop", "()Z", r)); got != 0 {
		t.Fatal("default loop")
	}
	mmppCall(t, v, r, "start")
	check(t, v.Advance(context.Background(), 20*time.Millisecond, nil))
	out := v.services.Media.Drain()
	nonzero := false
	for _, s := range out.PCM16 {
		if s != 0 {
			nonzero = true
		}
	}
	if !nonzero {
		t.Fatal("no actual PCM")
	}
	before := mmppInfo(t, v, r).Position
	if before <= 0 {
		t.Fatal("no progress")
	}
	mmppCall(t, v, r, "pause")
	check(t, v.Advance(context.Background(), 30*time.Millisecond, nil))
	if mmppInfo(t, v, r).Position != before {
		t.Fatal("pause progressed")
	}
	v.services.Media.Drain()
	state, e := v.MarshalBinary()
	check(t, e)
	run := func() []byte {
		mmppCall(t, v, r, "resume")
		check(t, v.Advance(context.Background(), 20*time.Millisecond, nil))
		if mmppInfo(t, v, r).Position != before+20*time.Millisecond {
			t.Fatal("resume did not continue from exact paused position")
		}
		b, e := v.MarshalBinary()
		check(t, e)
		return b
	}
	first := run()
	check(t, v.UnmarshalBinary(state))
	second := run()
	if !bytes.Equal(first, second) {
		t.Fatal("replay differs")
	}
	mmppCall(t, v, r, "stop")
	invokeTestNative(t, v, mmppClass, "setPlayBackLoop", "(Z)V", r, IntValue(1))
	mmppCall(t, v, r, "start")
	check(t, v.Advance(context.Background(), 250*time.Millisecond, nil))
	if mmppInfo(t, v, r).State != shared.ClipPlaying {
		t.Fatal("loop ended")
	}
	loopState, e := v.MarshalBinary()
	check(t, e)
	check(t, v.Advance(context.Background(), 125*time.Millisecond, nil))
	loopAfter, e := v.MarshalBinary()
	check(t, e)
	check(t, v.UnmarshalBinary(loopState))
	if mustInt(t, invokeTestNative(t, v, mmppClass, "isPlayBackLoop", "()Z", r)) != 1 {
		t.Fatal("state lost loop setting")
	}
	check(t, v.Advance(context.Background(), 125*time.Millisecond, nil))
	loopReplay, e := v.MarshalBinary()
	check(t, e)
	if !bytes.Equal(loopAfter, loopReplay) {
		t.Fatal("loop replay differs")
	}
	mmppCall(t, v, r, "stop")
	if mmppInfo(t, v, r).Position != 0 {
		t.Fatal("stop did not rewind emulator position")
	}
	invokeTestNative(t, v, mmppClass, "setPlayBackLoop", "(Z)V", r, IntValue(0))
	mmppCall(t, v, r, "start")
	check(t, v.Advance(context.Background(), 150*time.Millisecond, nil))
	if mmppInfo(t, v, r).State != shared.ClipStopped {
		t.Fatal("once kept playing")
	}
}
func TestMMPPBoundsMissingUnsupportedAndPolicy(t *testing.T) {
	v := mmppVM(t, NativePolicyLGT)
	r := mmppNew(t, v)
	_, _, e := v.natives[nativeKey{mmppClass, "setMediaLocation", "(Ljava/lang/String;)V"}](context.Background(), v, r, []Value{ReferenceValue(v.NewString("missing.wav"))})
	if e == nil || !strings.Contains(e.Error(), "IOException") {
		t.Fatalf("missing resource: %v", e)
	}
	a := v.newArray("[B", []Value{IntValue(1)})
	for _, pair := range [][2]int32{{-1, 1}, {0, -1}, {1, 1}, {2147483647, 2147483647}} {
		_, _, e = v.natives[nativeKey{mmppClass, "setMediaSource", "([BII)V"}](context.Background(), v, r, []Value{ReferenceValue(a), IntValue(pair[0]), IntValue(pair[1])})
		if e == nil {
			t.Fatal("bounds succeeded")
		}
	}
	for _, m := range []struct {
		n, d string
		a    []Value
	}{{"getVolumeLevel", "()Ljava/lang/String;", nil}, {"setVolumeLevel", "(Ljava/lang/String;)V", []Value{ReferenceValue(v.NewString("50"))}}} {
		_, _, e = v.natives[nativeKey{mmppClass, m.n, m.d}](context.Background(), v, r, m.a)
		if e == nil || !strings.Contains(e.Error(), "unsupported") {
			t.Fatalf("volume guessed: %v", e)
		}
	}
	state, e := v.MarshalBinary()
	check(t, e)
	for _, p := range []NativePolicy{NativePolicySKT, NativePolicyJ2ME} {
		other := mmppVM(t, p)
		if other.natives[nativeKey{mmppClass, "<init>", "()V"}] != nil {
			t.Fatal("mmpp leaked")
		}
		if other.UnmarshalBinary(state) == nil {
			t.Fatal("cross policy restore")
		}
	}
	for k := range v.natives {
		if !standardJavaClass(k.class) && k.class != mmppClass && k.class != "mmpp/media/BackLight" && k.class != "mmpp/lang/MathFP" && k.class != "mmpp/microedition/lcdui/GraphicsX" {
			t.Fatalf("OEM leaked: %v", k)
		}
	}
	other := mmppVM(t, NativePolicyLGT)
	v.RegisterNative(mmppClass, "start", "()V", nil)
	if other.natives[nativeKey{mmppClass, "start", "()V"}] == nil {
		t.Fatal("shared registry")
	}
}
