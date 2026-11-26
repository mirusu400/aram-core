package application

import (
	"bytes"
	"context"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

// A product host starts the guest at the user's date through the factory. The
// title must read that date from its first instruction and again after a
// reset, while a factory that names no date keeps the fixed default headless
// runs depend on.
func TestFactoryWallEpochStartsTheGuestClock(t *testing.T) {
	client := syntheticKTFClient()
	copy(client[0x30:], []byte{
		0x7f, 0x20, // movs r0, #0x7f
		0x70, 0x47, // bx lr
	})
	jar := testZIP(t, map[string][]byte{"client.bin4096": client})
	archive := testZIP(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__":      []byte("PID:PD000001\nAID:01020304\nMClass:GameMain\n"),
	})
	create := func(epoch int64) *Machine {
		t.Helper()
		factory := NewFactory()
		factory.WallEpochMillis = epoch
		created, err := factory.Create(context.Background(), machinecore.Source{
			Name:     "clock.zip",
			ReaderAt: bytes.NewReader(archive),
			Size:     int64(len(archive)),
		})
		check(t, err)
		machine := created.(*Machine)
		t.Cleanup(func() { _ = machine.Close() })
		return machine
	}
	epochOf := func(machine *Machine) int64 {
		clock := machine.ktf.Services.Clock
		return clock.WallMillis() - clock.Monotonic().Milliseconds()
	}

	if got := epochOf(create(0)); got != shared.DefaultWallEpochMillis {
		t.Fatalf("default wall epoch = %d, want %d", got, shared.DefaultWallEpochMillis)
	}

	const epoch = int64(1_795_000_000_000)
	machine := create(epoch)
	if got := epochOf(machine); got != epoch {
		t.Fatalf("wall epoch = %d, want %d", got, epoch)
	}
	check(t, machine.Reset(context.Background()))
	if got := epochOf(machine); got != epoch {
		t.Fatalf("wall epoch after reset = %d, want %d", got, epoch)
	}
}
