package skvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

func TestMMPPSourceTransactionsAndLocation(t *testing.T) {
	v := mmppVM(t, NativePolicyLGT)
	r := mmppNew(t, v)
	wav := synthesizeToneWAV(60, 100, 80)
	check(t, v.SetResourcesChecked(map[string][]byte{"clip.wav": wav}))
	invokeTestNative(t, v, mmppClass, "setMediaLocation", "(Ljava/lang/String;)V", r, ReferenceValue(v.NewString("/clip.wav")))
	original := mmppInfo(t, v, r)
	bad := v.newArray("[B", []Value{IntValue(1)})
	fail := func(n, d string, a ...Value) {
		t.Helper()
		_, _, e := v.natives[nativeKey{mmppClass, n, d}](context.Background(), v, r, a)
		if e == nil {
			t.Fatal("invalid replacement succeeded")
		}
		if got := mmppInfo(t, v, r); got != original {
			t.Fatalf("failed source changed clip: %+v -> %+v", original, got)
		}
	}
	fail("setMediaSource", "([B)V", ReferenceValue(bad))
	fail("setMediaSource", "([B)V", ReferenceValue(0))
	fail("setMediaSource", "([BII)V", ReferenceValue(bad), IntValue(0), IntValue(0))
	fail("setMediaLocation", "(Ljava/lang/String;)V", ReferenceValue(v.NewString("missing.wav")))
	// The bounded overload must copy exactly the requested region, not padding.
	padded := append([]byte{7, 8}, wav...)
	padded = append(padded, 9)
	values := make([]Value, len(padded))
	for i, b := range padded {
		values[i] = IntValue(int32(int8(b)))
	}
	a := v.newArray("[B", values)
	invokeTestNative(t, v, mmppClass, "setMediaSource", "([BII)V", r, ReferenceValue(a), IntValue(2), IntValue(int32(len(wav))))
	o, _ := v.Object(r)
	c := o.Native.(*audioClipState)
	source, e := v.services.Media.Source(v.serviceOwner, c.clip)
	check(t, e)
	if !bytes.Equal(source, wav) {
		t.Fatal("source slice mismatch")
	}
	arr, _ := v.Object(a)
	arr.Array.Elements[2] = IntValue(0)
	source, e = v.services.Media.Source(v.serviceOwner, c.clip)
	check(t, e)
	if !bytes.Equal(source, wav) {
		t.Fatal("source aliases Java array")
	}
	mmppCall(t, v, r, "start")
	check(t, v.Advance(context.Background(), 10*time.Millisecond, nil))
	original = mmppInfo(t, v, r)
	fail("setMediaSource", "([B)V", ReferenceValue(bad))
	mmppCall(t, v, r, "pause")
	original = mmppInfo(t, v, r)
	fail("setMediaSource", "([B)V", ReferenceValue(a))
	mmppCall(t, v, r, "resume")
	check(t, v.Advance(context.Background(), 10*time.Millisecond, nil))
	if mmppInfo(t, v, r).Position <= original.Position {
		t.Fatal("replacement failure broke resume")
	}
}

func TestMMPPPolicyDigestsAtomicityAndIdentity(t *testing.T) {
	base := digestClassData(nil)
	for _, p := range []NativePolicy{NativePolicySKT, NativePolicyJ2ME, NativePolicyLGT} {
		v := mmppVM(t, p)
		want := base
		if p == NativePolicyJ2ME {
			want = sha256.Sum256(append([]byte("j2me-native-policy-v1\x00"), base[:]...))
		}
		if p == NativePolicyLGT {
			want = sha256.Sum256(append([]byte("lgt-mmpp-native-policy-v1\x00"), base[:]...))
		}
		if v.classDigest != want {
			t.Fatalf("policy %d digest changed", p)
		}
		before, e := v.MarshalBinary()
		check(t, e)
		for _, otherPolicy := range []NativePolicy{NativePolicySKT, NativePolicyJ2ME, NativePolicyLGT} {
			if otherPolicy == p {
				continue
			}
			other := mmppVM(t, otherPolicy)
			state, e := other.MarshalBinary()
			check(t, e)
			if v.UnmarshalBinary(state) == nil {
				t.Fatal("cross-policy restore accepted")
			}
			after, e := v.MarshalBinary()
			check(t, e)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected restore mutated VM")
			}
		}
	}
	v := mmppVM(t, NativePolicyLGT)
	v.properties["microedition.configuration"] = "wrong"
	v.properties["MIN"] = "wrong"
	for key, want := range map[string]string{"microedition.configuration": "CLDC-1.0", "microedition.profiles": "MIDP-1.0", "MIN": "", "com.xce.wipi.version": ""} {
		value := invokeTestNative(t, v, "java/lang/System", "getProperty", "(Ljava/lang/String;)Ljava/lang/String;", 0, ReferenceValue(v.NewString(key)))
		r, e := value.Reference()
		check(t, e)
		if want == "" {
			if r != 0 {
				t.Fatal("carrier/app property leaked")
			}
		} else {
			got, e := v.String(r)
			check(t, e)
			if got != want {
				t.Fatalf("%s = %s", key, got)
			}
		}
	}
	for name := range v.hostSupers {
		if !standardJavaClass(name) && name != mmppClass && name != "mmpp/media/BackLight" && name != "mmpp/lang/MathFP" && name != "mmpp/microedition/lcdui/GraphicsX" {
			t.Fatalf("host class leaked %s", name)
		}
	}
	for key := range v.hostStatic {
		if strings.HasPrefix(key, "com/") || strings.HasPrefix(key, "org/") {
			t.Fatalf("OEM static leaked %s", key)
		}
	}
}
