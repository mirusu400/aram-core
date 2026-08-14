package runtime

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const DefaultWallEpochMillis = int64(946684800000) // 2000-01-01T00:00:00Z

type ClockState struct {
	MonotonicNanos     int64
	WallEpochMillis    int64
	TimezoneOffsetMins int32
	Locale             string
	AdvanceSequence    uint64
}

// Clock is a virtual clock. Host wall time is never consulted.
type Clock struct {
	monotonic          time.Duration
	wallEpochMillis    int64
	timezoneOffsetMins int32
	locale             string
	advanceSequence    uint64
}

func NewClock(epochMillis int64, timezoneOffsetMins int32, locale string) (*Clock, error) {
	if epochMillis == 0 {
		epochMillis = DefaultWallEpochMillis
	}
	clock := &Clock{}
	if err := clock.Restore(ClockState{
		WallEpochMillis:    epochMillis,
		TimezoneOffsetMins: timezoneOffsetMins,
		Locale:             locale,
	}); err != nil {
		return nil, err
	}
	return clock, nil
}

func (c *Clock) Monotonic() time.Duration {
	return c.monotonic
}

func (c *Clock) WallMillis() int64 {
	return c.wallEpochMillis + c.monotonic.Milliseconds()
}

func (c *Clock) LocalMillis() int64 {
	return c.WallMillis() + int64(c.timezoneOffsetMins)*60_000
}

func (c *Clock) Locale() string {
	return c.locale
}

func (c *Clock) TimezoneOffsetMinutes() int32 {
	return c.timezoneOffsetMins
}

func (c *Clock) Advance(delta time.Duration) error {
	if delta < 0 || delta > time.Duration(math.MaxInt64-int64(c.monotonic)) {
		return fmt.Errorf("%w: invalid clock advance %s", ErrInvalidArgument, delta)
	}
	if delta == 0 {
		return nil
	}
	if c.advanceSequence == math.MaxUint64 {
		return fmt.Errorf("%w: clock advance sequence exhausted", ErrLimitExceeded)
	}
	next := c.monotonic + delta
	if !validWallTime(
		c.wallEpochMillis,
		next,
		c.timezoneOffsetMins,
	) {
		return fmt.Errorf("%w: clock wall time overflows", ErrLimitExceeded)
	}
	c.monotonic = next
	c.advanceSequence++
	return nil
}

func (c *Clock) Snapshot() ClockState {
	return ClockState{
		MonotonicNanos:     int64(c.monotonic),
		WallEpochMillis:    c.wallEpochMillis,
		TimezoneOffsetMins: c.timezoneOffsetMins,
		Locale:             c.locale,
		AdvanceSequence:    c.advanceSequence,
	}
}

func (c *Clock) Restore(state ClockState) error {
	if state.MonotonicNanos < 0 ||
		state.TimezoneOffsetMins < -24*60 ||
		state.TimezoneOffsetMins > 24*60 ||
		len(state.Locale) > 64 ||
		strings.IndexByte(state.Locale, 0) >= 0 ||
		!validWallTime(
			state.WallEpochMillis,
			time.Duration(state.MonotonicNanos),
			state.TimezoneOffsetMins,
		) {
		return fmt.Errorf("%w: invalid clock state", ErrInvalidState)
	}
	c.monotonic = time.Duration(state.MonotonicNanos)
	c.wallEpochMillis = state.WallEpochMillis
	c.timezoneOffsetMins = state.TimezoneOffsetMins
	c.locale = state.Locale
	c.advanceSequence = state.AdvanceSequence
	return nil
}

func validWallTime(
	epochMillis int64,
	monotonic time.Duration,
	timezoneOffsetMins int32,
) bool {
	elapsedMillis := monotonic.Milliseconds()
	if elapsedMillis > 0 && epochMillis > math.MaxInt64-elapsedMillis {
		return false
	}
	wall := epochMillis + elapsedMillis
	offset := int64(timezoneOffsetMins) * 60_000
	if offset > 0 {
		return wall <= math.MaxInt64-offset
	}
	return offset == 0 || wall >= math.MinInt64-offset
}
