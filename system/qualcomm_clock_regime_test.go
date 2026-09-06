package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestQualcommClockRegimeLatchesAlignedWords(t *testing.T) {
	device := NewQualcommClockRegime()
	for _, access := range []struct {
		offset uint32
		value  uint32
	}{{0x0404, 0x2e}, {0x0604, 0x55}, {0x1428, 0x15e}, {0x1870, 0x20}, {0x262c, 0x440}, {0x2954, 0}, {0x4878, 0}, {0x4d08, 0xffffffff}, {0x5054, 0xfd3a}, {0x5814, 0x12345678}} {
		check(t, device.Write(access.offset, Width32, access.value))
		value, err := device.Read(access.offset, Width32)
		if err != nil || value != access.value {
			t.Fatalf("register %#x = %#x error %v", access.offset, value, err)
		}
	}
	if _, err := device.Read(2, Width32); !errors.Is(err, ErrQualcommClockRegimeMMIO) {
		t.Fatalf("unaligned read error = %v", err)
	}
	if err := device.Write(0x5000, Width16, 0); !errors.Is(err, ErrQualcommClockRegimeMMIO) {
		t.Fatalf("wrong-width write error = %v", err)
	}
	if _, err := device.Read(0x3000, Width32); !errors.Is(err, ErrQualcommClockRegimeMMIO) {
		t.Fatalf("reserved-gap read error = %v", err)
	}
	if _, err := device.Read(QualcommClockRegimeWindowSize, Width32); !errors.Is(err, ErrQualcommClockRegimeMMIO) {
		t.Fatalf("out-of-range read error = %v", err)
	}
}

func TestQualcommClockRegimeStateRoundTripAndReset(t *testing.T) {
	device := NewQualcommClockRegime()
	_ = device.Write(0x5054, Width32, 0xfd3a)
	state, err := device.SaveState()
	check(t, err)
	restored := NewQualcommClockRegime()
	check(t, restored.LoadState(state))
	value, _ := restored.Read(0x5054, Width32)
	if value != 0xfd3a {
		t.Fatalf("restored clock register = %#x", value)
	}
	if err := restored.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated state error = %v", err)
	}
	check(t, restored.Reset())
	value, _ = restored.Read(0x5054, Width32)
	if value != 0 {
		t.Fatalf("reset clock register = %#x", value)
	}
}

func TestQualcommClockRegimeProfiledSleepControllerStops(t *testing.T) {
	device, err := NewQualcommClockRegimeWithSleepControllers([]uint32{0x5200})
	check(t, err)
	check(t, device.Write(0x5230, Width32, 0))
	status, err := device.Read(0x5224, Width32)
	if err != nil || status != 1 {
		t.Fatalf("sleep-controller status = %#x error %v, want stopped state 1", status, err)
	}

	unprofiled := NewQualcommClockRegime()
	check(t, unprofiled.Write(0x5230, Width32, 0))
	status, _ = unprofiled.Read(0x5224, Width32)
	if status != 0 {
		t.Fatalf("unprofiled sleep-controller status = %#x, want latch-only zero", status)
	}
}

func TestQualcommClockRegimeRejectsInvalidSleepControllerProfiles(t *testing.T) {
	for _, offsets := range [][]uint32{{0x3000}, {0x5202}, {0x5200, 0x5200}} {
		if _, err := NewQualcommClockRegimeWithSleepControllers(offsets); err == nil {
			t.Fatalf("accepted invalid sleep-controller offsets %#v", offsets)
		}
	}
}

func TestQualcommClockRegimeProfiledCounterAdvancesAndWraps(t *testing.T) {
	device, err := NewQualcommClockRegimeWithConfig(QualcommClockRegimeConfig{
		Counters: []QualcommClockRegimeCounterConfig{{
			Offset: 0x6000, InstructionsPerSecond: 10, CounterHz: 3, Bits: 4,
		}},
	})
	check(t, err)
	check(t, device.Advance(3))
	value, _ := device.Read(0x6000, Width32)
	if value != 0 {
		t.Fatalf("counter after 9/10 tick = %#x", value)
	}
	check(t, device.Advance(1))
	value, _ = device.Read(0x6000, Width32)
	if value != 1 {
		t.Fatalf("counter after 12/10 ticks = %#x", value)
	}
	check(t, device.Write(0x6000, Width32, 15))
	check(t, device.Advance(30))
	value, _ = device.Read(0x6000, Width32)
	if value != 8 {
		t.Fatalf("wrapped four-bit counter = %#x, want 8", value)
	}
	check(t, device.Reset())
	check(t, device.Advance(4))
	value, _ = device.Read(0x6000, Width32)
	if value != 1 {
		t.Fatalf("counter after reset = %#x, want 1", value)
	}
}

