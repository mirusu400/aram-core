package gvm_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/gvm"
	gruntime "github.com/mirusu400/aram-core/runtime"
)

func serviceClock(t *testing.T, epoch int64, offset int32, elapsed int64) *gruntime.Clock {
	t.Helper()
	c := new(gruntime.Clock)
	if err := c.Restore(gruntime.ClockState{WallEpochMillis: epoch, TimezoneOffsetMins: offset, MonotonicNanos: elapsed, Locale: "explicit-test"}); err != nil {
		t.Fatal(err)
	}
	return c
}

func serviceVM(t *testing.T, code []byte, space gvm.AddressSpace, config *gvm.ServiceConfig) *gvm.VM {
	t.Helper()
	v, err := gvm.NewWithAddressSpaceAndServices(code, 0, space, config)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func serviceWords(data []byte) []uint16 {
	words := make([]uint16, len(data)/2)
	for i := range words {
		words[i] = binary.LittleEndian.Uint16(data[2*i:])
	}
	return words
}

func TestServicesDeviceQuery(t *testing.T) {
	for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		for _, width := range []int32{1, 119, 120, 127, 128, 175, 176, 256} {
			for _, height := range []int32{1, 79, 80, 127, 128, 175, 176, 256} {
				for _, audio := range []int32{0, 1, 5, 6, 15, 16, 31, 32, 37, 38, 65541, -1, math.MinInt32, math.MaxInt32} {
					t.Run(fmt.Sprintf("r%d/%dx%d/a%d", region, width, height, audio), func(t *testing.T) {
						k := uint16(8)
						if width < 120 || height < 80 {
							k = 1
						} else if width < 128 || height < 128 {
							k = 2
						} else if width < 176 || height < 176 {
							k = 4
						}
						mask := uint16(uint64(1) << uint32(audio&31))
						if audio == 5 {
							mask = 0x24
						}
						if audio == 6 {
							mask = 0x64
						}
						expected := []uint16{0x9797, k, 4, mask, 1, 0x9797}
						profile := gvm.DeviceQueryProfile{Width: width, Height: height, AudioType: audio}
						ref := uint16(1)
						if region == gvm.AddressFile {
							ref |= 0x4000
						}
						code := []byte{6, byte(ref >> 8), byte(ref), 0x51, 0xff}
						start := len(code)
						payload := bytes.Repeat([]byte{0x97}, 12)
						code = append(code, payload...)
						v := serviceVM(t, code, gvm.AddressSpace{FileStart: uint32(start), FileLength: 12, RAM: payload, Symbols: []gvm.AddressSymbol{
							{Region: region, Offset: 2, Length: 2}, {Region: region, Length: 12},
						}}, &gvm.ServiceConfig{DeviceQuery: &profile})
						// Supplied profile is copied, never guest-addressable or retained by alias.
						profile.Width = 1
						profile.Height = 1
						profile.AudioType = 0
						if err := v.Run(2); err != gvm.ErrBudget || v.PC() != 4 || len(v.Stack()) != 0 || v.Halted() {
							t.Fatalf("query: %v pc%d", err, v.PC())
						}
						got, _ := v.Symbol(1)
						if !reflect.DeepEqual(serviceWords(got), expected) {
							t.Fatalf("got%x want%x", serviceWords(got), expected)
						}
						if err := v.Step(); err != nil || !v.Halted() {
							t.Fatalf("halt: %v", err)
						}
					})
				}
			}
		}
	}
}

