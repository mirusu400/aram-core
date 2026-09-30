package application

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader"
)

func virusProductMachine(t *testing.T) machinecore.Machine {
	t.Helper()
	path, data := findAuthorizedPackage(t, GNEX32VirusSHA256)
	machine, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
		Format: string(loader.KindGNEX),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return machine
}

func stepVirusFrames(t *testing.T, machine machinecore.Machine, count int) {
	t.Helper()
	for frame := 0; frame < count; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d of %d: %v", frame, count, err)
		}
	}
}

func pressVirusKey(t *testing.T, machine machinecore.Machine, control string) {
	t.Helper()
	for _, pressed := range []bool{true, false} {
		if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: pressed}); err != nil {
			t.Fatalf("%s pressed=%v: %v", control, pressed, err)
		}
	}
}

func TestGNEX32VirusProductSplash(t *testing.T) {
	machine := virusProductMachine(t)
	stepVirusFrames(t, machine, 9)
	frame := machine.Framebuffer()
	if frame == nil || frame.Bounds().Size() != image.Pt(120, 149) {
		t.Fatalf("splash bounds = %v", frame)
	}
	if counts, ok := machine.(interface{ GNEX32PresentCount() uint64 }); !ok || counts.GNEX32PresentCount() != 1 {
		t.Fatalf("GNEX frame count: %v %v", counts, ok)
	}
}

func TestGNEX32VirusMenuAndHelp(t *testing.T) {
	machine := virusProductMachine(t)
	stepVirusFrames(t, machine, 600)
	splash := brewFrameHash(machine.Framebuffer())
	pressVirusKey(t, machine, "select")
	stepVirusFrames(t, machine, 1200)
	if title := brewFrameHash(machine.Framebuffer()); title == splash {
		t.Fatal("select did not advance from splash to title")
	}
	pressVirusKey(t, machine, "select")
	stepVirusFrames(t, machine, 600)
	menu := brewFrameHash(machine.Framebuffer())
	pressVirusKey(t, machine, "down")
	stepVirusFrames(t, machine, 30)
	pressVirusKey(t, machine, "down")
	stepVirusFrames(t, machine, 30)
	pressVirusKey(t, machine, "select")
	stepVirusFrames(t, machine, 600)
	if help := brewFrameHash(machine.Framebuffer()); help == menu {
		t.Fatal("HELP did not advance from menu")
	}
}

func advanceVirusToGameplay(t *testing.T, machine machinecore.Machine) {
	t.Helper()
	stepVirusFrames(t, machine, 600)
	pressVirusKey(t, machine, "select")
	stepVirusFrames(t, machine, 1200)
	pressVirusKey(t, machine, "select")
	stepVirusFrames(t, machine, 600)
	pressVirusKey(t, machine, "select") // GAME START
	stepVirusFrames(t, machine, 600)
	if ink := virusDialogueInk(machine.Framebuffer(), image.Rect(8, 60, 112, 85)); ink < 10 {
		t.Fatalf("opening dialogue contains only %d text pixels", ink)
	}
	for page := 0; page < 19; page++ {
		pressVirusKey(t, machine, "select")
		stepVirusFrames(t, machine, 15)
	}
	stepVirusFrames(t, machine, 600)
	if ink := virusDialogueInk(machine.Framebuffer(), image.Rect(4, 112, 108, 134)); ink < 10 {
		t.Fatalf("scene dialogue contains only %d text pixels", ink)
	}
	for page := 0; page < 3; page++ {
		pressVirusKey(t, machine, "select") // advance the opening scene dialogue
		stepVirusFrames(t, machine, 15)
	}
	stepVirusFrames(t, machine, 30)
}

func virusDialogueInk(frame image.Image, area image.Rectangle) int {
	if frame == nil {
		return 0
	}
	count := 0
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			pixel := color.RGBAModel.Convert(frame.At(x, y)).(color.RGBA)
			if pixel.R >= 100 && pixel.G >= 70 && pixel.G > pixel.B {
				count++
			}
		}
	}
	return count
}

func TestGNEX32VirusProductGameplayRespondsToDirection(t *testing.T) {
	left := virusProductMachine(t)
	right := virusProductMachine(t)
	advanceVirusToGameplay(t, left)
	advanceVirusToGameplay(t, right)
	if frame := left.Framebuffer(); frame == nil || frame.Bounds().Size() != image.Pt(120, 149) {
		t.Fatalf("gameplay framebuffer = %v", frame)
	}
	before := brewFrameHash(left.Framebuffer())
	if before != brewFrameHash(right.Framebuffer()) {
		t.Fatal("identical game openings diverged before input")
	}
	pressVirusKey(t, left, "left")
	pressVirusKey(t, right, "right")
	stepVirusFrames(t, left, 200)
	stepVirusFrames(t, right, 200)
	if brewFrameHash(left.Framebuffer()) == brewFrameHash(right.Framebuffer()) {
		t.Fatal("opposite gameplay directions produced identical frames")
	}
}
