package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/cheat"
	machinecore "github.com/mirusu400/aram-core/core"
)

const fortress4KTFDigest = "7618e9a8f50aa8fc6b50f1e49e9ad3641618e1b41d3ef921efe3d63f120ac8db"

// Issue #570. In SBS포트리스4 (KTF Clet, AID 01042CDD) a tank should drive
// while a direction is held and stop when it is let go. It did nothing while
// the key was down and then kept driving after the release, because a Clet
// card was handed the press and release edges the wrong way round: the
// title's native code saw the key go up when it was pressed and go down when
// it was released.
//
// The title keeps the direction its tank is driving in a word of its battle
// state, reached through GOT slot 0x0015347c at offset 0x5c: -1 while driving
// left, 0 when stopped. Its native key-press handler sets it and its
// key-release handler clears it.
func TestReferenceKTFIssue570TankDrivesOnlyWhileTheKeyIsHeld(t *testing.T) {
	path, data := findAuthorizedPackage(t, fortress4KTFDigest)
	factory := NewFactory()
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		Path:     path,
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
		SHA256:   fortress4KTFDigest,
	})
	check(t, err)
	machine := created.(*Machine)
	wrapped, err := machine.WithCheats(cheat.Options{})
	check(t, err)
	t.Cleanup(func() { _ = wrapped.Close() })
	library, err := cheat.NewLibrary(wrapped.Cheats())
	check(t, err)
	check(t, library.Import(fortress4KTFCatalog()))
	check(t, library.ApplyState(nil))
	check(t, wrapped.Start(context.Background()))

	// The first launch shows a one-time restart notice and exits on any key.
	stepIssue544(t, wrapped, 100)
	check(t, wrapped.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: true}))
	check(t, wrapped.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: false}))
	for frames := 0; wrapped.State() != machinecore.StateStopped; frames++ {
		if frames == 300 {
			t.Fatal("first-run restart did not stop the application")
		}
		check(t, wrapped.StepFrame(context.Background()))
	}
	check(t, wrapped.Reset(context.Background()))
	check(t, wrapped.Start(context.Background()))

	// Idle-screen notice, publisher logos and title screen, 게임시작, 아케이드
	// 모드, the first tank, and a tip dialog lead into the first battle.
	stepIssue544(t, wrapped, 295)
	tapIssue544(t, wrapped, "ok", 3)
	stepIssue544(t, wrapped, 1296)
	for _, control := range []string{"ok", "ok", "down", "ok", "ok", "ok"} {
		tapIssue544(t, wrapped, control, 3)
		stepIssue544(t, wrapped, 96)
	}
	stepIssue544(t, wrapped, 600)

	const (
		gameStateGOT  = uint32(0x00153450)
		battleGOT     = uint32(0x0015347c)
		battleState   = uint32(0x13)
		directionWord = uint32(0x5c)
	)
	read := func(got, offset uint32) uint32 {
		t.Helper()
		address, err := machine.ktf.ReadU32(got)
		check(t, err)
		value, err := machine.ktf.ReadU32(address + offset)
		check(t, err)
		return value
	}
	if state := read(gameStateGOT, 0); state != battleState {
		t.Fatalf("title state = %#x, want the battle %#x", state, battleState)
	}
	if direction := int32(read(battleGOT, directionWord)); direction != 0 {
		t.Fatalf("tank direction before any key = %d, want 0", direction)
	}

	check(t, wrapped.QueueInput(machinecore.InputEvent{Control: "left", Pressed: true}))
	stepIssue544(t, wrapped, 5)
	if direction := int32(read(battleGOT, directionWord)); direction != -1 {
		t.Fatalf("tank direction just after pressing left = %d, want -1", direction)
	}
	// Hold the key well past the auto-repeat delay.
	stepIssue544(t, wrapped, 55)
	if direction := int32(read(battleGOT, directionWord)); direction != -1 {
		t.Fatalf("tank direction with left still held = %d, want -1", direction)
	}
	check(t, wrapped.QueueInput(machinecore.InputEvent{Control: "left", Pressed: false}))
	stepIssue544(t, wrapped, 5)
	if direction := int32(read(battleGOT, directionWord)); direction != 0 {
		t.Fatalf("tank direction after releasing left = %d, want 0", direction)
	}
}

// fortress4KTFCatalog is the aram-cheat entry the product enables by default
// for this package. Without it the title waits forever on 게임 인증중.. for
// the retired publisher server.
func fortress4KTFCatalog() cheat.Catalog {
	return cheat.Catalog{
		Version: cheat.CatalogVersion,
		Title: cheat.Title{
			ImageSHA256: "68d76fe126bb9d8abd4a858b126d7e539e1edebedae31a5cba8b7652cafffe14",
			FileSHA256:  []string{fortress4KTFDigest},
		},
		Cheats: []cheat.Cheat{{
			ID:               "skip-game-authentication",
			Name:             "Skip game authentication",
			RestoreOnDisable: true,
			DefaultEnabled:   true,
			Patches: []cheat.Patch{{
				Address:  0x00118894,
				Value:    cheat.Bytes{0x01, 0x21},
				Expected: cheat.Bytes{0x59, 0x79},
			}},
		}},
	}
}
