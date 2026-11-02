package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Issue 574: the title registers its ISoundPlayer callback with a NULL user
// pointer, and the callback clears the byte at pUser+12 when a clip is done.
// That store reaches low handset memory, which does not abort on hardware.
// The key sequence is aram-fuzz seed 419 at density 4; the first DONE used to
// fault at frame 225.
func TestIssue574BREWSoundCallbackStoresThroughNullUserPointer(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	const (
		name   = "[KTF BREW] 대항해원정시대EX(작화).zip"
		digest = "bf614facdf2dbf5b1e37b98e0dbed6e063dd7fefae221fa8377932b8d9c23104"
	)
	path := filepath.Join(root, "KTF BREW 게임파일", "RPG", name)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("exact archive is not present in the authorized corpus")
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != digest {
		t.Fatalf("archive SHA-256=%s, want %s", got, digest)
	}
	factory := NewFactory()
	factory.AllowUntrustedBREW = true
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	machine, err := factory.Create(context.Background(), machinecore.Source{
		Name: name, SHA256: digest, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := map[int]machinecore.InputEvent{}
	for _, event := range []struct {
		frame   int
		control string
		pressed bool
	}{
		{62, "left", true}, {70, "ok", true}, {73, "send", true}, {81, "num0", true},
		{84, "star", true}, {87, "num8", true}, {91, "star", false}, {92, "num2", true},
		{113, "soft-left", true}, {118, "num8", false}, {134, "num1", true}, {135, "send", false},
		{140, "menu", true}, {142, "num8", true}, {145, "star", true}, {148, "hash", true},
		{149, "num9", true}, {151, "num9", false}, {158, "num5", true}, {175, "send", true},
		{178, "star", false}, {179, "num6", true}, {184, "num2", false}, {191, "hash", false},
		{196, "soft-left", false}, {198, "num1", false}, {207, "num6", false}, {209, "num2", true},
		{220, "num3", true}, {224, "right", true},
	} {
		events[event.frame] = machinecore.InputEvent{Control: event.control, Pressed: event.pressed}
	}
	for frame := 0; frame < 600; frame++ {
		if event, ok := events[frame]; ok {
			if err := machine.QueueInput(event); err != nil {
				t.Fatalf("frame %d input %+v: %v", frame, event, err)
			}
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	if machine.State() != machinecore.StateRunning {
		t.Fatalf("state=%s after 600 frames, want running", machine.State())
	}
}
