package application

import (
	"context"
	"encoding/binary"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// gvmHostRequestArchive builds a v2 image whose primary dispatch runs code and
// whose single file-backed media record holds a NUL-terminated URL.
func gvmHostRequestArchive(t *testing.T, code []byte, url string) []byte {
	t.Helper()
	const entry, event, input, ds, ps, dm, pm = 54, 112, 120, 128, 132, 136, 140
	if len(code) > event-entry {
		t.Fatalf("code of %d bytes overlaps the event entry", len(code))
	}
	payload := append([]byte(url), 0)
	sgs := make([]byte, pm+len(payload))
	sgs[0], sgs[2], sgs[5], sgs[10] = 2, 12, 1, 'G'
	for offset, value := range map[int]uint16{
		0x1c: entry, 0x20: event, 0x22: input, 0x2c: ds, 0x2e: ps, 0x30: dm, 0x32: pm,
	} {
		binary.LittleEndian.PutUint16(sgs[offset:], value)
	}
	copy(sgs[entry:], code)
	copy(sgs[event:], []byte{0xff})
	copy(sgs[input:], []byte{0xff})
	copy(sgs[ds:], []byte{1, 2, 1, 0})
	copy(sgs[ps:], []byte{1, 2, 3, 4})
	binary.LittleEndian.PutUint16(sgs[dm+2:], uint16(len(payload)))
	copy(sgs[pm:], payload)
	return testZIP(t, map[string][]byte{"game.sgs": sgs})
}

func TestGVMBrowserRequestStopsMachineAndExposesOutbox(t *testing.T) {
	const target = "http://example.invalid/genesis/down"
	machine := newOperationalFixture(t, gvmHostRequestArchive(t, []byte{0x05, 0, 0xc4, 0xff}, target))
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateStopped {
		t.Fatalf("state = %s, want stopped", machine.State())
	}
	requests := machine.ExternalRequests()
	if len(requests) != 1 || requests[0].Kind != machinecore.ExternalRequestBrowser || requests[0].Target != target || requests[0].Sequence == 0 {
		t.Fatalf("requests = %+v", requests)
	}
	machine.ClearExternalRequests(requests[0].Sequence)
	if remaining := machine.ExternalRequests(); len(remaining) != 0 {
		t.Fatalf("cleared requests = %+v", remaining)
	}
	if err := machine.Reset(context.Background()); err != nil || machine.State() != machinecore.StateReady {
		t.Fatalf("reset = %v, state %s", err, machine.State())
	}
}

func TestGVMExitRequestStopsMachineWithoutRequests(t *testing.T) {
	machine := newOperationalFixture(t, gvmHostRequestArchive(t, []byte{0xc5}, "unused"))
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateStopped || len(machine.ExternalRequests()) != 0 {
		t.Fatalf("state = %s requests = %+v", machine.State(), machine.ExternalRequests())
	}
}

// ARAM has no other stored scripts, so a script launch ends the title the way
// a failed launch does on the reference emulator.
func TestGVMScriptLaunchStopsMachine(t *testing.T) {
	// provider, script, name media 0, word, data media 0, word, c3
	code := []byte{0x05, 1, 0x05, 2, 0x05, 0, 0x05, 0, 0x05, 0, 0x05, 0, 0xc3}
	machine := newOperationalFixture(t, gvmHostRequestArchive(t, code, "next"))
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateStopped || len(machine.ExternalRequests()) != 0 {
		t.Fatalf("state = %s requests = %+v", machine.State(), machine.ExternalRequests())
	}
}

func TestGVMCloseHidesUnacknowledgedRequests(t *testing.T) {
	machine := newOperationalFixture(t, gvmHostRequestArchive(t, []byte{0x05, 0, 0xc4}, "http://example.invalid/"))
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(machine.ExternalRequests()) != 1 {
		t.Fatal("browser request was not recorded")
	}
	if err := machine.Close(); err != nil {
		t.Fatal(err)
	}
	if requests := machine.ExternalRequests(); len(requests) != 0 {
		t.Fatalf("closed machine requests = %+v", requests)
	}
}