func TestQualcommClockRegimeCounterStatePreservesFractionalPhase(t *testing.T) {
	config := QualcommClockRegimeConfig{Counters: []QualcommClockRegimeCounterConfig{{
		Offset: 0x6000, InstructionsPerSecond: 10, CounterHz: 3, Bits: 18,
	}}}
	source, err := NewQualcommClockRegimeWithConfig(config)
	check(t, err)
	check(t, source.Advance(3))
	state, err := source.SaveState()
	check(t, err)
	restored, err := NewQualcommClockRegimeWithConfig(config)
	check(t, err)
	check(t, restored.LoadState(state))
	check(t, restored.Advance(1))
	value, _ := restored.Read(0x6000, Width32)
	if value != 1 {
		t.Fatalf("restored fractional counter = %#x, want 1", value)
	}

	mismatched, err := NewQualcommClockRegimeWithConfig(QualcommClockRegimeConfig{
		Counters: []QualcommClockRegimeCounterConfig{{
			Offset: 0x6000, InstructionsPerSecond: 10, CounterHz: 2, Bits: 18,
		}},
	})
	check(t, err)
	if err := mismatched.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched counter state error = %v", err)
	}
}

func TestQualcommClockRegimeMigratesLatchOnlyStatesAsSubsets(t *testing.T) {
	legacy := make([]byte, 8+(QualcommClockRegimeWindowSize/4)*4)
	copy(legacy, "QCRG")
	binary.LittleEndian.PutUint32(legacy[4:8], 1)
	binary.LittleEndian.PutUint32(legacy[8+(0x6000/4)*4:], 7)
	profiled, err := NewQualcommClockRegimeWithConfig(QualcommClockRegimeConfig{
		Counters: []QualcommClockRegimeCounterConfig{{
			Offset: 0x6000, InstructionsPerSecond: 10, CounterHz: 3, Bits: 18,
		}},
	})
	check(t, err)
	if err := profiled.LoadState(legacy); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("exact legacy state error = %v", err)
	}
	check(t, profiled.LoadStateSubset(legacy))
	value, _ := profiled.Read(0x6000, Width32)
	if value != 7 {
		t.Fatalf("migrated legacy counter latch = %#x", value)
	}

	latchOnly := NewQualcommClockRegime()
	state, err := latchOnly.SaveState()
	check(t, err)
	if err := profiled.LoadStateSubset(state); err != nil {
		t.Fatalf("migrate v2 counter subset: %v", err)
	}
	check(t, profiled.Advance(4))
	value, _ = profiled.Read(0x6000, Width32)
	if value != 1 {
		t.Fatalf("new counter after v2 subset migration = %#x", value)
	}
}

func TestQualcommClockRegimeRejectsInvalidCounterProfiles(t *testing.T) {
	valid := QualcommClockRegimeCounterConfig{
		Offset: 0x6000, InstructionsPerSecond: 10, CounterHz: 3, Bits: 18,
	}
	profiles := [][]QualcommClockRegimeCounterConfig{
		{{Offset: 0x3000, InstructionsPerSecond: 10, CounterHz: 3, Bits: 18}},
		{{Offset: 0x6002, InstructionsPerSecond: 10, CounterHz: 3, Bits: 18}},
		{{Offset: 0x6000, CounterHz: 3, Bits: 18}},
		{{Offset: 0x6000, InstructionsPerSecond: 10, Bits: 18}},
		{{Offset: 0x6000, InstructionsPerSecond: 10, CounterHz: 11, Bits: 18}},
		{{Offset: 0x6000, InstructionsPerSecond: 10, CounterHz: 3}},
		{{Offset: 0x6000, InstructionsPerSecond: 10, CounterHz: 3, Bits: 33}},
		{valid, valid},
	}
	for _, counters := range profiles {
		if _, err := NewQualcommClockRegimeWithConfig(QualcommClockRegimeConfig{
			Counters: counters,
		}); err == nil {
			t.Fatalf("accepted invalid counter profile %#v", counters)
		}
	}
	if _, err := NewQualcommClockRegimeWithConfig(QualcommClockRegimeConfig{
		SleepControllers: []uint32{0x5200},
		Counters: []QualcommClockRegimeCounterConfig{{
			Offset: 0x5224, InstructionsPerSecond: 10, CounterHz: 3, Bits: 18,
		}},
	}); err == nil {
		t.Fatal("accepted counter overlapping sleep-controller status")
	}
}

