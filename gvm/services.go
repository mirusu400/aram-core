package gvm

import (
	"errors"
	"fmt"
	"math"

	gruntime "github.com/mirusu400/aram-core/runtime"
)

var (
	ErrInvalidServiceConfig   = errors.New("gvm: invalid service configuration")
	ErrDeviceQueryUnavailable = errors.New("gvm: device query service unavailable")
	ErrClockUnavailable       = errors.New("gvm: clock service unavailable")
	ErrClockRange             = errors.New("gvm: clock outside supported civil-time range")
)

// DeviceQueryProfile is explicit GVM adapter state, not a detected handset or
// audio capability enum. Width and Height must be 1..256 as portable safety
// policy. AudioType retains all signed32 bits for the reference's exact transform.
// The constructor copies this value; there is no mid-run profile setter.
type DeviceQueryProfile struct {
	Width     int32
	Height    int32
	AudioType int32
}

// CivilTimePolicy identifies an explicitly selected portable conversion policy.
// Zero is absent, not an implicit UTC or host-timezone policy.
type CivilTimePolicy uint8

// FixedOffsetNoDST uses the borrowed clock's whole-minute fixed UTC offset.
// It does not reproduce Windows timezone discovery or DST transition behavior.
const FixedOffsetNoDST CivilTimePolicy = 1

// ServiceConfig opts independently into device query (51) and civil clock (b9).
// Clock and ClockPolicy must be supplied together. The Clock pointer is borrowed,
// not copied: the owner must serialize VM execution and clock Advance/Restore.
// GVM never advances, restores or replaces that clock. A nonnil clock alone does
// not establish an actual device's time or profile. Callers must select both.
// No VM save-state or factory/startup integration is supplied by this API.
type ServiceConfig struct {
	DeviceQuery *DeviceQueryProfile
	Clock       *gruntime.Clock
	ClockPolicy CivilTimePolicy
}

type serviceState struct {
	deviceQuery *DeviceQueryProfile
	clock       *gruntime.Clock
	clockPolicy CivilTimePolicy
}

// NewWithAddressSpaceAndServices explicitly enables service-aware dispatch.
// Nil config enables no provider and supplies no defaults: valid query operands
// then report a named unavailable cause inside ExecutionError. Old constructors
// remain distinguishable and return UnsupportedOpcodeError for 51/b9 instead.
// Configuration is validated before arena construction, with no clock mutation.
// Current clock conversion range is checked per query, since its owner can advance
// or restore the shared clock after construction. Epoch zero can be selected via
// runtime.Clock.Restore; runtime.NewClock(0,...) instead normalizes to its default.
func NewWithAddressSpaceAndServices(program []byte, entry uint32, space AddressSpace, config *ServiceConfig) (*VM, error) {
	state := &serviceState{}
	if config != nil {
		if p := config.DeviceQuery; p != nil {
			if p.Width < 1 || p.Width > 256 || p.Height < 1 || p.Height > 256 {
				return nil, fmt.Errorf("%w: device dimensions must be 1..256", ErrInvalidServiceConfig)
			}
			copy := *p
			state.deviceQuery = &copy
		}
		if (config.Clock == nil && config.ClockPolicy != 0) || (config.Clock != nil && config.ClockPolicy != FixedOffsetNoDST) {
			return nil, fmt.Errorf("%w: clock requires explicit FixedOffsetNoDST policy", ErrInvalidServiceConfig)
		}
		state.clock, state.clockPolicy = config.Clock, config.ClockPolicy
	}
	v, err := NewWithAddressSpace(program, entry, space)
	if err != nil {
		return nil, err
	}
	v.services = state
	return v, nil
}

// serviceSpan keeps ReadWord's tag/signed-index rules, with full output preflight
// against the selected global arena, not descriptor lengths. It is private so
// callers cannot obtain a mutable view of VM storage.
func (v *VM) serviceSpan(ref uint16, extent uint64) ([]byte, error) {
	if v.address == nil {
		return nil, ErrInvalidAddress
	}
	region, index := v.address.ram, ref
	if index&0x4000 != 0 {
		region = v.address.file
		index &^= 0x4000
	}
	start := uint64(index) * 2
	if int16(index) < 0 || start >= uint64(len(region)) || extent > uint64(len(region))-start {
		return nil, ErrInvalidAddress
	}
	return region[start : start+extent], nil
}

func (s *serviceState) deviceQueryWords() ([4]uint16, error) {
	if s.deviceQuery == nil {
		return [4]uint16{}, ErrDeviceQueryUnavailable
	}
	p := s.deviceQuery
	k := uint16(8)
	switch {
	case p.Width < 120 || p.Height < 80:
		k = 1
	case p.Width < 128 || p.Height < 128:
		k = 2
	case p.Width < 176 || p.Height < 176:
		k = 4
	}
	mask := uint16(uint32(1) << uint32(p.AudioType&31))
	if p.AudioType == 5 {
		mask = 0x24
	} else if p.AudioType == 6 {
		mask = 0x64
	}
	return [4]uint16{k, 4, mask, 1}, nil
}

func checkedServiceMillis(a, b int64) (int64, bool) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, false
	}
	return a + b, true
}

func (s *serviceState) clockWords() ([4]uint16, error) {
	if s.clock == nil || s.clockPolicy != FixedOffsetNoDST {
		return [4]uint16{}, ErrClockUnavailable
	}
	// One authoritative sample, under the caller's serialization discipline.
	// No WallMillis/LocalMillis resampling, host time, time advancement or RNG.
	state := s.clock.Snapshot()
	if state.MonotonicNanos < 0 || state.TimezoneOffsetMins < -1440 || state.TimezoneOffsetMins > 1440 {
		return [4]uint16{}, ErrClockRange
	}
	utc, ok := checkedServiceMillis(state.WallEpochMillis, state.MonotonicNanos/1_000_000)
	const maxMillis = int64(2147483647999)
	if !ok || utc < 0 || utc > maxMillis {
		return [4]uint16{}, ErrClockRange
	}
	local, ok := checkedServiceMillis(utc, int64(state.TimezoneOffsetMins)*60_000)
	// Validate milliseconds before division: -1ms must not truncate into epoch0.
	// This conservative range is portable policy, not full native CRT equivalence.
	if !ok || local < 0 || local > maxMillis {
		return [4]uint16{}, ErrClockRange
	}
	seconds := local / 1000
	return [4]uint16{uint16(seconds / 3600 % 24), uint16(seconds / 60 % 60), uint16(seconds % 60), uint16(utc % 1000)}, nil
}
