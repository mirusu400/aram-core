package runtime

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestLCG214013KnownSequenceAndModularOracle(t *testing.T) {
	if RNGLCG214013Output15 != RNGAlgorithm("lcg32-214013-2531011-output15") {
		t.Fatal("algorithm identity changed")
	}
	r := NewRandom(999, 4)
	check(t, r.SetLCG214013Seed("known", 1))
	for i, want := range []uint32{41, 18467, 6334, 26500, 19169, 15724, 11478, 29358, 26962, 24464} {
		got, err := r.LCG214013Output15("known")
		check(t, err)
		if got != want {
			t.Fatalf("known draw %d = %d, want %d", i, got, want)
		}
	}
	for _, seed := range []uint32{0, 1, 0x80000000, math.MaxUint32} {
		check(t, r.SetLCG214013Seed("oracle", seed))
		state := uint64(seed)
		for i := 0; i < 5000; i++ {
			// Widened modular arithmetic is independent of production uint32 overflow.
			state = (state*214013 + 2531011) % (uint64(1) << 32)
			got, err := r.LCG214013Output15("oracle")
			check(t, err)
			if want := uint32((state / 65536) % 32768); got != want {
				t.Fatalf("seed %d draw %d = %d, want %d", seed, i, got, want)
			}
			saved := r.Snapshot().Streams[1]
			if saved.State != [4]uint64{state} || saved.Draws != uint64(i+1) {
				t.Fatalf("noncanonical state: %+v", saved)
			}
		}
	}
}

func TestLCG214013ErrorsDoNotMutate(t *testing.T) {
	r := NewRandom(42, 3)
	_, err := r.Uint64("xoshiro")
	check(t, err)
	check(t, r.SetJavaSeed("java", 7))
	check(t, r.SetLCG214013Seed("lcg", 0))
	assertError := func(name string, want error, call func() error) {
		t.Helper()
		before := r.Snapshot()
		err := call()
		if !errors.Is(err, want) {
			t.Fatalf("%s error %v, want %v", name, err, want)
		}
		if !reflect.DeepEqual(before, r.Snapshot()) {
			t.Fatalf("%s mutated state", name)
		}
	}
	draw := func(name string) func() error {
		return func() error {
			v, e := r.LCG214013Output15(name)
			if e != nil && v != 0 {
				t.Errorf("error draw returned %d", v)
			}
			return e
		}
	}
	for _, name := range []string{"", " \t\n", strings.Repeat("a", 65), "bad\x00name"} {
		assertError("invalid draw", ErrInvalidArgument, draw(name))
		assertError("invalid seed", ErrInvalidArgument, func() error { return r.SetLCG214013Seed(name, 1) })
	}
	assertError("missing even at cap", ErrNotFound, draw("missing"))
	assertError("cap", ErrLimitExceeded, func() error { return r.SetLCG214013Seed("missing", 1) })
	for _, name := range []string{"java", "xoshiro"} {
		assertError("wrong draw", ErrInvalidState, draw(name))
		assertError("wrong seed", ErrInvalidState, func() error { return r.SetLCG214013Seed(name, 1) })
	}
	assertError("old xoshiro mismatch", ErrInvalidState, func() error { _, e := r.Uint64("lcg"); return e })
	assertError("old java mismatch", ErrInvalidState, func() error { _, e := r.JavaInt("lcg"); return e })
	state := r.Snapshot()
	state.Streams[1].Draws = math.MaxUint64 - 1
	check(t, r.Restore(state))
	_, err = r.LCG214013Output15("lcg")
	check(t, err)
	assertError("exhaustion", ErrLimitExceeded, draw("lcg"))
	check(t, r.SetLCG214013Seed("lcg", 0)) // Reset is allowed even at the stream cap and draw limit.
	saved := r.Snapshot().Streams[1]
	if saved.State != [4]uint64{} || saved.Draws != 0 {
		t.Fatalf("reset: %+v", saved)
	}
	v, err := r.LCG214013Output15("lcg")
	check(t, err)
	if v != 38 {
		t.Fatalf("zero seed first draw %d", v)
	}
	empty := NewRandom(0, 1)
	before := empty.Snapshot()
	_, err = empty.LCG214013Output15("absent")
	if !errors.Is(err, ErrNotFound) || !reflect.DeepEqual(before, empty.Snapshot()) {
		t.Fatal("missing draw created a stream")
	}
	check(t, empty.SetLCG214013Seed(strings.Repeat("n", 64), 1))
}

