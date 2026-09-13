package skvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A constant PCM source gives an independent exact gain oracle, with no font,
// score synthesizer, host audio device or implementation-derived expected data.
func mmppConstantSource(t *testing.T, v *VM, r uint32) {
	t.Helper()
	const frames = 4410
	data := make([]byte, 44+frames*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 44100)
	binary.LittleEndian.PutUint32(data[28:], 88200)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], frames*2)
	for i := 44; i < len(data); i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 10000)
	}
	a := make([]Value, len(data))
	for i, b := range data {
		a[i] = IntValue(int32(int8(b)))
	}
	invokeTestNative(t, v, mmppClass, "setMediaSource", "([B)V", r, ReferenceValue(v.newArray("[B", a)))
}

func TestMMPPVolumeLevelsProduceExactPCM(t *testing.T) {
	for level := 0; level <= 5; level++ {
		t.Run(strconv.Itoa(level), func(t *testing.T) {
			v := mmppVM(t, NativePolicyLGT)
			r := mmppNew(t, v)
			mmppConstantSource(t, v, r)
			invokeTestNative(t, v, mmppClass, "setVolumeLevel", "(Ljava/lang/String;)V", r, ReferenceValue(v.NewString(strconv.Itoa(level))))
			if got := mmppInfo(t, v, r).Volume; got != uint8(level*20) {
				t.Fatalf("gain=%d", got)
			}
			mmppCall(t, v, r, "start")
			check(t, v.Advance(context.Background(), 10*time.Millisecond, nil))
			out := v.services.Media.Drain()
			if level > 0 && len(out.PCM16) == 0 {
				t.Fatal("successful nonzero volume produced no PCM")
			}
			for i, s := range out.PCM16 {
				if s != int16(level*2000) {
					t.Fatalf("sample %d=%d want %d", i, s, level*2000)
				}
			}
			if mmppInfo(t, v, r).Position == 0 {
				t.Fatal("volume prevented timeline advancement")
			}
		})
	}
}

func TestMMPPVolumeErrorsAreTransactional(t *testing.T) {
	v := mmppVM(t, NativePolicyLGT)
	r := mmppNew(t, v)
	mmppConstantSource(t, v, r)
	call := v.natives[nativeKey{mmppClass, "setVolumeLevel", "(Ljava/lang/String;)V"}]
	for _, text := range []string{"", " ", "-1", "6", "50", "1.0", "1,2", "2147483648", "not-a-level"} {
		arg := ReferenceValue(v.NewString(text))
		before, e := v.MarshalBinary()
		check(t, e)
		if _, _, e = call(context.Background(), v, r, []Value{arg}); e == nil || !strings.Contains(e.Error(), "unsupported") {
			t.Fatalf("invalid volume %q: %v", text, e)
		}
		after, e := v.MarshalBinary()
		check(t, e)
		if !bytes.Equal(before, after) {
			t.Fatalf("invalid volume %q changed state", text)
		}
	}
	empty := mmppNew(t, v)
	if _, _, e := call(context.Background(), v, empty, []Value{ReferenceValue(v.NewString("3"))}); e == nil {
		t.Fatal("volume without a source succeeded")
	}
	if _, _, e := call(context.Background(), v, r, []Value{ReferenceValue(0)}); e == nil {
		t.Fatal("null volume succeeded")
	}
	if _, _, e := call(context.Background(), v, 0, []Value{ReferenceValue(v.NewString("3"))}); e == nil {
		t.Fatal("invalid receiver succeeded")
	}
	// The getter's available-level versus current-level meaning is still unproven.
	if _, _, e := v.natives[nativeKey{mmppClass, "getVolumeLevel", "()Ljava/lang/String;"}](context.Background(), v, r, nil); e == nil {
		t.Fatal("guessed volume getter")
	}
}

func TestMMPPVolumePreservesClipStateAndSourceTransactions(t *testing.T) {
	v := mmppVM(t, NativePolicyLGT)
	r := mmppNew(t, v)
	mmppConstantSource(t, v, r)
	info := mmppInfo(t, v, r)
	check(t, v.services.Media.SetClipGain(v.serviceOwner, info.ID, 100, true, -25))
	invokeTestNative(t, v, mmppClass, "setVolumeLevel", "(Ljava/lang/String;)V", r, ReferenceValue(v.NewString("+03")))
	before := mmppInfo(t, v, r)
	if before.Volume != 60 || !before.Muted || before.Pan != -25 || before.State != info.State || before.Position != info.Position {
		t.Fatalf("volume changed other clip properties: %+v", before)
	}
	bad := ReferenceValue(v.newArray("[B", []Value{IntValue(1)}))
	if _, _, err := v.natives[nativeKey{mmppClass, "setMediaSource", "([B)V"}](context.Background(), v, r, []Value{bad}); err == nil {
		t.Fatal("bad replacement accepted")
	}
	if mmppInfo(t, v, r) != before {
		t.Fatal("failed replacement lost configured gain")
	}
	mmppConstantSource(t, v, r)
	after := mmppInfo(t, v, r)
	if after.Volume != 100 || after.Muted || after.Pan != 0 {
		t.Fatal("new source did not retain ordinary new-clip defaults")
	}
}

func TestMMPPVolumeReplayAndClipIsolation(t *testing.T) {
	v := mmppVM(t, NativePolicyLGT)
	a := mmppNew(t, v)
	b := mmppNew(t, v)
	mmppConstantSource(t, v, a)
	mmppConstantSource(t, v, b)
	arg := ReferenceValue(v.NewString("3"))
	invokeTestNative(t, v, mmppClass, "setVolumeLevel", "(Ljava/lang/String;)V", a, arg)
	if mmppInfo(t, v, b).Volume != 100 {
		t.Fatal("other player gain changed")
	}
	mmppCall(t, v, a, "start")
	check(t, v.Advance(context.Background(), 10*time.Millisecond, nil))
	before, e := v.MarshalBinary()
	check(t, e)
	rev := v.services.Media.OutputRevision()
	invokeTestNative(t, v, mmppClass, "setVolumeLevel", "(Ljava/lang/String;)V", a, arg)
	after, e := v.MarshalBinary()
	check(t, e)
	if !bytes.Equal(before, after) || v.services.Media.OutputRevision() != rev {
		t.Fatal("same gain invalidated state or PCM")
	}
	run := func() []byte {
		check(t, v.Advance(context.Background(), 10*time.Millisecond, nil))
		s, e := v.MarshalBinary()
		check(t, e)
		return s
	}
	first := run()
	check(t, v.UnmarshalBinary(before))
	second := run()
	if !bytes.Equal(first, second) || mmppInfo(t, v, a).Volume != 60 {
		t.Fatal("gain replay mismatch")
	}
	for _, policy := range []NativePolicy{NativePolicySKT, NativePolicyJ2ME} {
		other := mmppVM(t, policy)
		if other.SupportsNativeReference(mmppClass, "setVolumeLevel", "(Ljava/lang/String;)V") {
			t.Fatal("volume leaked to another policy")
		}
	}
}
