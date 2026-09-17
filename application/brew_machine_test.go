package application

import (
	"context"
	"errors"
	"image"
	"image/color"
	"io"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/brewrt"
	machinecore "github.com/mirusu400/aram-core/core"
)

type panicReaderAt struct{}

func (panicReaderAt) ReadAt([]byte, int64) (int, error) {
	panic("unexpected source read")
}

func TestBREWMachineQueuesPressAndRelease(t *testing.T) {
	machine := newBREWMachine(machinecore.Source{Name: "synthetic.zip"}, brewrt.Package{})
	press := machinecore.InputEvent{Control: "up", Pressed: true, At: time.Millisecond}
	release := machinecore.InputEvent{Control: "up", Pressed: false, At: 2 * time.Millisecond}
	if err := machine.QueueInput(release); err != nil {
		t.Fatal(err)
	}
	if err := machine.QueueInput(press); err != nil {
		t.Fatal(err)
	}
	if len(machine.input) != 2 || machine.input[0] != press || machine.input[1] != release {
		t.Fatalf("queued transitions = %#v, want press then release", machine.input)
	}
}

func TestBREWMachineInputScheduleUsesGuestElapsedTime(t *testing.T) {
	input := []machinecore.InputEvent{
		{Control: "up", Pressed: true, At: 16 * time.Millisecond},
		{Control: "up", Pressed: false, At: 48 * time.Millisecond},
	}
	if got := dueBREWInputCount(input, 32*time.Millisecond); got != 1 {
		t.Fatalf("due input count at 32ms = %d, want 1", got)
	}
}

func TestBREWExecutionBoundaryPausesMachine(t *testing.T) {
	machine := newBREWMachine(machinecore.Source{Name: "synthetic.zip"}, brewrt.Package{})
	machine.state = machinecore.StateRunning
	err := machine.executionErrorLocked("timer", &brewrt.ExecutionBoundaryError{Interface: "IShell", MethodSlot: 99})
	var boundary *brewrt.ExecutionBoundaryError
	if !errors.As(err, &boundary) || machine.state != machinecore.StatePaused {
		t.Fatalf("boundary error=%v state=%s, want typed boundary and paused", err, machine.state)
	}
}

func TestBREWFactoryRejectsWrongSizeBeforeReading(t *testing.T) {
	_, matched, err := NewFactory().createBREWMachine(context.Background(), machinecore.Source{
		Name: "oversized.zip", ReaderAt: panicReaderAt{}, Size: brewrt.ArchiveSize + 1,
	})
	if err != nil || matched {
		t.Fatalf("wrong-size source matched=%v err=%v", matched, err)
	}
	var _ io.ReaderAt = panicReaderAt{}
}

func TestBREWMachineRendersPackageSplashNonUniformly(t *testing.T) {
	splash := image.NewRGBA(image.Rect(0, 0, 2, 1))
	splash.SetRGBA(0, 0, color.RGBA{R: 0xff, A: 0xff})
	splash.SetRGBA(1, 0, color.RGBA{B: 0xff, A: 0xff})
	machine := newBREWMachine(machinecore.Source{Name: "synthetic.zip"}, brewrt.Package{Splash: splash})
	machine.renderSplash()
	if frameIsUniform(machine.Framebuffer()) {
		t.Fatal("package splash frame is uniform")
	}
}

func TestBREWMachineDoesNotPublishMIFSplashBeforeGuestUpdate(t *testing.T) {
	splash := image.NewRGBA(image.Rect(0, 0, 2, 1))
	splash.SetRGBA(0, 0, color.RGBA{R: 0xff, A: 0xff})
	splash.SetRGBA(1, 0, color.RGBA{B: 0xff, A: 0xff})
	machine := newBREWMachine(machinecore.Source{Name: "synthetic.zip"}, brewrt.Package{Splash: splash})
	if got := machine.Framebuffer().Bounds(); got != image.Rect(0, 0, 120, 160) {
		t.Fatalf("initial BREW framebuffer bounds = %v", got)
	}
	if !frameIsUniform(machine.Framebuffer()) {
		t.Fatal("MIF splash was published before a guest IDisplay Update")
	}
}

func frameIsUniform(frame image.Image) bool {
	bounds := frame.Bounds()
	first := color.RGBAModel.Convert(frame.At(bounds.Min.X, bounds.Min.Y)).(color.RGBA)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if color.RGBAModel.Convert(frame.At(x, y)).(color.RGBA) != first {
				return false
			}
		}
	}
	return true
}
