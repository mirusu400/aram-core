package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image/png"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The exact reported ZIP reaches a BREW card-game screen after leaving its
// network notice and title menus. The package's 16-long sprintf call used to
// fault the machine during play (issue #345).
func TestBREWIssue345ReachesCardGame(t *testing.T) {
	const digest = "e7213d6630aea93ed3b89d6cf2f7880e042c1157651bfc3968712e4ad1a5243f"
	path, data := findAuthorizedPackage(t, digest)
	factory := NewFactory()
	// The embedding frontend opts into structurally validated BREW modules.
	factory.AllowUntrustedBREW = true
	ctx := context.Background()
	machine, err := factory.Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	if err := machine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	step := func(count int) {
		t.Helper()
		for frame := 0; frame < count; frame++ {
			if err := machine.StepFrame(ctx); err != nil {
				t.Fatalf("frame %d/%d: %v", frame, count, err)
			}
		}
	}
	press := func(control string) {
		t.Helper()
		if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}); err != nil {
			t.Fatal(err)
		}
		step(1)
		if err := machine.QueueInput(machinecore.InputEvent{Control: control}); err != nil {
			t.Fatal(err)
		}
		step(100)
	}
	frameHash := func() [32]byte {
		t.Helper()
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, machine.Framebuffer()); err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(encoded.Bytes())
	}
	step(1000)
	press("num3") // network notice -> title
	press("ok")   // title -> main menu
	menu := frameHash()
	press("ok") // main menu -> game menu
	for range 10 {
		press("ok")
	}
	if got := frameHash(); got == menu {
		t.Fatal("game did not advance beyond the main menu")
	}
	if machine.State() != machinecore.StateRunning {
		t.Fatalf("machine state = %s, want running", machine.State())
	}
}