func TestServicesConfigValidation(t *testing.T) {
	clock := serviceClock(t, 0, 0, 0)
	cases := []*gvm.ServiceConfig{
		{DeviceQuery: &gvm.DeviceQueryProfile{Width: 0, Height: 1}},
		{DeviceQuery: &gvm.DeviceQueryProfile{Width: -1, Height: 1}},
		{DeviceQuery: &gvm.DeviceQueryProfile{Width: 257, Height: 1}},
		{DeviceQuery: &gvm.DeviceQueryProfile{Width: math.MinInt32, Height: 1}},
		{DeviceQuery: &gvm.DeviceQueryProfile{Width: math.MaxInt32, Height: 1}},
		{DeviceQuery: &gvm.DeviceQueryProfile{Width: 1, Height: 0}},
		{DeviceQuery: &gvm.DeviceQueryProfile{Width: 1, Height: -1}},
		{DeviceQuery: &gvm.DeviceQueryProfile{Width: 1, Height: 257}},
		{Clock: clock}, {ClockPolicy: gvm.FixedOffsetNoDST},
		{Clock: clock, ClockPolicy: gvm.CivilTimePolicy(2)}, {ClockPolicy: gvm.CivilTimePolicy(255)},
	}
	for i, config := range cases {
		before := clock.Snapshot()
		v, err := gvm.NewWithAddressSpaceAndServices([]byte{0xff}, 0, gvm.AddressSpace{}, config)
		if v != nil || !errors.Is(err, gvm.ErrInvalidServiceConfig) {
			t.Fatalf("case%d: vm%v err%v", i, v, err)
		}
		if clock.Snapshot() != before {
			t.Fatal("constructor changed clock")
		}
	}
	for _, config := range []*gvm.ServiceConfig{nil, {}, {Clock: clock, ClockPolicy: gvm.FixedOffsetNoDST}, {DeviceQuery: &gvm.DeviceQueryProfile{Width: 1, Height: 256, AudioType: math.MinInt32}}} {
		if _, err := gvm.NewWithAddressSpaceAndServices([]byte{0xff}, 0, gvm.AddressSpace{}, config); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServicesClockValues(t *testing.T) {
	const maxMillis = int64(2147483647999)
	cases := []struct {
		name    string
		epoch   int64
		offset  int32
		elapsed int64
		want    []uint16
	}{
		{"epoch-zero", 0, 0, 0, []uint16{0, 0, 0, 0}},
		{"last-millisecond", 86399999, 0, 0, []uint16{23, 59, 59, 999}},
		{"next-day", 86400000, 0, 0, []uint16{0, 0, 0, 0}},
		{"positive-offset", 0, 540, 123456789, []uint16{9, 0, 0, 123}},
		{"negative-offset", 3600000, -60, 999999999, []uint16{0, 0, 0, 999}},
		{"elapsed-carry", 59999, 0, 1000000, []uint16{0, 1, 0, 0}},
		{"upper-end", maxMillis, 0, 0, []uint16{3, 14, 7, 999}},
	}
	for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("r%d/%s", region, tc.name), func(t *testing.T) {
				clock := serviceClock(t, tc.epoch, tc.offset, tc.elapsed)
				before := clock.Snapshot()
				ref := uint16(0)
				if region == gvm.AddressFile {
					ref = 0x4000
				}
				code := []byte{6, byte(ref >> 8), byte(ref), 0xb9, 0xff}
				start := len(code)
				code = append(code, bytes.Repeat([]byte{0xa7}, 8)...)
				v := serviceVM(t, code, gvm.AddressSpace{FileStart: uint32(start), FileLength: 8, RAM: code[start:], Symbols: []gvm.AddressSymbol{{Region: region, Length: 8}}}, &gvm.ServiceConfig{Clock: clock, ClockPolicy: gvm.FixedOffsetNoDST})
				if err := v.Run(2); err != gvm.ErrBudget || v.PC() != 4 || len(v.Stack()) != 0 {
					t.Fatalf("query: %v", err)
				}
				got, _ := v.Symbol(0)
				if !reflect.DeepEqual(serviceWords(got), tc.want) {
					t.Fatalf("got%v want%v", serviceWords(got), tc.want)
				}
				if clock.Snapshot() != before {
					t.Fatal("query mutated clock")
				}
			})
		}
	}
}

func TestServicesClockSharedAdvance(t *testing.T) {
	clock := serviceClock(t, 86399999, 0, 0)
	code := []byte{6, 0, 0, 0xb9, 6, 0, 0, 0xb9, 6, 0, 0, 0xb9, 0xff}
	v := serviceVM(t, code, gvm.AddressSpace{RAM: make([]byte, 8), Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 8}}}, &gvm.ServiceConfig{Clock: clock, ClockPolicy: gvm.FixedOffsetNoDST})
	before := clock.Snapshot()
	for i := 0; i < 2; i++ {
		if err := v.Run(2); err != gvm.ErrBudget {
			t.Fatal(err)
		}
		got, _ := v.Symbol(0)
		if !reflect.DeepEqual(serviceWords(got), []uint16{23, 59, 59, 999}) {
			t.Fatalf("unadvanced query%v", serviceWords(got))
		}
		if clock.Snapshot() != before {
			t.Fatal("query advanced time")
		}
	}
	if err := clock.Advance(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	advanced := clock.Snapshot()
	if err := v.Run(2); err != gvm.ErrBudget {
		t.Fatal(err)
	}
	got, _ := v.Symbol(0)
	if !reflect.DeepEqual(serviceWords(got), []uint16{0, 0, 0, 0}) || clock.Snapshot() != advanced {
		t.Fatal("did not borrow authoritative clock")
	}
}

