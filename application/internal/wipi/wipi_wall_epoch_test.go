package wipi

import (
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

// The start date a host selects must still be the date after the runtime
// rebuilds its services for a reset.
func TestPublicRuntimeWallEpochSurvivesReset(t *testing.T) {
	runtime := newPublicRuntime(t)
	check(t, runtime.SetWallEpoch(0))
	if got := runtime.Services.Clock.WallMillis(); got != shared.DefaultWallEpochMillis {
		t.Fatalf("wall clock after zero epoch = %d, want the default", got)
	}
	const epoch = int64(1_795_000_000_000)
	check(t, runtime.SetWallEpoch(epoch))
	if got := runtime.Services.Clock.WallMillis(); got != epoch {
		t.Fatalf("wall clock = %d, want %d", got, epoch)
	}
	check(t, runtime.Reset())
	if got := runtime.Services.Clock.WallMillis(); got != epoch {
		t.Fatalf("wall clock after reset = %d, want %d", got, epoch)
	}
}
