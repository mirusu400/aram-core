package application

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/brewrt"
	machinecore "github.com/mirusu400/aram-core/core"
)

type panicReaderAt struct{}

func TestBREWFrameQuantumMatchesStepFrameClock(t *testing.T) {
	machine := newBREWMachine(machinecore.Source{Name: "synthetic.zip"}, brewrt.Package{})
	if got := machine.FrameQuantum(); got != 16*time.Millisecond {
		t.Fatalf("BREW frame quantum = %v, want 16ms", got)
	}
}

func TestBREWEmptyGenerationChangeEmitsOneMarker(t *testing.T) {
	runtime, err := brewrt.New(brewrt.Package{Module: make([]byte, 20)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	machine := &brewMachine{runtime: runtime, audioGeneration: 1}
	machine.resetBREWAudioLocked(time.Second)
	marker := machine.DrainAudio()
	if marker.Generation != 2 || len(marker.PCM16) != 0 || marker.StartGuestNS != int64(time.Second) {
		t.Fatalf("BREW reset did not expose a discontinuity: %+v", marker)
	}
	if err := marker.Validate(); err != nil {
		t.Fatal(err)
	}
	if extra := machine.DrainAudio(); extra.Generation != 0 || len(extra.PCM16) != 0 {
		t.Fatal("BREW reset exposed a duplicate discontinuity")
	}
}

func genericBREWArchiveForTest(t *testing.T) []byte {
	t.Helper()
	module := make([]byte, 8)
	binary.LittleEndian.PutUint32(module, 0xe92d400c)
	mif := make([]byte, 64)
	for offset, value := range map[int]uint32{0: 0x00010011, 4: 0x10001, 8: 32, 12: 8, 16: 40, 20: 1, 24: 48, 28: 16} {
		binary.LittleEndian.PutUint32(mif[offset:], value)
	}
	binary.LittleEndian.PutUint32(mif[40:], 48)
	binary.LittleEndian.PutUint32(mif[44:], 64)
	binary.LittleEndian.PutUint32(mif[48:], 0x01023456)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, data := range map[string][]byte{
		"game.mif":     mif,
		"bin/game.mod": module,
		"bin/game.sig": []byte("unverified carrier signature"),
	} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

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
	snapshot := machine.DebugSnapshot(10)
	if snapshot.HostTrace.Total != 2 || len(snapshot.HostTrace.Entries) != 2 ||
		!strings.Contains(snapshot.HostTrace.Entries[0], "control=up pressed=false") ||
		!strings.Contains(snapshot.HostTrace.Entries[1], "control=up pressed=true") {
		t.Fatalf("BREW input history = %+v", snapshot.HostTrace)
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

func TestBREWKeyCodesMatchAEEVirtualKeys(t *testing.T) {
	tests := map[string]uint32{
		"up":         0xe031,
		"down":       0xe032,
		"left":       0xe033,
		"right":      0xe034,
		"ok":         0xe035,
		"fire":       0xe035,
		"select":     0xe035,
		"soft-left":  0xe036,
		"soft-right": 0xe037,
		"menu":       0xe03e,
		"back":       0xe030,
		"clear":      0xe030,
		"send":       0xe02f,
		"end":        0xe02e,
		"star":       0xe02b,
		"hash":       0xe02c,
		"pound":      0xe02c,
		"num0":       0xe021,
		"num1":       0xe022,
		"num2":       0xe023,
		"num3":       0xe024,
		"num4":       0xe025,
		"num5":       0xe026,
		"num6":       0xe027,
		"num7":       0xe028,
		"num8":       0xe029,
		"num9":       0xe02a,
	}
	for control, want := range tests {
		got, ok := brewKeyCode(control)
		if !ok || got != want {
			t.Errorf("brewKeyCode(%q) = %#x, %t; want %#x, true", control, got, ok, want)
		}
	}
	if got, ok := brewKeyCode("volume-up"); ok || got != 0 {
		t.Fatalf("unknown BREW key = %#x, %t; want 0, false", got, ok)
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

func TestBREWFactoryRejectsOversizeBeforeReading(t *testing.T) {
	_, matched, err := NewFactory().createBREWMachine(context.Background(), machinecore.Source{
		Name: "oversized.zip", ReaderAt: panicReaderAt{}, Size: maxBREWArchiveSize + 1,
	})
	if err != nil || matched {
		t.Fatalf("oversize source matched=%v err=%v", matched, err)
	}
	var _ io.ReaderAt = panicReaderAt{}
}

func TestBREWFactoryRequiresExplicitOptInForUnverifiedPackage(t *testing.T) {
	data := genericBREWArchiveForTest(t)
	source := machinecore.Source{Name: "generic.zip", ReaderAt: bytes.NewReader(data), Size: int64(len(data))}
	if _, matched, err := NewFactory().createBREWMachine(context.Background(), source); err != nil || matched {
		t.Fatalf("default generic BREW matched=%v err=%v", matched, err)
	}
	factory := NewFactory()
	factory.AllowUntrustedBREW = true
	machine, matched, err := factory.createBREWMachine(context.Background(), source)
	if err != nil || !matched || machine == nil {
		t.Fatalf("opted-in generic BREW machine=%T matched=%v err=%v", machine, matched, err)
	}
}

func TestBREWFaultDebugBundleHasCPUAndMemory(t *testing.T) {
	data := genericBREWArchiveForTest(t)
	source := machinecore.Source{Name: "generic.zip", ReaderAt: bytes.NewReader(data), Size: int64(len(data))}
	factory := NewFactory()
	factory.AllowUntrustedBREW = true
	created, matched, err := factory.createBREWMachine(context.Background(), source)
	if err != nil || !matched {
		t.Fatalf("create BREW machine: matched=%t err=%v", matched, err)
	}
	machine := created.(*brewMachine)
	defer machine.Close()
	if regions := machine.DebugMemoryRegions(4096); regions != nil {
		t.Fatalf("unfaulted BREW memory regions = %+v", regions)
	}
	if err := machine.Start(context.Background()); err == nil {
		t.Fatal("synthetic module unexpectedly started")
	}
	if machine.State() != machinecore.StateFaulted {
		t.Fatalf("state = %s, want faulted", machine.State())
	}
	snapshot := machine.DebugSnapshot(4)
	if snapshot.Runtime != "brew" || snapshot.State != "faulted" || snapshot.CPU == nil || snapshot.LastResult == nil {
		t.Fatalf("BREW fault snapshot lacks CPU or result: %+v", snapshot)
	}
	regions := machine.DebugMemoryRegions(4096)
	if len(regions) == 0 {
		t.Fatal("BREW fault has no readable debug memory")
	}
	readable := 0
	for _, region := range regions {
		readable += len(region.Data)
	}
	if readable > 4096 {
		t.Fatalf("BREW fault debug memory = %d bytes, want at most 4096", readable)
	}
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
