package ktf

import (
	"testing"
)

// TestKTFJavaClipsRecycleTheBoundedPool covers the shape random key input
// reached on 영웅전설3: KTF Java has no collector, so a title that constructs a
// Clip per sound effect walks the bounded media pool up to its limit. Asking
// for one more must retire an older clip rather than fault.
func TestKTFJavaClipsRecycleTheBoundedPool(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	// Well past the 256-clip pool.
	const clips = 1000
	for index := 1; index <= clips; index++ {
		instance := uint32(index)
		runtime.clips[instance] = &ktfClip{volume: 100}
		if _, err := runtime.ensureKTFClipService(instance); err != nil {
			t.Fatalf("clip %d: %v", index, err)
		}
	}
	if live := len(runtime.clipServices); live > 256 {
		t.Fatalf("%d clips hold %d services, over the pool", clips, live)
	}
}

// TestKTFJavaClipsRecycleEvenWhenEveryClipPlays pins the harder half: with
// every clip playing there is no idle victim, and a handset mixer asked for
// more simultaneous voices than it has stops the oldest rather than refusing.
func TestKTFJavaClipsRecycleEvenWhenEveryClipPlays(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	for index := 1; index <= 600; index++ {
		instance := uint32(index)
		runtime.clips[instance] = &ktfClip{volume: 100, playing: true}
		if _, err := runtime.ensureKTFClipService(instance); err != nil {
			t.Fatalf("playing clip %d: %v", index, err)
		}
	}
}
