package runtime

import (
	"errors"
	"math"
	"testing"
	"time"
)

// A host starts a machine at a date of its choosing by moving the wall epoch.
// The monotonic reading, which every timer and event is stamped with, must not
// move with it.
func TestClockSetWallEpochMovesOnlyTheWallClock(t *testing.T) {
	clock, err := NewClock(0, 540, "ko-KR")
	if err != nil {
		t.Fatal(err)
	}
	if err := clock.Advance(1500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	const epoch = int64(1_795_000_000_000)
	if err := clock.SetWallEpoch(epoch); err != nil {
		t.Fatal(err)
	}
	if got, want := clock.Monotonic(), 1500*time.Millisecond; got != want {
		t.Errorf("monotonic = %s, want %s", got, want)
	}
	if got, want := clock.WallMillis(), epoch+1500; got != want {
		t.Errorf("wall = %d, want %d", got, want)
	}
	if got, want := clock.LocalMillis(), epoch+1500+540*60_000; got != want {
		t.Errorf("local = %d, want %d", got, want)
	}
	if err := clock.SetWallEpoch(math.MaxInt64); !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("overflowing epoch: err = %v, want ErrLimitExceeded", err)
	}
	if got, want := clock.WallMillis(), epoch+1500; got != want {
		t.Errorf("wall after rejected epoch = %d, want %d", got, want)
	}
}
