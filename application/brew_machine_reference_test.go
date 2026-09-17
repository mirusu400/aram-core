package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	err = machine.Start(context.Background())
	var boundary *brewrt.ExecutionBoundaryError
	if !errors.As(err, &boundary) || boundary.ClassID != brewrt.FirstUnsupportedClassID {
		t.Fatalf("start boundary = %v, want shell class 0x%08x", err, brewrt.FirstUnsupportedClassID)
	}
	if machine.State() != machinecore.StatePaused {
		t.Fatalf("state after first frame = %s, want paused", machine.State())
	}
	if frameIsUniform(machine.Framebuffer()) {
		t.Fatal("exact BREW diagnostic splash is uniform")
	}
	implementation, ok := machine.(*brewMachine)
	if !ok || implementation.runtime == nil || implementation.runtime.ModuleObject() == 0 {
		t.Fatal("exact BREW module entry did not produce a module object")
	}
	if len(implementation.input) != 2 || !implementation.input[0].Pressed || implementation.input[1].Pressed {
		t.Fatalf("exact BREW pending input transitions = %#v", implementation.input)
	}
	t.Logf(
		"module object 0x%08x; %v; diagnostic splash non-uniform; key press/release retained but not dispatched",
		implementation.runtime.ModuleObject(),
		boundary,
	)
}