func TestLCG214013IsolationReseedAndRandomContinuation(t *testing.T) {
	r := NewRandom(123, 5)
	check(t, r.SetLCG214013Seed("a", 1))
	check(t, r.SetLCG214013Seed("b", 1))
	check(t, r.SetJavaSeed("java", 42))
	_, err := r.Uint64("xoshiro")
	check(t, err)
	before := r.Snapshot()
	for i := 0; i < 10; i++ {
		_, err = r.LCG214013Output15("a")
		check(t, err)
	}
	after := r.Snapshot()
	if !reflect.DeepEqual(before.Streams[1:], after.Streams[1:]) {
		t.Fatal("draw changed other streams")
	}
	check(t, r.SetLCG214013Seed("a", 1))
	if !reflect.DeepEqual(before, r.Snapshot()) {
		t.Fatal("reseed did not restore stream")
	}
	for i := 0; i < 7; i++ {
		_, err = r.LCG214013Output15("a")
		check(t, err)
	}
	clone := NewRandom(999, 1)
	check(t, clone.Restore(r.Snapshot()))
	for i := 0; i < 100; i++ {
		for _, name := range []string{"a", "b"} {
			a, e := r.LCG214013Output15(name)
			check(t, e)
			b, e := clone.LCG214013Output15(name)
			check(t, e)
			if a != b {
				t.Fatal("LCG continuation mismatch")
			}
		}
		a, e := r.JavaInt("java")
		check(t, e)
		b, e := clone.JavaInt("java")
		check(t, e)
		if a != b {
			t.Fatal("Java continuation mismatch")
		}
		x, e := r.Uint64("xoshiro")
		check(t, e)
		y, e := clone.Uint64("xoshiro")
		check(t, e)
		if x != y {
			t.Fatal("xoshiro continuation mismatch")
		}
	}
	if !reflect.DeepEqual(r.Snapshot(), clone.Snapshot()) {
		t.Fatal("continuation states differ")
	}
}

func TestLCG214013RestoreCanonicalAndRollback(t *testing.T) {
	r := NewRandom(12, 4)
	check(t, r.SetLCG214013Seed("a", 1))
	check(t, r.SetLCG214013Seed("z", 2))
	for _, state := range [][4]uint64{{0}, {math.MaxUint32}} {
		saved := r.Snapshot()
		saved.Streams[1].State = state
		check(t, r.Restore(saved))
		if !reflect.DeepEqual(saved, r.Snapshot()) {
			t.Fatal("canonical state not preserved")
		}
	}
	for i, bad := range [][4]uint64{{uint64(1) << 32}, {math.MaxUint64}, {0, 1}, {0, 0, 1}, {0, 0, 0, 1}} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			before := r.Snapshot()
			saved := r.Snapshot()
			saved.Seed++
			saved.Streams[0].State[0]++
			saved.Streams[1].State = bad
			if err := r.Restore(saved); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("restore error %v", err)
			}
			if !reflect.DeepEqual(before, r.Snapshot()) {
				t.Fatal("invalid restore partially committed")
			}
		})
	}
}

func TestLCG214013CannotUseImplicitInitializer(t *testing.T) {
	r := NewRandom(123, 1)
	before := r.Snapshot()
	stream, err := r.ensureStream("explicit-only", RNGLCG214013Output15)
	if stream != nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("implicit initializer returned %v, %v", stream, err)
	}
	if !reflect.DeepEqual(before, r.Snapshot()) {
		t.Fatal("implicit initializer mutated state")
	}
	check(t, r.SetLCG214013Seed("explicit-only", 1))
	other := NewRandom(987, 1)
	check(t, other.SetLCG214013Seed("different-name", 1))
	for i := 0; i < 100; i++ {
		a, err := r.LCG214013Output15("explicit-only")
		check(t, err)
		b, err := other.LCG214013Output15("different-name")
		check(t, err)
		if a != b {
			t.Fatal("explicit seed depends on root seed or stream name")
		}
	}
}
