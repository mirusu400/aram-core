package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

func TestBREWLegacyKeyEventAdvancesReportedRPGs(t *testing.T) {
	for _, test := range []struct {
		issue, digest string
	}{
		{"313", "9d1bf51748ac93d6008d2a55f29a5c26c13d778486b981c258c2c8ef9dedf9ef"},
		{"314", "08d439acc62dda49995218275464cb04ee7338bbcffcb261558290f80be4eb32"},
	} {
		t.Run(test.issue, func(t *testing.T) {
			path, data := findAuthorizedPackage(t, test.digest)
			machine := newBREWReferenceMachine(t, path, data)
			stepBREWReference(t, machine, 239)
			before := brewFrameHash(machine.Framebuffer())
			tapBREWReference(t, machine)
			stepBREWReference(t, machine, 120)
			after := brewFrameHash(machine.Framebuffer())
			if before == after {
				t.Fatal("first select tap left the guest screen unchanged")
			}
			tapBREWReference(t, machine)
			stepBREWReference(t, machine, 240)
			if second := brewFrameHash(machine.Framebuffer()); second == after {
				t.Fatal("second select tap left the guest screen unchanged")
			}
			if got := machine.runtime.EventCount(0x100); got != 2 {
				t.Fatalf("EVT_KEY dispatches = %d, want 2", got)
			}
		})
	}
}

func TestBREWIssue314RendersTitleMenuAndGameplay(t *testing.T) {
	const digest = "08d439acc62dda49995218275464cb04ee7338bbcffcb261558290f80be4eb32"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 239)
	for _, frames := range []int{120, 240} {
		tapBREWReference(t, machine)
		stepBREWReference(t, machine, frames)
	}
	titleHash := brewFrameHash(machine.Framebuffer())
	if got := hex.EncodeToString(titleHash[:]); got != "b02aadda8aa43ce20399829bca111048980978bb20e9a821f0fd1624661ffbb4" {
		t.Fatalf("title artwork frame SHA-256 = %s", got)
	}
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 120)
	menuHash := brewFrameHash(machine.Framebuffer())
	if got := hex.EncodeToString(menuHash[:]); got != "4755ed61d2ff1bf2903cde279745fcdd379187f86592373879c009cf7be9a61b" {
		t.Fatalf("readable main menu frame SHA-256 = %s", got)
	}
	// The first menu item opens new-game selection; continue through its
	// confirmation and verify the story is still advancing after rendering.
	for _, frames := range []int{120, 240, 240} {
		tapBREWReference(t, machine)
		stepBREWReference(t, machine, frames)
	}
	before := brewFrameHash(machine.Framebuffer())
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 120)
	if after := brewFrameHash(machine.Framebuffer()); after == before {
		t.Fatal("story did not advance after selecting new game")
	}
	for tap := 1; tap < 60; tap++ {
		tapBREWReference(t, machine)
		stepBREWReference(t, machine, 120)
	}
	mapHash := brewFrameHash(machine.Framebuffer())
	if got := hex.EncodeToString(mapHash[:]); got != "bfb251f7a89bee2d41d480ff718d489d9215984d182febe3bc905117d470cc2f" {
		t.Fatalf("illustrated game map frame SHA-256 = %s", got)
	}
	for press := 0; press < 4; press++ {
		tapBREWControl(t, machine, "right")
		stepBREWReference(t, machine, 120)
	}
	if after := brewFrameHash(machine.Framebuffer()); after == mapHash {
		t.Fatal("game map did not respond to directional input")
	}
}

func TestBREWIssue313ReachesCombatWithoutBitmapHeapExhaustion(t *testing.T) {
	const digest = "9d1bf51748ac93d6008d2a55f29a5c26c13d778486b981c258c2c8ef9dedf9ef"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 239)
	checks := map[int]string{
		1:  "b09d68328a6c7140b04231faec4f17468403f18ba31136ea16352e1b44b62d61", // main menu
		3:  "697a12d5b4190c21e0a5c8375dd3e914913a6ddf7ba0ce2bd0e95bdb1b8c8e81", // opening story
		25: "5cc99afac6a5217ee0a9dbb562e690fc234f8c8c045e0910130b7f638df99251", // combat map
	}
	for tap := 1; tap <= 100; tap++ {
		tapBREWReference(t, machine)
		stepBREWReference(t, machine, 120)
		if want, ok := checks[tap]; ok {
			frame := brewFrameHash(machine.Framebuffer())
			if got := hex.EncodeToString(frame[:]); got != want {
				t.Fatalf("frame after tap %d = %s, want %s", tap, got, want)
			}
		}
	}
	before := brewFrameHash(machine.Framebuffer())
	tapBREWControl(t, machine, "right")
	stepBREWReference(t, machine, 120)
	if after := brewFrameHash(machine.Framebuffer()); after == before {
		t.Fatal("combat map did not respond to directional input")
	}
}

func newBREWReferenceMachine(t *testing.T, path string, data []byte) *brewMachine {
	t.Helper()
	factory := NewFactory()
	factory.AllowUntrustedBREW = true
	ctx := context.Background()
	created, err := factory.Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	machine, ok := created.(*brewMachine)
	if !ok {
		t.Fatalf("machine type = %T, want BREW", created)
	}
	if err := machine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return machine
}

func stepBREWReference(t *testing.T, machine *brewMachine, count int) {
	t.Helper()
	ctx := context.Background()
	for frame := 0; frame < count; frame++ {
		if err := machine.StepFrame(ctx); err != nil {
			t.Fatalf("step frame %d/%d: %v", frame, count, err)
		}
	}
}

func tapBREWReference(t *testing.T, machine *brewMachine) {
	tapBREWControl(t, machine, "select")
}

func tapBREWControl(t *testing.T, machine *brewMachine, control string) {
	t.Helper()
	for _, event := range []machinecore.InputEvent{
		{Control: control, Pressed: true},
		{Control: control, Pressed: false, At: time.Millisecond},
	} {
		if err := machine.QueueInput(event); err != nil {
			t.Fatal(err)
		}
	}
}

func brewFrameHash(frame image.Image) [32]byte {
	bounds := frame.Bounds()
	pixels := make([]byte, 0, bounds.Dx()*bounds.Dy()*4)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := frame.At(x, y).RGBA()
			pixels = append(pixels, byte(r>>8), byte(g>>8), byte(b>>8), byte(a>>8))
		}
	}
	return sha256.Sum256(pixels)
}
