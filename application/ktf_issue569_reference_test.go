package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

const superActionHero2KTFDigest = "f9fdd6476e3a9267cc4ef6eec4f59a6484af837cd1b9fa1775215273a63b27a1"

// Issue #569. 슈퍼액션히어로2 (KTF, AID 0103BC81) checks its access level when
// the player leaves the title screen and requires the 0xbc bits of
// MC_knlGetAccessLevel. That kernel slot used to answer 0, so the title
// showed "인증 과정에 에러가 발생했습니다 / 에러코드 : 1001"; confirming it ran
// the title's error path, which passes a string-table offset to MC_fsRemove,
// and the read through 0x00067310 faulted the machine.
//
// With the access level granted, the title reads its packaged cert.c2s and
// creates its /locdata record, which it only does after the check passes.
func TestReferenceKTFIssue569AccessLevelPassesCertificateCheck(t *testing.T) {
	path, data := findAuthorizedPackage(t, superActionHero2KTFDigest)
	factory := NewFactory()
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		Path:     path,
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
		SHA256:   superActionHero2KTFDigest,
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.Start(context.Background()))

	// The publisher logo, then the title screen, then three confirmations:
	// the one that used to fault was the second.
	stepIssue544(t, machine, 500)
	for range 3 {
		tapIssue544(t, machine, "ok", 3)
		stepIssue544(t, machine, 96)
	}
	if machine.State() == machinecore.StateStopped {
		t.Fatal("title stopped after leaving the title screen")
	}
	if _, ok := machine.ktf.FileData["/locdata"]; !ok {
		t.Fatalf("title never created /locdata; private files = %d", len(machine.ktf.FileData))
	}
}