func TestServicesAvailabilityIndependentOfClockRange(t *testing.T) {
	clock := serviceClock(t, -1, 0, 0)
	before := clock.Snapshot()
	code := []byte{6, 0, 0, 0x51, 6, 0, 0, 0xb9}
	v := serviceVM(t, code, gvm.AddressSpace{RAM: make([]byte, 8), Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 8}}}, &gvm.ServiceConfig{
		DeviceQuery: &gvm.DeviceQueryProfile{Width: 128, Height: 128, AudioType: 65542},
		Clock:       clock, ClockPolicy: gvm.FixedOffsetNoDST,
	})
	if err := v.Run(2); err != gvm.ErrBudget {
		t.Fatalf("51 must not require clock conversion: %v", err)
	}
	output, _ := v.Symbol(0)
	if !reflect.DeepEqual(serviceWords(output), []uint16{4, 4, 64, 1}) {
		t.Fatalf("full32 AudioType: %x", output)
	}
	if err := v.Run(2); !errors.Is(err, gvm.ErrClockRange) {
		t.Fatalf("b9 must independently validate clock: %v", err)
	}
	after, _ := v.Symbol(0)
	if !bytes.Equal(output, after) || clock.Snapshot() != before {
		t.Fatal("failed b9 changed successful51 output or clock")
	}
}

func TestServicesFaultsAndAvailability(t *testing.T) {
	for _, op := range []byte{0x51, 0xb9} {
		unavailable := gvm.ErrDeviceQueryUnavailable
		if op == 0xb9 {
			unavailable = gvm.ErrClockUnavailable
		}
		valid := &gvm.ServiceConfig{DeviceQuery: &gvm.DeviceQueryProfile{Width: 176, Height: 176, AudioType: 6}, Clock: serviceClock(t, 0, 0, 0), ClockPolicy: gvm.FixedOffsetNoDST}
		opposite := &gvm.ServiceConfig{Clock: valid.Clock, ClockPolicy: gvm.FixedOffsetNoDST}
		if op == 0xb9 {
			opposite = &gvm.ServiceConfig{DeviceQuery: valid.DeviceQuery}
		}
		cases := []struct {
			name   string
			push   bool
			ref    uint16
			size   int
			config *gvm.ServiceConfig
			want   error
		}{
			{"underflow-before-all", false, 0x8000, 0, nil, gvm.ErrStackUnderflow},
			{"address-before-service", true, 0x8000, 8, nil, gvm.ErrInvalidAddress},
			{"negative-file", true, 0xc000, 8, valid, gvm.ErrInvalidAddress},
			{"missing-file", true, 0x4000, 8, valid, gvm.ErrInvalidAddress},
			{"empty-arena", true, 0, 0, valid, gvm.ErrInvalidAddress},
			{"only-two", true, 0, 2, valid, gvm.ErrInvalidAddress},
			{"only-four", true, 0, 4, valid, gvm.ErrInvalidAddress},
			{"only-six", true, 0, 6, valid, gvm.ErrInvalidAddress},
			{"odd-tail", true, 0, 7, valid, gvm.ErrInvalidAddress},
			{"past-end", true, 1, 8, valid, gvm.ErrInvalidAddress},
			{"unavailable", true, 0, 8, nil, unavailable},
			{"independent-services", true, 0, 8, opposite, unavailable},
		}
		for _, tc := range cases {
			t.Run(fmt.Sprintf("op%x/%s", op, tc.name), func(t *testing.T) {
				code := []byte{}
				if tc.push {
					code = append(code, 6, byte(tc.ref>>8), byte(tc.ref))
				}
				code = append(code, op, 0xff)
				payload := bytes.Repeat([]byte{0xa7}, tc.size)
				v := serviceVM(t, code, gvm.AddressSpace{RAM: payload, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: uint32(tc.size)}}}, tc.config)
				if tc.push {
					if err := v.Step(); err != nil {
						t.Fatal(err)
					}
				}
				stack := v.Stack()
				pc := v.PC()
				clockBefore := valid.Clock.Snapshot()
				err := v.Step()
				var fault *gvm.ExecutionError
				if !errors.Is(err, tc.want) || !errors.As(err, &fault) || fault.Offset != pc || v.PC() != pc+1 || v.Halted() || !reflect.DeepEqual(v.Stack(), stack) {
					t.Fatalf("fault: %v pc%d", err, v.PC())
				}
				if v.Step() != err || v.Run(0) != err || v.Run(99) != err {
					t.Fatal("nonsticky fault")
				}
				got, _ := v.Symbol(0)
				if !bytes.Equal(got, payload) || !bytes.Equal(v.Program(), code) || !reflect.DeepEqual(v.Stack(), stack) || valid.Clock.Snapshot() != clockBefore {
					t.Fatal("fault changed state")
				}
			})
		}
	}
}

