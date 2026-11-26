package skvmhost

import (
	"bytes"
	"context"
	"image"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/quirkdb"
	"github.com/mirusu400/aram-core/loader/j2me"

	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

func newWallEpochMachine(t *testing.T, source machinecore.Source, app Application) *Machine {
	t.Helper()
	m, err := newJavaMachine(context.Background(), source, app, nil, image.Pt(240, 320), 0, 0, 0, false, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

// The host-selected start date survives a reset, and it is a clock setting
// rather than configuration: a state saved under one date must load on a
// machine started at another, bringing its own clock with it.
func TestJavaMachineWallEpochSurvivesResetAndStateLoad(t *testing.T) {
	source := machinecore.Source{ProfileID: "j2me-1.0/generic/generic", SHA256: strings.Repeat("0", 64)}
	const first, second = int64(1_795_000_000_000), int64(1_200_000_000_000)

	m := newWallEpochMachine(t, source, Application{})
	if got := m.services.Clock.WallMillis(); got != shared.DefaultWallEpochMillis {
		t.Fatalf("default wall clock = %d, want %d", got, shared.DefaultWallEpochMillis)
	}
	if err := m.SetWallEpoch(0); err != nil {
		t.Fatal(err)
	}
	if got := m.services.Clock.WallMillis(); got != shared.DefaultWallEpochMillis {
		t.Fatalf("wall clock after zero epoch = %d, want the default", got)
	}
	if err := m.SetWallEpoch(first); err != nil {
		t.Fatal(err)
	}
	if got := m.services.Clock.WallMillis(); got != first {
		t.Fatalf("wall clock = %d, want %d", got, first)
	}
	if err := m.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.services.Clock.WallMillis(); got != first {
		t.Fatalf("wall clock after reset = %d, want %d", got, first)
	}
	var saved bytes.Buffer
	if err := m.SaveState(&saved); err != nil {
		t.Fatal(err)
	}

	other := newWallEpochMachine(t, source, Application{})
	if err := other.SetWallEpoch(second); err != nil {
		t.Fatal(err)
	}
	if err := other.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatalf("load a state saved under another start date: %v", err)
	}
	if got := other.services.Clock.WallMillis(); got != first {
		t.Fatalf("wall clock after state load = %d, want the saved %d", got, first)
	}
}

// A title whose exact package pins a date keeps it: that date is what keeps
// its packaged save coherent, and the host clock would undo it.
func TestJavaMachineWallEpochLeavesPinnedTitleDate(t *testing.T) {
	pinned := quirkdb.LGTWallEpochs[0]
	source := machinecore.Source{ProfileID: j2me.LGTProfileID, SHA256: pinned.PackageSHA256}
	m := newWallEpochMachine(t, source, Application{MainClass: pinned.MainClass})
	if err := m.SetWallEpoch(1_795_000_000_000); err != nil {
		t.Fatal(err)
	}
	if got := m.services.Clock.WallMillis(); got != pinned.Millis {
		t.Fatalf("pinned title wall clock = %d, want %d", got, pinned.Millis)
	}
}
