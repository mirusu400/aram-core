package application

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

// The issue happened after roughly eight hours, so replay the captured fault
// registers against the exact authorized package rather than committing a game
// save or running a timing-sensitive eight-hour test.
func TestIssue535Zenonia2SkipsInvalidIceBossSprite(t *testing.T) {
	path, data := findAuthorizedPackage(t, zenonia2RaptorSHA256)
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.cpu.WriteRegister(cpu.RegisterLR, 0x00070eef))
	check(t, machine.cpu.WriteRegister(cpu.RegisterR4, 0))
	check(t, machine.cpu.WriteRegister(cpu.RegisterR5, 105))
	check(t, machine.cpu.WriteRegister(cpu.RegisterR0, 105))

	result := cpu.Result{
		Reason: cpu.StopFault,
		PC:     0,
		Err:    errors.New("execute at unmapped guest address 0x00000000"),
	}
	machine.resumeRaptorInvalidDraw(&result)
	if result.Err != nil || result.Reason != cpu.StopBudget || result.PC != 0x00070eee {
		t.Fatalf("captured ice-boss draw fault was not recovered: %+v", result)
	}
	if r0, err := machine.cpu.ReadRegister(cpu.RegisterR0); err != nil || r0 != 0 {
		t.Fatalf("draw return = %08x, err=%v", r0, err)
	}
}