func TestServicesClockRangeAtomicity(t *testing.T) {
	for _, tc := range []struct {
		epoch  int64
		offset int32
	}{{-1, 0}, {0, -1}, {2147483648000, 0}, {2147483647999, 1}, {math.MinInt64, 0}, {math.MaxInt64, 0}} {
		clock := serviceClock(t, tc.epoch, tc.offset, 0)
		before := clock.Snapshot()
		code := []byte{6, 0, 0, 0xb9, 0xff}
		payload := bytes.Repeat([]byte{0xa7}, 8)
		v := serviceVM(t, code, gvm.AddressSpace{RAM: payload, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressRAM, Length: 8}}}, &gvm.ServiceConfig{Clock: clock, ClockPolicy: gvm.FixedOffsetNoDST})
		if err := v.Step(); err != nil {
			t.Fatal(err)
		}
		err := v.Step()
		if !errors.Is(err, gvm.ErrClockRange) || v.PC() != 4 || !reflect.DeepEqual(v.Stack(), []uint16{0}) || v.Step() != err || v.Run(0) != err {
			t.Fatalf("range epoch%d offset%d: %v", tc.epoch, tc.offset, err)
		}
		got, _ := v.Symbol(0)
		if !bytes.Equal(got, payload) || !bytes.Equal(v.Program(), code) || clock.Snapshot() != before {
			t.Fatal("range fault changed guest or clock")
		}
	}
}

func TestServicesLegacyUnsupported(t *testing.T) {
	for _, op := range []byte{0x51, 0xb9} {
		for _, push := range []bool{false, true} {
			code := []byte{}
			if push {
				code = append(code, 6, 0, 0)
			}
			code = append(code, op)
			at, err := gvm.NewAt(code, 0)
			if err != nil {
				t.Fatal(err)
			}
			symbols, err := gvm.NewWithSymbols(code, 0, []gvm.SymbolRegion{{Length: 8, Initial: make([]byte, 8)}})
			if err != nil {
				t.Fatal(err)
			}
			address := addressVM(t, code, gvm.AddressSpace{RAM: make([]byte, 8)})
			for _, v := range []*gvm.VM{gvm.New(code), at, symbols, address} {
				if push {
					if err := v.Step(); err != nil {
						t.Fatal(err)
					}
				}
				pc := v.PC()
				stack := v.Stack()
				err := v.Step()
				var unsupported *gvm.UnsupportedOpcodeError
				if !errors.As(err, &unsupported) || unsupported.Opcode != op || unsupported.Offset != pc || v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), stack) || v.Run(0) != err {
					t.Fatalf("legacy: %v", err)
				}
			}
		}
	}
}

func TestServicesArenaBounds(t *testing.T) {
	for _, op := range []byte{0x51, 0xb9} {
		for _, region := range []gvm.AddressRegion{gvm.AddressFile, gvm.AddressRAM} {
			for _, size := range []int{0, 2, 4, 6, 7, 8, 9} {
				t.Run(fmt.Sprintf("op%x/r%d/size%d", op, region, size), func(t *testing.T) {
					ref := uint16(0)
					if region == gvm.AddressFile {
						ref = 0x4000
					}
					code := []byte{6, byte(ref >> 8), byte(ref), op, 0xff}
					start := len(code)
					payload := bytes.Repeat([]byte{0xa7}, size)
					code = append(code, payload...)
					clock := serviceClock(t, 0, 0, 0)
					before := clock.Snapshot()
					config := &gvm.ServiceConfig{DeviceQuery: &gvm.DeviceQueryProfile{Width: 1, Height: 1}, Clock: clock, ClockPolicy: gvm.FixedOffsetNoDST}
					v := serviceVM(t, code, gvm.AddressSpace{FileStart: uint32(start), FileLength: uint32(size), RAM: payload, Symbols: []gvm.AddressSymbol{{Region: region, Length: uint32(size)}}}, config)
					if err := v.Step(); err != nil {
						t.Fatal(err)
					}
					err := v.Step()
					got, _ := v.Symbol(0)
					if size < 8 {
						if !errors.Is(err, gvm.ErrInvalidAddress) || v.PC() != 4 || !reflect.DeepEqual(v.Stack(), []uint16{ref}) || v.Step() != err || !bytes.Equal(got, payload) || !bytes.Equal(v.Program(), code) {
							t.Fatalf("bounds failure: %v", err)
						}
					} else {
						expected := []uint16{1, 4, 1, 1}
						if op == 0xb9 {
							expected = []uint16{0, 0, 0, 0}
						}
						if err != nil || !reflect.DeepEqual(serviceWords(got[:8]), expected) || len(v.Stack()) != 0 {
							t.Fatalf("exact bounds: %v got%x", err, got)
						}
						if size == 9 && got[8] != 0xa7 {
							t.Fatal("odd tail overwritten")
						}
					}
					if clock.Snapshot() != before {
						t.Fatal("clock mutated")
					}
				})
			}
		}
	}
}

