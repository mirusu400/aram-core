package skvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"reflect"
	"strings"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func mmppStopSnapshot(t *testing.T, v *VM) []byte {
	t.Helper()
	b, err := v.MarshalBinary()
	check(t, err)
	return b
}

func TestMMPPEmptyStopIdentityAndIsolation(t *testing.T) {
	v := mmppVM(t, NativePolicyLGT)
	empty := mmppNew(t, v)
	active := mmppNew(t, v)
	invokeTestNative(t, v, mmppClass, "setPlayBackLoop", "(Z)V", empty, IntValue(1))
	mmppSource(t, v, active)
	mmppCall(t, v, active, "start")
	check(t, v.Advance(context.Background(), 20*time.Millisecond, nil))
	state := mmppStopSnapshot(t, v)
	want := v.services.Media.Drain()
	if len(want.PCM16) == 0 {
		t.Fatal("no queued PCM")
	}
	check(t, v.UnmarshalBinary(state))
	for round := 0; round < 2; round++ {
		before := mmppStopSnapshot(t, v)
		info := mmppInfo(t, v, active)
		revision := v.services.Media.OutputRevision()
		for i := 0; i < 3; i++ {
			mmppCall(t, v, empty, "stop")
			if !bytes.Equal(before, mmppStopSnapshot(t, v)) {
				t.Fatal("empty stop changed serialized VM/services")
			}
			if v.services.Media.OutputRevision() != revision || mmppInfo(t, v, active) != info {
				t.Fatal("empty stop changed active player")
			}
		}
		if got := v.services.Media.Drain(); !reflect.DeepEqual(got, want) {
			t.Fatal("empty stop changed queued PCM")
		}
		check(t, v.UnmarshalBinary(state))
	}
}

func TestMMPPEmptyStopMissingSourceCleanupThenPlayback(t *testing.T) {
	v := mmppVM(t, NativePolicyLGT)
	r := mmppNew(t, v)
	mmppCall(t, v, r, "stop")
	_, _, err := v.natives[nativeKey{mmppClass, "setMediaLocation", "(Ljava/lang/String;)V"}](context.Background(), v, r, []Value{ReferenceValue(v.NewString("missing.wav"))})
	if err == nil || !strings.Contains(err.Error(), "IOException") {
		t.Fatalf("missing location did not surface IOException: %v", err)
	}
	before := mmppStopSnapshot(t, v)
	mmppCall(t, v, r, "stop")
	if !bytes.Equal(before, mmppStopSnapshot(t, v)) {
		t.Fatal("cleanup mutated state")
	}
	for _, name := range []string{"start", "pause", "resume"} {
		_, _, e := v.natives[nativeKey{mmppClass, name, "()V"}](context.Background(), v, r, nil)
		if e == nil || !strings.Contains(e.Error(), "no source") {
			t.Fatalf("%s accepted absent source: %v", name, e)
		}
	}
	bad := v.newArray("[B", []Value{IntValue(1)})
	_, _, err = v.natives[nativeKey{mmppClass, "setMediaSource", "([B)V"}](context.Background(), v, r, []Value{ReferenceValue(bad)})
	if err == nil {
		t.Fatal("unknown source became playback success")
	}
	mmppCall(t, v, r, "stop")
	check(t, v.SetResourcesChecked(map[string][]byte{"real.wav": synthesizeToneWAV(69, 100, 80)}))
	invokeTestNative(t, v, mmppClass, "setMediaLocation", "(Ljava/lang/String;)V", r, ReferenceValue(v.NewString("/real.wav")))
	state := mmppStopSnapshot(t, v)
	run := func() []byte {
		mmppCall(t, v, r, "start")
		check(t, v.Advance(context.Background(), 20*time.Millisecond, nil))
		info := mmppInfo(t, v, r)
		if info.State != shared.ClipPlaying || info.Position != 20*time.Millisecond {
			t.Fatalf("no real playback: %+v", info)
		}
		out := v.services.Media.Drain()
		nonzero := false
		for _, sample := range out.PCM16 {
			nonzero = nonzero || sample != 0
		}
		if !nonzero {
			t.Fatal("no nonzero WAV PCM")
		}
		return mmppStopSnapshot(t, v)
	}
	first := run()
	check(t, v.UnmarshalBinary(state))
	if !bytes.Equal(first, run()) {
		t.Fatal("subsequent playback restore diverged")
	}
}

