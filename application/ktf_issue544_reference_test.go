package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/cheat"
	machinecore "github.com/mirusu400/aram-core/core"
)

const heroLoreFiveKTFDigest = "7bd3a6c272f53ca7b1d67296f705a99f03357f4aa24880d07c558071c8428c38"

// The KTF build has no certification file and its retired handset check ends on
// an authentication-failure screen. The cataloged repair completes that local
// state and the one-time first-run flow without contacting a carrier service.
func TestReferenceKTFIssue544HeroLoreFiveOfflineLocalStart(t *testing.T) {
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
	if got := machine.ktf.Services.Device.Config().PhoneNumber; got != "01000000000" {
		t.Fatalf("offline PHONENUMBER = %q", got)
	}
	wrapped, err := machine.WithCheats(cheat.Options{})
	check(t, err)
	t.Cleanup(func() { _ = wrapped.Close() })
	library, err := cheat.NewLibrary(wrapped.Cheats())
	check(t, err)
	check(t, library.Import(heroLoreFiveKTFCatalog()))
	check(t, library.ApplyState(nil))

	check(t, wrapped.Start(context.Background()))
	stepIssue544(t, wrapped, 180)
	tapIssue544(t, wrapped, "ok", 4)
	stepIssue544(t, wrapped, 180)
	tapIssue544(t, wrapped, "ok", 4)
	for frames := 0; wrapped.State() != machinecore.StateStopped; frames++ {
		if frames == 300 {
			t.Fatal("first-run restart did not stop the application")
		}
		check(t, wrapped.StepFrame(context.Background()))
	}
	check(t, wrapped.Reset(context.Background()))
	if got := machine.ktf.Services.Device.Config().PhoneNumber; got != "" {
		t.Fatalf("relaunch PHONENUMBER = %q, want unavailable", got)
	}
	check(t, wrapped.Start(context.Background()))
	stepIssue544(t, wrapped, 240)
	tapIssue544(t, wrapped, "ok", 4)
	stepIssue544(t, wrapped, 5000)
	titleFrame := [sha256.Size]byte{
		0xe3, 0xbc, 0xa1, 0xb0, 0x76, 0x5b, 0x13, 0xf9,
		0xd9, 0x46, 0x69, 0x6b, 0x2d, 0x31, 0x26, 0x74,
		0xd0, 0xcf, 0x28, 0xa3, 0x4d, 0x72, 0xb0, 0xd2,
		0x13, 0xd1, 0x17, 0xf8, 0x16, 0xd8, 0xf0, 0xe5,
	}
	if got := sha256.Sum256(machine.frame.Pix); got != titleFrame {
		t.Fatalf("KTF title frame = %x", got)
	}
}

func heroLoreFiveKTFCatalog() cheat.Catalog {
	return cheat.Catalog{
		Version: cheat.CatalogVersion,
		Title: cheat.Title{
			ImageSHA256: "a07d0074c564e5660affbab4c470cd6be64c70401c909e7f70bb88bf26eb1d85",
			FileSHA256:  []string{heroLoreFiveKTFDigest},
		},
		Cheats: []cheat.Cheat{{
			ID:               "offline-local-start",
			Name:             "Start locally without retired carrier checks",
			RestoreOnDisable: true,
			DefaultEnabled:   true,
			Patches: []cheat.Patch{
				{
					Address:  0x0016b7cc,
					Value:    cheat.Bytes{0x01, 0x22},
					Expected: cheat.Bytes{0x02, 0x1c},
				},
				{
					Address:  0x0020168a,
					Value:    cheat.Bytes{0x05, 0x21},
					Expected: cheat.Bytes{0x01, 0x21},
				},
			},
		}},
	}
}

func stepIssue544(t *testing.T, machine machinecore.Machine, count int) {
	t.Helper()
	for range count {
		check(t, machine.StepFrame(context.Background()))
	}
}

func tapIssue544(
	t *testing.T,
	machine machinecore.Machine,
	control string,
	holdFrames int,
) {
	t.Helper()
	check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
	stepIssue544(t, machine, holdFrames)
	check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}))
	stepIssue544(t, machine, 1)
}
