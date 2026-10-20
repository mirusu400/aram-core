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
		state = state*214013 + 2531011
		want := int32(-50) + int32((state>>16)&0x7fff)%100
		if result.Pop != 2 || len(result.Push) != 1 || int32(result.Push[0]) != want {
			t.Fatalf("draw %d = %+v, want %d", draw, result, want)
		}
		if got := int32(result.Push[0]); got < -50 || got >= 50 {
			t.Fatalf("draw %d outside [-50, 50): %d", draw, got)
		}
	}
}

func TestRandMatchesLegacyKnownSequenceAndNormalizesBounds(t *testing.T) {
	runtime := newNVRuntime(t, 1)
	if _, err := runtime.service(0xc8, []uint32{1}); err != nil {
		t.Fatal(err)
	}
	for index, want := range []uint32{41, 18467, 6334, 26500, 19169, 15724, 11478, 29358, 26962, 24464} {
		result, err := runtime.service(0xc9, []uint32{0, 32768})
		if err != nil {
			t.Fatal(err)
		}
		if result.Pop != 2 || len(result.Push) != 1 || result.Push[0] != want {
			t.Fatalf("draw %d = %+v, want %d", index, result, want)
		}
	}

	runtime.randomState = 1
	forward, err := runtime.service(0xc9, []uint32{^uint32(49), 50})
	if err != nil {
		t.Fatal(err)
	}
	runtime.randomState = 1
	reversed, err := runtime.service(0xc9, []uint32{50, ^uint32(49)})
	if err != nil {
		t.Fatal(err)
	}
	if forward.Push[0] != reversed.Push[0] {
		t.Fatalf("reversed bounds = %d, forward = %d", reversed.Push[0], forward.Push[0])
	}
}

func TestRandEqualBoundsDoNotAdvanceState(t *testing.T) {
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
}

func TestRandCoversFullSignedRangeWithoutOverflow(t *testing.T) {
	runtime := newNVRuntime(t, 1)
	runtime.randomState = 0xffffffff
	result, err := runtime.service(0xc9, []uint32{0x80000000, 0x7fffffff})
	if err != nil {
		t.Fatal(err)
	}
	state := uint32(0xffffffff)
	state = state*214013 + 2531011
	want := int32(-2147483648) + int32((state>>16)&0x7fff)
	if result.Pop != 2 || len(result.Push) != 1 || int32(result.Push[0]) != want {
		t.Fatalf("full signed range = %+v, want %d", result, want)
	}
}