func TestMMPPEmptyStopRejectsInvalidReceivers(t *testing.T) {
	for _, kind := range []string{"null", "missing", "uninitialized", "wrong-native", "invalid-clip"} {
		t.Run(kind, func(t *testing.T) {
			v := mmppVM(t, NativePolicyLGT)
			var r uint32
			switch kind {
			case "null":
			case "missing":
				r = 999999
			case "uninitialized":
				r = v.NewObject(mmppClass, nil)
			case "wrong-native":
				r = v.NewString("not a player")
			case "invalid-clip":
				r = mmppNew(t, v)
				o, _ := v.Object(r)
				o.Native.(*audioClipState).clip = 999999
			}
			_, _, err := v.natives[nativeKey{mmppClass, "stop", "()V"}](context.Background(), v, r, nil)
			if err == nil {
				t.Fatal("invalid receiver/clip accepted")
			}
		})
	}
}

func TestMMPPStoppedClipCleanupIdentity(t *testing.T) {
	for _, mode := range []string{"prepared", "explicit-stop", "natural-end"} {
		t.Run(mode, func(t *testing.T) {
			v := mmppVM(t, NativePolicyLGT)
			r := mmppNew(t, v)
			mmppSource(t, v, r)
			if mode != "prepared" {
				mmppCall(t, v, r, "start")
				if mode == "natural-end" {
					check(t, v.Advance(context.Background(), 150*time.Millisecond, nil))
				} else {
					check(t, v.Advance(context.Background(), 20*time.Millisecond, nil))
					mmppCall(t, v, r, "stop")
				}
			}
			if mmppInfo(t, v, r).State != shared.ClipStopped {
				t.Fatal("test player is not stopped")
			}
			other := mmppNew(t, v)
			mmppSource(t, v, other)
			mmppCall(t, v, other, "start")
			check(t, v.Advance(context.Background(), 10*time.Millisecond, nil))
			before := mmppStopSnapshot(t, v)
			revision := v.services.Media.OutputRevision()
			for n := 0; n < 3; n++ {
				mmppCall(t, v, r, "stop")
				if !bytes.Equal(before, mmppStopSnapshot(t, v)) || revision != v.services.Media.OutputRevision() {
					t.Fatal("already-stopped cleanup mutated state or queued audio")
				}
			}
			check(t, v.UnmarshalBinary(before))
			mmppCall(t, v, r, "stop")
			if !bytes.Equal(before, mmppStopSnapshot(t, v)) {
				t.Fatal("restored stopped cleanup mutated state")
			}
			mmppCall(t, v, r, "start")
			check(t, v.Advance(context.Background(), 20*time.Millisecond, nil))
			if got := mmppInfo(t, v, r); got.State != shared.ClipPlaying || got.Position != 20*time.Millisecond {
				t.Fatalf("restart after cleanup failed: %+v", got)
			}
		})
	}
}

func TestMMPPOldV1StateRejectedAtomically(t *testing.T) {
	old := mmppVM(t, NativePolicyLGT)
	mmppNew(t, old)
	base := digestClassData(nil)
	old.classDigest = sha256.Sum256(append([]byte("lgt-mmpp-native-policy-v1\x00"), base[:]...))
	oldState := mmppStopSnapshot(t, old)
	v := mmppVM(t, NativePolicyLGT)
	r := mmppNew(t, v)
	mmppSource(t, v, r)
	mmppCall(t, v, r, "start")
	check(t, v.Advance(context.Background(), 20*time.Millisecond, nil))
	before := mmppStopSnapshot(t, v)
	revision := v.services.Media.OutputRevision()
	if err := v.UnmarshalBinary(oldState); err == nil {
		t.Fatal("old LGT v1 state accepted")
	}
	if !bytes.Equal(before, mmppStopSnapshot(t, v)) || revision != v.services.Media.OutputRevision() {
		t.Fatal("old state rejection mutated live VM/services")
	}
}
