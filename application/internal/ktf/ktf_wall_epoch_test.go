package ktf

import "testing"

// MC_knlCurrentTime and System.currentTimeMillis read the services clock, so
// the start date a host selects is the date the title sees.
func TestKTFRuntimeWallEpochIsWhatTheGuestReads(t *testing.T) {
	runtime := newCardOriginRuntime(t)
	defaultEpoch := runtime.wallEpochMS()
	check(t, runtime.SetWallEpoch(0))
	if got := runtime.wallEpochMS(); got != defaultEpoch {
		t.Fatalf("wall epoch after zero = %d, want the default %d", got, defaultEpoch)
	}
	const epoch = int64(1_795_000_000_000)
	check(t, runtime.SetWallEpoch(epoch))
	if got := runtime.wallEpochMS(); got != epoch {
		t.Fatalf("wall epoch = %d, want %d", got, epoch)
	}
	if got := runtime.wallReadMS(); got < uint64(epoch) || got > uint64(epoch)+1000 {
		t.Fatalf("guest wall read = %d, want about %d", got, epoch)
	}
}