func TestQualcommClockRegimeComparatorRaisesProfiledInterruptAndAcknowledges(t *testing.T) {
	interrupts, err := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{SourceCount: 8, Bank0Sources: 4},
		&interruptLineProbe{},
	)
	check(t, err)
	check(t, interrupts.Write(qualcommVICEnable0Offset, Width32, 1<<3))
	config := testQualcommClockRegimeComparatorConfig()
	config.VectoredInterruptController = interrupts
	device, err := NewQualcommClockRegimeWithConfig(config)
	check(t, err)
	check(t, device.Write(0x480c, Width32, 0xa5000011))
	check(t, device.Write(0x48cc, Width32, 3<<8|0x5a))
	check(t, device.Write(0x487c, Width32, 1<<2))
	check(t, device.Advance(14))
	if status, _ := device.Read(0x4864, Width32); status != 0 {
		t.Fatalf("early comparator status = %#x", status)
	}
	if pending := interrupts.PendingStatusBanks(); pending != [2]uint32{} {
		t.Fatalf("early comparator interrupt = %#v", pending)
	}
	if counter, _ := device.Read(0x480c, Width32); counter != 0xa5000211 {
		t.Fatalf("field-packed comparator counter = %#x, want 0xa5000211", counter)
	}
	check(t, device.Advance(1))
	if status, _ := device.Read(0x4864, Width32); status != 1<<2 {
		t.Fatalf("expired comparator status = %#x", status)
	}
	if pending := interrupts.PendingStatusBanks(); pending != [2]uint32{1 << 3, 0} {
		t.Fatalf("expired comparator interrupt = %#v", pending)
	}
	if match, _ := device.Read(0x48cc, Width32); match != 3<<8|0x5a {
		t.Fatalf("comparator advance changed non-field match bits: %#x", match)
	}
	check(t, device.Write(0x4870, Width32, 1<<2))
	if status, _ := device.Read(0x4864, Width32); status != 0 {
		t.Fatalf("acknowledged comparator status = %#x", status)
	}
}

func TestQualcommClockRegimeComparatorIsSliceInvariantAndStateful(t *testing.T) {
	newDevice := func() (*QualcommClockRegime, *QualcommVectoredInterruptController) {
		interrupts, err := NewQualcommVectoredInterruptController(
			QualcommVectoredInterruptConfig{SourceCount: 8, Bank0Sources: 4},
			nil,
		)
		check(t, err)
		config := testQualcommClockRegimeComparatorConfig()
		config.VectoredInterruptController = interrupts
		device, err := NewQualcommClockRegimeWithConfig(config)
		check(t, err)
		_ = device.Write(0x48c4, Width32, 1<<8)
		_ = device.Write(0x487c, Width32, 1)
		return device, interrupts
	}
	whole, wholeInterrupts := newDevice()
	sliced, slicedInterrupts := newDevice()
	check(t, whole.Advance(19))
	for _, instructions := range []uint64{3, 4, 5, 7} {
		check(t, sliced.Advance(instructions))
	}
	wholeState, _ := whole.SaveState()
	slicedState, _ := sliced.SaveState()
	if !bytes.Equal(wholeState, slicedState) {
		t.Fatal("comparator state depends on ClockedRunner slicing")
	}
	if wholeInterrupts.PendingStatusBanks() != slicedInterrupts.PendingStatusBanks() {
		t.Fatal("comparator interrupt state depends on ClockedRunner slicing")
	}

	restoredInterrupts, _ := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{SourceCount: 8, Bank0Sources: 4},
		nil,
	)
	restoredConfig := testQualcommClockRegimeComparatorConfig()
	restoredConfig.VectoredInterruptController = restoredInterrupts
	restored, err := NewQualcommClockRegimeWithConfig(restoredConfig)
	check(t, err)
	check(t, restored.LoadState(wholeState))
	check(t, whole.Advance(1))
	check(t, restored.Advance(1))
	continuedState, _ := whole.SaveState()
	restoredState, _ := restored.SaveState()
	if !bytes.Equal(continuedState, restoredState) {
		t.Fatal("restored comparator lost fractional phase")
	}
	if err := restored.LoadState(wholeState[:len(wholeState)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated comparator state error = %v", err)
	}
}

