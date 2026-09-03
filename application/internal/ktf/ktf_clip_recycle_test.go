package ktf

import "testing"

// The clip recycler frees the host media service behind the oldest Java clip
// when the bounded pool is full. It picked its victim with 0 standing for "no
// victim yet", and a Clip constructed on a null receiver files itself under
// instance 0: whenever Go's randomized map order visited that entry last, the
// scan ended with the sentinel and the recycler reported that it had nothing
// to free. 영웅전설3 then faulted on its next Clip with "media clip count
// reached 256", at a different point in the run every time (issue #130).
func TestKTFClipRecyclerIgnoresTheNullInstance(t *testing.T) {
	// Map order is randomized per range, and the sentinel only swallowed the
	// victim when instance 0 came last, so one attempt proves nothing.
	for attempt := 0; attempt < 64; attempt++ {
		runtime := newCardOriginRuntime(t)
		for _, instance := range []uint32{0, 0x1000, 0x2000} {
			serviceID, err := runtime.Services.Media.CreateClip(
				runtime.ServiceOwner,
				"",
				0,
			)
			if err != nil {
				t.Fatal(err)
			}
			runtime.clipServices[instance] = serviceID
			runtime.clips[instance] = &ktfClip{volume: 100}
		}
		if !runtime.recycleKTFClipService() {
			t.Fatalf(
				"attempt %d: recycler found no victim with a null-instance clip present",
				attempt,
			)
		}
		if _, ok := runtime.clipServices[0]; !ok {
			t.Fatalf("attempt %d: recycler retired the null instance", attempt)
		}
		if _, ok := runtime.clipServices[0x1000]; ok {
			t.Fatalf("attempt %d: recycler kept the oldest real clip", attempt)
		}
	}
}

// Every clip playing is not a reason to give up: a handset mixer stops its
// oldest voice when a title asks for one more than the device has.
func TestKTFClipRecyclerTakesAPlayingClipWhenNoneAreIdle(t *testing.T) {
	runtime := newCardOriginRuntime(t)
	for _, instance := range []uint32{0x3000, 0x4000} {
		serviceID, err := runtime.Services.Media.CreateClip(
			runtime.ServiceOwner,
			"",
			0,
		)
		if err != nil {
			t.Fatal(err)
		}
		runtime.clipServices[instance] = serviceID
		runtime.clips[instance] = &ktfClip{volume: 100, playing: true}
	}
	if !runtime.recycleKTFClipService() {
		t.Fatal("recycler gave up with every clip playing")
	}
	if _, ok := runtime.clipServices[0x3000]; ok {
		t.Fatal("recycler kept the oldest playing clip")
	}
}
