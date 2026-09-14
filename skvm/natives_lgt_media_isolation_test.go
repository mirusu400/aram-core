package skvm

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestMMPPSourceDoesNotDiscardOtherPlayerPCM(t *testing.T) {
	for _, mode := range []string{"new", "replace", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			v := mmppVM(t, NativePolicyLGT)
			a := mmppNew(t, v)
			b := mmppNew(t, v)
			mmppSource(t, v, a)
			if mode == "replace" {
				mmppSource(t, v, b)
			}
			mmppCall(t, v, a, "start")
			check(t, v.Advance(context.Background(), 20*time.Millisecond, nil))
			state, e := v.MarshalBinary()
			check(t, e)
			want := v.services.Media.Drain()
			if len(want.PCM16) == 0 {
				t.Fatal("no queued PCM")
			}
			check(t, v.UnmarshalBinary(state))
			before := mmppInfo(t, v, a)
			revision := v.services.Media.OutputRevision()
			if mode == "invalid" {
				bad := v.newArray("[B", []Value{IntValue(1)})
				_, _, e = v.natives[nativeKey{mmppClass, "setMediaSource", "([B)V"}](context.Background(), v, b, []Value{ReferenceValue(bad)})
				if e == nil {
					t.Fatal("invalid source accepted")
				}
			} else {
				mmppSource(t, v, b)
			}
			got := v.services.Media.Drain()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s source discarded or changed other player PCM: got %d samples, want %d", mode, len(got.PCM16), len(want.PCM16))
			}
			if v.services.Media.OutputRevision() != revision {
				t.Fatal("unrelated output revision changed")
			}
			if mmppInfo(t, v, a) != before {
				t.Fatal("other player state changed")
			}
		})
	}
}