func TestServicesInstructionAliases(t *testing.T) {
	for _, op := range []byte{0x51, 0xb9} {
		clock := serviceClock(t, 0, 0, 0)
		code := []byte{6, 0x40, 0, op, 0xa7, 0xa7, 0xa7, 0xa7, 0xff}
		config := &gvm.ServiceConfig{DeviceQuery: &gvm.DeviceQueryProfile{Width: 1, Height: 1}, Clock: clock, ClockPolicy: gvm.FixedOffsetNoDST}
		v := serviceVM(t, code, gvm.AddressSpace{FileLength: 8, Symbols: []gvm.AddressSymbol{{Region: gvm.AddressFile, Length: 2}, {Region: gvm.AddressFile, Length: 8}}}, config)
		// Config pointer fields are also copied; only the explicit clock object is shared.
		config.DeviceQuery = nil
		config.Clock = nil
		config.ClockPolicy = 0
		if err := v.Run(2); err != gvm.ErrBudget || v.PC() != 4 || len(v.Stack()) != 0 {
			t.Fatalf("alias query: %v", err)
		}
		got, _ := v.Symbol(1)
		expected := []uint16{1, 4, 1, 1}
		if op == 0xb9 {
			expected = []uint16{0, 0, 0, 0}
		}
		if !reflect.DeepEqual(serviceWords(got), expected) {
			t.Fatalf("alias contents%x", got)
		}
		if op == 0x51 {
			err := v.Step()
			var unsupported *gvm.UnsupportedOpcodeError
			if !errors.As(err, &unsupported) || unsupported.Opcode != 1 || unsupported.Offset != 4 {
				t.Fatalf("changed future fetch: %v", err)
			}
		} else if err := v.Run(5); err != nil || !v.Halted() || v.PC() != 9 {
			t.Fatalf("changed NOPs and halt: %v", err)
		}
	}
}

func TestServicesStackBudgetAndReturns(t *testing.T) {
	for _, op := range []byte{0x51, 0xb9} {
		clock := serviceClock(t, 0, 0, 0)
		config := &gvm.ServiceConfig{DeviceQuery: &gvm.DeviceQueryProfile{Width: 1, Height: 1}, Clock: clock, ClockPolicy: gvm.FixedOffsetNoDST}
		code := []byte{0x44, 0, 4, 0xff}
		prefix := make([]uint16, 64)
		for i := range prefix {
			prefix[i] = uint16(0x8000 + i)
			code = append(code, 6, byte(prefix[i]>>8), byte(prefix[i]))
		}
		code = append(code, 6, 0, 0, op, 0x45)
		v := serviceVM(t, code, gvm.AddressSpace{RAM: make([]byte, 8)}, config)
		if err := v.Run(66); err != gvm.ErrBudget || len(v.Stack()) != 65 {
			t.Fatal(err)
		}
		pc := v.PC()
		state := clock.Snapshot()
		if err := v.Run(0); err != gvm.ErrBudget || v.PC() != pc || clock.Snapshot() != state {
			t.Fatal("zero budget did work")
		}
		if err := v.Step(); err != nil || v.PC() != pc+1 || !reflect.DeepEqual(v.Stack(), prefix) {
			t.Fatalf("full stack query: %v", err)
		}
		if err := v.Run(2); err != nil || !v.Halted() || v.PC() != 4 || !reflect.DeepEqual(v.Stack(), prefix) || clock.Snapshot() != state {
			t.Fatalf("return/halt: %v", err)
		}
	}
}
