package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/brewrt"
	machinecore "github.com/mirusu400/aram-core/core"
)

func TestBREWExactArchiveBootstrap(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	path := root
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(root, "KTF BREW 게임파일", "RPG", "[KTF BREW] 고구려영웅전-주몽편.zip")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read exact BREW reference archive %q: %v", path, err)
	}
	digest := sha256.Sum256(data)
	if got := hex.EncodeToString(digest[:]); got != brewrt.ArchiveSHA256 {
		t.Fatalf("reference archive SHA-256 = %s, want %s", got, brewrt.ArchiveSHA256)
	}
	machine, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: "고구려영웅전-주몽편.zip", Path: path, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatalf("create exact BREW machine: %v", err)
	}
	defer machine.Close()
	if err := machine.QueueInput(machinecore.InputEvent{Control: "up", Pressed: true}); err != nil {
		t.Fatalf("queue BREW key press: %v", err)
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "up", Pressed: false}); err != nil {
		t.Fatalf("queue BREW key release: %v", err)
	}
	if err := machine.Start(context.Background()); err != nil {
		t.Fatalf("start exact BREW title: %v", err)
	}
	if machine.State() != machinecore.StateRunning {
		t.Fatalf("state after first frame = %s, want running", machine.State())
	}
	for frames := 0; frames < 64 && frameIsUniform(machine.Framebuffer()); frames++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("step exact BREW title to first guest frame: %v", err)
		}
	}
	if frameIsUniform(machine.Framebuffer()) {
		t.Fatal("exact BREW guest framebuffer remained uniform after 64 frames")
	}
	implementation, ok := machine.(*brewMachine)
	if !ok || implementation.runtime == nil || implementation.runtime.ModuleObject() == 0 {
		t.Fatal("exact BREW module entry did not produce a module object")
	}
	if !implementation.guestFrame {
		t.Fatal("exact BREW frame was not committed by guest IDisplay Update")
	}
	stats, present := implementation.BREWFrameStats()
	if !present || stats.PresentCount == 0 || !stats.FrameValid {
		t.Fatalf("BREW frame stats = %+v, present=%v", stats, present)
	}
	if len(implementation.input) != 0 {
		t.Fatalf("exact BREW pending input transitions = %#v, want dispatched", implementation.input)
	}
	if got := implementation.runtime.EventCount(0x101); got != 1 {
		t.Fatalf("BREW EVT_KEY_PRESS dispatches = %d, want 1", got)
	}
	if got := implementation.runtime.EventCount(0x102); got != 1 {
		t.Fatalf("BREW EVT_KEY_RELEASE dispatches = %d, want 1", got)
	}
	t.Logf("module object 0x%08x; guest framebuffer committed; key press/release dispatched", implementation.runtime.ModuleObject())
}