func TestQualcommClockRegimeComparatorMigratesV2Subset(t *testing.T) {
	previous, err := NewQualcommClockRegimeWithConfig(QualcommClockRegimeConfig{
		Counters: []QualcommClockRegimeCounterConfig{{
			Offset: 0x6000, InstructionsPerSecond: 10, CounterHz: 3, Bits: 18,
		}},
	})
	check(t, err)
	_ = previous.Write(0x48c4, Width32, 1<<8)
	_ = previous.Write(0x487c, Width32, 1)
	v2, err := previous.SaveState()
	check(t, err)
	binary.LittleEndian.PutUint32(v2[4:8], 2)
	v2 = v2[:len(v2)-4]

	interrupts, _ := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{SourceCount: 8, Bank0Sources: 4},
		nil,
	)
	currentConfig := testQualcommClockRegimeComparatorConfig()
	currentConfig.Counters = []QualcommClockRegimeCounterConfig{{
		Offset: 0x6000, InstructionsPerSecond: 10, CounterHz: 3, Bits: 18,
	}}
	currentConfig.VectoredInterruptController = interrupts
	current, err := NewQualcommClockRegimeWithConfig(currentConfig)
	check(t, err)
	if err := current.LoadState(v2); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("exact v2 comparator migration error = %v", err)
	}
	check(t, current.LoadStateSubset(v2))
	check(t, current.Advance(5))
	if status, _ := current.Read(0x4864, Width32); status != 1 {
		t.Fatalf("migrated v2 comparator status = %#x, want event 0", status)
	}
}

func TestQualcommClockRegimeRejectsInvalidComparatorProfiles(t *testing.T) {
	interrupts, _ := NewQualcommVectoredInterruptController(
		QualcommVectoredInterruptConfig{SourceCount: 8, Bank0Sources: 4},
		nil,
	)
	valid := testQualcommClockRegimeComparatorConfig().Comparators[0]
	invalid := []QualcommClockRegimeComparatorConfig{
		{},
		func() QualcommClockRegimeComparatorConfig { value := valid; value.CounterMask = 0x500; return value }(),
		func() QualcommClockRegimeComparatorConfig { value := valid; value.CounterModulus = 257; return value }(),
		func() QualcommClockRegimeComparatorConfig { value := valid; value.EventMask = 0; return value }(),
		func() QualcommClockRegimeComparatorConfig { value := valid; value.MatchStride = 2; return value }(),
		func() QualcommClockRegimeComparatorConfig {
			value := valid
			value.MatchBaseOffset = 0x49f0
			value.EventMask = 0xff
			return value
		}(),
	}
	for _, comparator := range invalid {
		if _, err := NewQualcommClockRegimeWithConfig(QualcommClockRegimeConfig{
			Comparators:                 []QualcommClockRegimeComparatorConfig{comparator},
			VectoredInterruptController: interrupts,
		}); err == nil {
			t.Fatalf("accepted invalid comparator profile %+v", comparator)
		}
	}
	if _, err := NewQualcommClockRegimeWithConfig(QualcommClockRegimeConfig{
		Comparators: []QualcommClockRegimeComparatorConfig{valid},
	}); err == nil {
		t.Fatal("accepted comparator without its vectored interrupt controller")
	}
	if _, err := NewQualcommClockRegimeWithConfig(QualcommClockRegimeConfig{
		Comparators:                 []QualcommClockRegimeComparatorConfig{valid, valid},
		VectoredInterruptController: interrupts,
	}); err == nil {
		t.Fatal("accepted overlapping comparator fields")
	}
}

func testQualcommClockRegimeComparatorConfig() QualcommClockRegimeConfig {
	return QualcommClockRegimeConfig{Comparators: []QualcommClockRegimeComparatorConfig{{
		CounterOffset: 0x480c, CounterMask: 0x0000ff00,
		InstructionsPerSecond: 10, CounterHz: 2, CounterModulus: 5,
		MatchBaseOffset: 0x48c4, MatchStride: 4, MatchMask: 0x0000ff00,
		EnableOffset: 0x487c, StatusOffset: 0x4864, AcknowledgeOffset: 0x4870,
		EventMask: 0x0000000f, InterruptSource: 3, UseVectoredController: true,
	}}}
}
