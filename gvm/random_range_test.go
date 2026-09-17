package gvm

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	gruntime "github.com/mirusu400/aram-core/runtime"
)

func a1Program(values ...uint16) []byte {
	var code []byte
	for _, v := range values {
		code = append(code, 6, byte(v>>8), byte(v))
	}
	return append(code, 0xa1, 0xff)
}
func a1VM(t *testing.T, code []byte, config *ServiceConfig) *VM {
	t.Helper()
	v, err := NewWithAddressSpaceAndServices(code, 0, AddressSpace{RAM: []byte{0x12, 0x34}, Symbols: []AddressSymbol{{Region: AddressRAM, Length: 2}}}, config)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func a1Random(t *testing.T, seed uint32) *gruntime.Random {
	t.Helper()
	r := gruntime.NewRandom(987, 4)
	if err := r.SetLCG214013Seed("range", seed); err != nil {
		t.Fatal(err)
	}
	if err := r.SetJavaSeed("z-unrelated", 99); err != nil {
		t.Fatal(err)
	}
	return r
}
func a1Steps(t *testing.T, v *VM, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := v.Step(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRandomRangeOptInEqual(t *testing.T) {
	for _, raw := range []uint16{0, 1, 0x7fff, 0x8000, 0xffff} {
		v := a1VM(t, a1Program(raw, raw), nil)
		if err := v.Run(3); err != ErrBudget {
			t.Fatalf("equal opt-in: %v", err)
		}
		if !reflect.DeepEqual(v.Stack(), []uint16{raw}) || v.stack[1] != 0 {
			t.Fatalf("stack %x backing %x", v.Stack(), v.stack)
		}
	}
}

func TestRandomRangeOutputBoundaries(t *testing.T) {
	// Invert the odd multiplier modulo 2^32 to arrange exact next outputs.
	// This fixture changes only public seeded runtime state, never VM internals.
	inv := uint32(1)
	for i := 0; i < 5; i++ {
		inv *= 2 - 214013*inv
	}
	for _, draw := range []uint32{0, 1, 32766, 32767} {
		for _, width := range []int32{1, 2, 3, 255, 32767, 32768, 32769, 65535} {
			for _, reversed := range []bool{false, true} {
				seed := ((draw << 16) - uint32(2531011)) * inv
				r := a1Random(t, seed)
				lo, hi := int32(-32768), int32(-32768)+width
				a, b := uint16(lo), uint16(hi)
				if reversed {
					a, b = b, a
				}
				// A1 at the final byte succeeds without an inline operand fetch.
				code := a1Program(a, b)
				v := a1VM(t, code[:len(code)-1], &ServiceConfig{Random: r, RandomStream: "range"})
				if err := v.Run(3); err != ErrBudget {
					t.Fatal(err)
				}
				want := uint16(lo + int32(draw)%width)
				state := r.Snapshot().Streams[0]
				if v.Stack()[0] != want || v.stack[1] != 0 || state.Draws != 1 || state.State[0] != uint64(draw<<16) {
					t.Fatalf("draw%d width%d reversed%v: stack%x state%#v", draw, width, reversed, v.Stack(), state)
				}
				before := r.Snapshot()
				if err := v.Step(); !errors.Is(err, ErrTruncated) || !reflect.DeepEqual(before, r.Snapshot()) {
					t.Fatal("post-A1 fetch changed random state")
				}
			}
		}
	}
}
func TestRandomRangeLegacy(t *testing.T) {
	for _, values := range [][]uint16{nil, {1}, {0x8000, 0x8000}, {0xffff, 0x7fff}} {
		code := a1Program(values...)
		at, err := NewAt(code, 0)
		if err != nil {
			t.Fatal(err)
		}
		sy, err := NewWithSymbols(code, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		ad, err := NewWithAddressSpace(code, 0, AddressSpace{})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []*VM{New(code), at, sy, ad} {
			a1Steps(t, v, len(values))
			before := *v
			err := v.Step()
			var unsupported *UnsupportedOpcodeError
			if !errors.As(err, &unsupported) || unsupported.Opcode != 0xa1 || unsupported.Offset != len(values)*3 {
				t.Fatalf("legacy %v", err)
			}
			before.pc++
			before.fault = err
			if !reflect.DeepEqual(&before, v) {
				t.Fatal("legacy mutated state")
			}
			if v.Step() != err || v.Run(0) != err {
				t.Fatal("legacy fault not sticky")
			}
		}
	}
}
func TestRandomRangeSignedDraws(t *testing.T) {
	pairs := [][2]uint16{{0, 1}, {1, 0}, {0xffff, 0}, {0, 0xffff}, {0x8000, 0x7fff}, {0x7fff, 0x8000}, {0x8000, 0xffff}, {0xffff, 0x8000}, {0x7ffe, 0x7fff}, {0x8000, 0x8001}, {0xff80, 0x7f}, {0x7fff, 0x7fff}, {0x8000, 0x8000}}
	for _, depth := range []int{2, 65} {
		for _, pair := range pairs {
			for _, seed := range []uint32{0, 1, 0xffffffff, 0x80000000, 0x12345678} {
				t.Run(fmt.Sprintf("d%d/%x/%x", depth, pair, seed), func(t *testing.T) {
					r := a1Random(t, seed)
					values := make([]uint16, depth)
					for i := range values {
						values[i] = uint16(0x1100 + i)
					}
					values[depth-2], values[depth-1] = pair[0], pair[1]
					v := a1VM(t, a1Program(values...), &ServiceConfig{Random: r, RandomStream: "range"})
					initial := r.Snapshot()
					if err := v.Run(0); err != ErrBudget || v.PC() != 0 {
						t.Fatal("zero budget")
					}
					for range values {
						a1Steps(t, v, 1)
						if !reflect.DeepEqual(initial, r.Snapshot()) {
							t.Fatal("push drew")
						}
					}
					before := *v
					expected := initial
					want := pair[0]
					if pair[0] != pair[1] {
						s := seed*214013 + 2531011
						draw := (s >> 16) & 0x7fff
						lo, hi := int32(int16(pair[0])), int32(int16(pair[1]))
						if lo > hi {
							lo, hi = hi, lo
						}
						want = uint16(lo + int32(draw)%(hi-lo))
						expected.Streams = append([]gruntime.RNGStreamState(nil), initial.Streams...)
						expected.Streams[0].State = [4]uint64{uint64(s)}
						expected.Streams[0].Draws++
					}
					if err := v.Run(1); err != ErrBudget {
						t.Fatal(err)
					}
					before.pc++
					before.depth--
					before.stack[depth-2] = want
					before.stack[depth-1] = 0
					if !reflect.DeepEqual(&before, v) || !reflect.DeepEqual(expected, r.Snapshot()) {
						t.Fatalf("result/state want %x got %x RNG %#v", want, v.Stack(), r.Snapshot())
					}
					a1Steps(t, v, 1)
					halt := *v
					if !v.Halted() || v.Run(0) != nil || v.Step() != nil || !reflect.DeepEqual(&halt, v) || !reflect.DeepEqual(expected, r.Snapshot()) {
						t.Fatal("halt not inert")
					}
				})
			}
		}
	}
}

// Bytecode creates actual nested return frames and a saved-top marker before A1.
func a1Nested(values ...uint16) []byte {
	code := []byte{0x44, 0, 4, 0xff, 0x44, 0, 8, 0x45}
	pushes := a1Program(values...)
	code = append(code, pushes[:len(pushes)-2]...)
	return append(code, 0x0b, 0xa1, 0x45)
}
func TestRandomRangeFailuresTransactional(t *testing.T) {
	for _, kind := range []string{"provider", "absent", "wrong", "exhausted"} {
		for _, depth := range []int{0, 1, 2, 65} {
			t.Run(fmt.Sprintf("%s/d%d", kind, depth), func(t *testing.T) {
				r := a1Random(t, 1)
				var config *ServiceConfig
				cause := ErrRandomUnavailable
				if kind != "provider" {
					config = &ServiceConfig{Random: r, RandomStream: "range"}
					state := r.Snapshot()
					switch kind {
					case "absent":
						state.Streams = nil
						cause = gruntime.ErrNotFound
					case "wrong":
						state.Streams[0].Algorithm = gruntime.RNGJava48
						cause = gruntime.ErrInvalidState
					case "exhausted":
						state.Streams[0].Draws = math.MaxUint64
						cause = gruntime.ErrLimitExceeded
					}
					if err := r.Restore(state); err != nil {
						t.Fatal(err)
					}
				}
				values := make([]uint16, depth)
				if depth > 0 {
					values[depth-1] = 1
				}
				v := a1VM(t, a1Nested(values...), config)
				a1Steps(t, v, depth+3)
				before := *v
				memory := append([]byte(nil), v.address.ram...)
				program := append([]byte(nil), v.code...)
				rng := r.Snapshot()
				offset := v.PC()
				err := v.Step()
				if depth < 2 {
					cause = ErrStackUnderflow
				}
				var execution *ExecutionError
				if !errors.As(err, &execution) || execution.Offset != offset || !errors.Is(err, cause) {
					t.Fatalf("fault got %v want %v", err, cause)
				}
				before.pc++
				before.fault = err
				if !reflect.DeepEqual(&before, v) || !reflect.DeepEqual(program, v.code) || !reflect.DeepEqual(memory, v.address.ram) || !reflect.DeepEqual(rng, r.Snapshot()) {
					t.Fatal("failed draw mutated VM, hidden saved/return state, memory or RNG")
				}
				if v.Step() != err || v.Run(100) != err || v.Run(0) != err || !reflect.DeepEqual(&before, v) || !reflect.DeepEqual(rng, r.Snapshot()) {
					t.Fatal("fault not inert")
				}
			})
		}
	}
}
func TestRandomRangeEqualSkipsInvalidStream(t *testing.T) {
	for _, kind := range []string{"absent", "wrong", "exhausted"} {
		r := a1Random(t, 3)
		state := r.Snapshot()
		switch kind {
		case "absent":
			state.Streams = nil
		case "wrong":
			state.Streams[0].Algorithm = gruntime.RNGJava48
		case "exhausted":
			state.Streams[0].Draws = math.MaxUint64
		}
		if err := r.Restore(state); err != nil {
			t.Fatal(err)
		}
		v := a1VM(t, a1Nested(0xffff, 0xffff), &ServiceConfig{Random: r, RandomStream: "range"})
		a1Steps(t, v, 5)
		before := *v
		a1Steps(t, v, 1)
		before.pc++
		before.depth--
		before.stack[1] = 0
		if !reflect.DeepEqual(&before, v) || !reflect.DeepEqual(state, r.Snapshot()) {
			t.Fatal("equal touched stream or hidden state")
		}
		a1Steps(t, v, 2)
		if v.PC() != 3 || v.returnDepth != 0 {
			t.Fatal("nested returns corrupted")
		}
		a1Steps(t, v, 1)
		if !v.Halted() {
			t.Fatal("halt")
		}
	}
}
func TestRandomRangeConfigOwnership(t *testing.T) {
	r := a1Random(t, 7)
	before := r.Snapshot()
	invalid := []*ServiceConfig{{Random: r}, {RandomStream: "range"}, {Random: r, RandomStream: " \t\n"}, {Random: r, RandomStream: strings.Repeat("a", 65)}, {Random: r, RandomStream: "a\x00b"}, {Random: r, RandomStream: strings.Repeat("é", 33)}}
	for _, c := range invalid {
		v, err := NewWithAddressSpaceAndServices([]byte{0xff}, 0, AddressSpace{}, c)
		if v != nil || !errors.Is(err, ErrInvalidServiceConfig) || !reflect.DeepEqual(before, r.Snapshot()) {
			t.Fatalf("invalid config %v", err)
		}
	}
	for _, name := range []string{"range", " missing ", strings.Repeat("a", 64), strings.Repeat("é", 32)} {
		v, err := NewWithAddressSpaceAndServices([]byte{0xff}, 0, AddressSpace{}, &ServiceConfig{Random: r, RandomStream: name})
		if err != nil || v == nil || !reflect.DeepEqual(before, r.Snapshot()) {
			t.Fatalf("valid name %q: %v", name, err)
		}
	}
	config := &ServiceConfig{Random: r, RandomStream: "range"}
	v := a1VM(t, a1Program(0, 100), config)
	config.Random = nil
	config.RandomStream = "changed"
	if err := v.Run(4); err != nil || r.Snapshot().Streams[0].Draws != 1 {
		t.Fatalf("config not copied: %v", err)
	}
}
func TestRandomRangeBorrowedRestoreAndServices(t *testing.T) {
	r := a1Random(t, 1)
	clock := new(gruntime.Clock)
	if err := clock.Restore(gruntime.ClockState{Locale: "test"}); err != nil {
		t.Fatal(err)
	}
	profile := DeviceQueryProfile{Width: 120, Height: 128, AudioType: 5}
	config := &ServiceConfig{Random: r, RandomStream: "range", Clock: clock, ClockPolicy: FixedOffsetNoDST, DeviceQuery: &profile}
	// Two unequal draws with an owner Restore between them, then real returns.
	code := a1Nested(0x8000, 0x7fff)
	code = append(code[:len(code)-1], 6, 0x7f, 0xff, 0xa1, 0x45)
	v := a1VM(t, code, config)
	a1Steps(t, v, 5)
	clockBefore := clock.Snapshot()
	profileBefore := *v.services.deviceQuery
	a1Steps(t, v, 1)
	state := r.Snapshot()
	state.Streams[0].State = [4]uint64{0}
	state.Streams[0].Draws = 12
	if err := r.Restore(state); err != nil {
		t.Fatal(err)
	}
	lower := int32(int16(v.Stack()[0]))
	a1Steps(t, v, 2)
	want := uint16(lower + 38%(32767-lower))
	if v.Stack()[0] != want || r.Snapshot().Streams[0].Draws != 13 || r.Snapshot().Streams[0].State[0] != 2531011 {
		t.Fatal("did not use restored borrowed state")
	}
	if clock.Snapshot() != clockBefore || *v.services.deviceQuery != profileBefore {
		t.Fatal("unrelated services changed")
	}
	a1Steps(t, v, 3)
	if !v.Halted() || v.returnDepth != 0 {
		t.Fatal("actual nested returns")
	}
	// A VM already constructed must revalidate the named stream after Restore.
	for _, kind := range []string{"absent", "wrong", "exhausted"} {
		r := a1Random(t, 1)
		v := a1VM(t, a1Program(0, 1), &ServiceConfig{Random: r, RandomStream: "range"})
		state := r.Snapshot()
		cause := gruntime.ErrInvalidState
		switch kind {
		case "absent":
			state.Streams = nil
			cause = gruntime.ErrNotFound
		case "wrong":
			state.Streams[0].Algorithm = gruntime.RNGJava48
		case "exhausted":
			state.Streams[0].Draws = math.MaxUint64
			cause = gruntime.ErrLimitExceeded
		}
		if err := r.Restore(state); err != nil {
			t.Fatal(err)
		}
		if err := v.Run(3); !errors.Is(err, cause) || !reflect.DeepEqual(state, r.Snapshot()) {
			t.Fatalf("restore revalidation: %v", err)
		}
	}
}
