package runtime_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	runtime "github.com/mirusu400/aram-core/runtime"
)

func lcgCheck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLCG214013ServicesBinaryContinuation(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint("mixed=", mixed), func(t *testing.T) {
			source, err := runtime.NewServices(runtime.Config{})
			lcgCheck(t, err)
			lcgCheck(t, source.Random.SetJavaSeed("java", 42))
			_, err = source.Random.Uint64("xoshiro")
			lcgCheck(t, err)
			if mixed {
				lcgCheck(t, source.Random.SetLCG214013Seed("lcg", math.MaxUint32))
				lcgCheck(t, source.Random.SetLCG214013Seed("zero", 0))
				for i := 0; i < 17; i++ {
					_, err = source.Random.LCG214013Output15("lcg")
					lcgCheck(t, err)
				}
			}
			encoded, err := source.MarshalBinary()
			lcgCheck(t, err)
			clone, err := runtime.NewServices(runtime.Config{})
			lcgCheck(t, err)
			lcgCheck(t, clone.UnmarshalBinary(encoded))
			if !reflect.DeepEqual(source.Snapshot(), clone.Snapshot()) {
				t.Fatal("binary state mismatch")
			}
			again, err := clone.MarshalBinary()
			lcgCheck(t, err)
			if !bytes.Equal(encoded, again) {
				t.Fatal("binary encoding not stable")
			}
			for i := 0; i < 100; i++ {
				if mixed {
					for _, name := range []string{"lcg", "zero"} {
						a, e := source.Random.LCG214013Output15(name)
						lcgCheck(t, e)
						b, e := clone.Random.LCG214013Output15(name)
						lcgCheck(t, e)
						if a != b {
							t.Fatal("LCG continuation mismatch")
						}
					}
				}
				a, e := source.Random.JavaInt("java")
				lcgCheck(t, e)
				b, e := clone.Random.JavaInt("java")
				lcgCheck(t, e)
				if a != b {
					t.Fatal("Java continuation mismatch")
				}
				x, e := source.Random.Uint64("xoshiro")
				lcgCheck(t, e)
				y, e := clone.Random.Uint64("xoshiro")
				lcgCheck(t, e)
				if x != y {
					t.Fatal("xoshiro continuation mismatch")
				}
			}
			if !reflect.DeepEqual(source.Snapshot(), clone.Snapshot()) {
				t.Fatal("continued services state mismatch")
			}
		})
	}
}

func TestLCG214013ServicesBinaryCanonicalRollback(t *testing.T) {
	source, err := runtime.NewServices(runtime.Config{})
	lcgCheck(t, err)
	lcgCheck(t, source.Random.SetJavaSeed("a-java", 42))
	lcgCheck(t, source.Random.SetLCG214013Seed("z-lcg", 0))
	encoded, err := source.MarshalBinary()
	lcgCheck(t, err)
	original := source.Random.Snapshot()
	payload, err := runtime.MarshalStateComponent(original)
	lcgCheck(t, err)
	// Replace only the synthetic random component, repairing both checksums so
	// rejection exercises semantic canonical validation, not integrity failure.
	if bytes.Count(encoded, payload) != 1 {
		t.Fatal("random payload not unique")
	}
	offset := bytes.Index(encoded, payload)
	target, err := runtime.NewServices(runtime.Config{})
	lcgCheck(t, err)
	lcgCheck(t, target.Random.SetLCG214013Seed("existing", 99))
	before := target.Snapshot()
	for i, bad := range [][4]uint64{{uint64(1) << 32}, {math.MaxUint64}, {0, 1}, {0, 0, 1}, {0, 0, 0, 1}} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			candidate := source.Random.Snapshot()
			candidate.Streams[1].State = bad
			replacement, e := runtime.MarshalStateComponent(candidate)
			lcgCheck(t, e)
			if len(replacement) != len(payload) {
				t.Fatal("unexpected payload size change")
			}
			corrupt := append([]byte(nil), encoded...)
			copy(corrupt[offset:], replacement)
			digest := sha256.Sum256(replacement)
			copy(corrupt[offset+len(replacement):], digest[:])
			digest = sha256.Sum256(corrupt[:len(corrupt)-sha256.Size])
			copy(corrupt[len(corrupt)-sha256.Size:], digest[:])
			if e = target.UnmarshalBinary(corrupt); !errors.Is(e, runtime.ErrInvalidState) {
				t.Fatalf("load error %v", e)
			}
			if !reflect.DeepEqual(before, target.Snapshot()) {
				t.Fatal("malformed canonical state partially committed services")
			}
		})
	}
	// A valid load after all failures proves the receiver remains usable.
	lcgCheck(t, target.UnmarshalBinary(encoded))
	if !reflect.DeepEqual(source.Snapshot(), target.Snapshot()) {
		t.Fatal("valid load after rejection failed")
	}
}
