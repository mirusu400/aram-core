package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"path/filepath"
	"strings"
	"testing"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	machinecore "github.com/mirusu400/aram-core/core"
)

const heroLoreFiveKTFDigest = "7bd3a6c272f53ca7b1d67296f705a99f03357f4aa24880d07c558071c8428c38"

// The reported authentication-bypass package has no certification file, but
// its client still requires a test handset number before it enters the game.
func TestReferenceKTFIssue544HeroLoreFiveOfflineIdentity(t *testing.T) {
	path, data := findAuthorizedPackage(t, heroLoreFiveKTFDigest)
	factory := NewFactory()
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		Path:     path,
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
		SHA256:   heroLoreFiveKTFDigest,
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if got := machine.ktf.Services.Device.Config().PhoneNumber; got != "01000000000" {
		t.Fatalf("offline PHONENUMBER = %q", got)
	}
	check(t, machine.ktf.SetTraceMode(ktfrt.KTFTraceFull))
	check(t, machine.Start(context.Background()))
	if trace := strings.Join(machine.ktf.HostTrace, "\n"); !strings.Contains(
		trace,
		`wipic_system_property_get:PHONENUMBER="01000000000":supported=true`,
	) {
		t.Fatalf("test phone was not exposed to the client; trace=%s", trace)
	}
	check(t, machine.ktf.SetTraceMode(ktfrt.KTFTraceCounters))
	stepIssue544(t, machine, 180)
	tapIssue544(t, machine, "ok", 4)
	stepIssue544(t, machine, 180)
	tapIssue544(t, machine, "ok", 4)
	for machine.State() != machinecore.StateStopped {
		check(t, machine.StepFrame(context.Background()))
	}
	check(t, machine.Reset(context.Background()))
	if got := machine.ktf.Services.Device.Config().PhoneNumber; got != "" {
		t.Fatalf("relaunch PHONENUMBER = %q, want unavailable", got)
	}
	check(t, machine.Start(context.Background()))
	stepIssue544(t, machine, 240)
	tapIssue544(t, machine, "ok", 4)
	stepIssue544(t, machine, 300)
	tapIssue544(t, machine, "left", 2)
	tapIssue544(t, machine, "ok", 2)
	stepIssue544(t, machine, 5000)
	noticeFrame := [sha256.Size]byte{
		0xdf, 0xfe, 0xc3, 0x1b, 0x93, 0x01, 0x88, 0x22,
		0x4d, 0xef, 0x94, 0x7b, 0xd1, 0x60, 0x9c, 0x6d,
		0xe7, 0x2f, 0xf5, 0x89, 0x5f, 0xc6, 0x4e, 0x53,
		0xd9, 0x94, 0x2b, 0xd4, 0xd2, 0x79, 0xa0, 0x6e,
	}
	if got := sha256.Sum256(machine.frame.Pix); got != noticeFrame {
		t.Fatalf("post-authentication notice frame = %x", got)
	}
}

func stepIssue544(t *testing.T, machine *Machine, count int) {
	t.Helper()
	for range count {
		check(t, machine.StepFrame(context.Background()))
	}
}

func tapIssue544(t *testing.T, machine *Machine, control string, holdFrames int) {
	t.Helper()
	check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
	stepIssue544(t, machine, holdFrames)
	check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}))
	stepIssue544(t, machine, 1)
}
