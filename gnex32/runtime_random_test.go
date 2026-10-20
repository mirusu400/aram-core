package gnex32

import "testing"

func TestRandSeedProducesDeterministicExclusiveRanges(t *testing.T) {
	runtime := newNVRuntime(t, 1)
	result, err := runtime.service(0xc8, []uint32{123})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 1 || len(result.Push) != 0 {
		t.Fatalf("RandSeed stack result = %+v", result)
	}

	state := uint32(123)
	for draw := 0; draw < 16; draw++ {
		result, err = runtime.service(0xc9, []uint32{^uint32(49), 50})
		if err != nil {
			t.Fatal(err)
		}
		state = state*1664525 + 1013904223
		want := int32(-50) + int32(state%100)
		if result.Pop != 2 || len(result.Push) != 1 || int32(result.Push[0]) != want {
			t.Fatalf("draw %d = %+v, want %d", draw, result, want)
		}
		if got := int32(result.Push[0]); got < -50 || got >= 50 {
			t.Fatalf("draw %d outside [-50, 50): %d", draw, got)
		}
	}
}

func TestRandEqualAndInvalidBoundsDoNotAdvanceState(t *testing.T) {
	runtime := newNVRuntime(t, 1)
	runtime.randomState = 0x12345678

	result, err := runtime.service(0xc9, []uint32{7, 7})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 2 || len(result.Push) != 1 || result.Push[0] != 7 {
		t.Fatalf("equal-bound Rand result = %+v", result)
	}
	if runtime.randomState != 0x12345678 {
		t.Fatalf("equal-bound Rand advanced state to %#x", runtime.randomState)
	}

	if _, err := runtime.service(0xc9, []uint32{8, 7}); err == nil {
		t.Fatal("Rand accepted descending bounds")
	}
	if runtime.randomState != 0x12345678 {
		t.Fatalf("invalid Rand advanced state to %#x", runtime.randomState)
	}
}
