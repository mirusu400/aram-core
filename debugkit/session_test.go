package debugkit

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

type fakeMachine struct {
	state  machinecore.State
	frame  *image.RGBA
	inputs []machinecore.InputEvent
	steps  int
	closed bool
	result cpu.Result
}

func newFakeMachine() *fakeMachine {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 1))
	frame.SetRGBA(0, 0, color.RGBA{A: 0xff})
	frame.SetRGBA(1, 0, color.RGBA{A: 0xff})
	return &fakeMachine{
		state: machinecore.StateReady,
		frame: frame,
	}
}

func (*fakeMachine) Load(context.Context, machinecore.Source) error {
	return nil
}

func (m *fakeMachine) State() machinecore.State {
	return m.state
}

func (m *fakeMachine) Start(context.Context) error {
	m.state = machinecore.StatePaused
	return nil
}

func (m *fakeMachine) Pause() error {
	m.state = machinecore.StatePaused
	return nil
}

func (m *fakeMachine) Resume() error {
	m.state = machinecore.StatePaused
	return nil
}

func (m *fakeMachine) Stop() error {
	m.state = machinecore.StateStopped
	return nil
}

func (m *fakeMachine) Reset(context.Context) error {
	m.state = machinecore.StateReady
	m.steps = 0
	m.inputs = nil
	m.frame.SetRGBA(0, 0, color.RGBA{A: 0xff})
	return nil
}

func (m *fakeMachine) StepFrame(context.Context) error {
	m.steps++
	m.state = machinecore.StatePaused
	m.result = cpu.Result{
		Reason:       cpu.StopBudget,
		Instructions: uint64(m.steps * 100),
		PC:           uint32(0x1000 + m.steps*2),
	}
	m.frame.SetRGBA(0, 0, color.RGBA{
		R: uint8(m.steps),
		G: 0x20,
		B: 0x40,
		A: 0xff,
	})
	return nil
}

func (m *fakeMachine) QueueInput(event machinecore.InputEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	m.inputs = append(m.inputs, event)
	return nil
}

func (m *fakeMachine) Framebuffer() image.Image {
	snapshot := image.NewRGBA(m.frame.Bounds())
	copy(snapshot.Pix, m.frame.Pix)
	return snapshot
}

func (*fakeMachine) DrainAudio() machinecore.AudioChunk {
	return machinecore.AudioChunk{}
}

func (m *fakeMachine) SaveState(output io.Writer) error {
	_, err := output.Write([]byte{byte(m.steps)})
	return err
}

func (m *fakeMachine) LoadState(input io.Reader) error {
	var state [1]byte
	if _, err := io.ReadFull(input, state[:]); err != nil {
		return err
	}
	m.steps = int(state[0])
	return nil
}

func (m *fakeMachine) Close() error {
	m.closed = true
	m.state = machinecore.StateStopped
	return nil
}

func (m *fakeMachine) ReadRegister(id uint32) (uint32, error) {
	if id > cpu.RegisterCPSR {
		return 0, cpu.ErrInvalidAddress
	}
	if id == cpu.RegisterPC {
		return m.result.PC, nil
	}
	if id == cpu.RegisterCPSR {
		return cpu.StatusThumb, nil
	}
	return id * 0x10, nil
}

func (m *fakeMachine) LastResult() cpu.Result {
	return m.result
}

func TestSessionTapAdvancesVirtualTimeAndCapturesScreen(t *testing.T) {
	machine := newFakeMachine()
	session, err := New(machine, Options{
		Diagnostics: func() map[string]any {
			return map[string]any{
				"wipi": map[string]any{
					"present_count": uint32(3),
					"last_api":      "paint",
				},
				"unimplemented": []string{"missing"},
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.Tap(context.Background(), "ok", 2); err != nil {
		t.Fatal(err)
	}
	if machine.steps != 3 {
		t.Fatalf("machine steps = %d, want 3", machine.steps)
	}
	if len(machine.inputs) != 2 {
		t.Fatalf("input count = %d, want 2", len(machine.inputs))
	}
	if !machine.inputs[0].Pressed || machine.inputs[0].At != 0 {
		t.Fatalf("key down = %+v", machine.inputs[0])
	}
	if machine.inputs[1].Pressed || machine.inputs[1].At != 32*time.Millisecond {
		t.Fatalf("key up = %+v", machine.inputs[1])
	}

	status := session.Status()
	if status.Frame != 3 || status.ElapsedMS != 48 || status.State != "paused" {
		t.Fatalf("status = %+v", status)
	}
	if status.Screen.Width != 2 || status.Screen.Height != 1 {
		t.Fatalf("screen geometry = %+v", status.Screen)
	}
	if status.Screen.NonBlackPixels != 1 || status.Screen.VisiblePixels != 2 {
		t.Fatalf("screen counts = %+v", status.Screen)
	}
	if len(status.Screen.RGBASHA256) != 64 {
		t.Fatalf("screen hash = %q", status.Screen.RGBASHA256)
	}
	if status.CPU == nil ||
		status.CPU.Registers["pc"] != 0x1006 ||
		status.CPU.LastResult.Reason != "budget" ||
		status.CPU.LastResult.Instructions != 300 {
		t.Fatalf("CPU status = %+v", status.CPU)
	}

	pixel, err := session.Pixel(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pixel != (Pixel{R: 3, G: 0x20, B: 0x40, A: 0xff}) {
		t.Fatalf("pixel = %+v", pixel)
	}
	if _, err := session.Pixel(2, 0); err == nil {
		t.Fatal("out-of-bounds pixel unexpectedly succeeded")
	}

	path := filepath.Join(t.TempDir(), "nested", "frame.png")
	report, err := session.Screenshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if report != status.Screen {
		t.Fatalf("screenshot report = %+v, want %+v", report, status.Screen)
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	decoded, err := png.Decode(input)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds() != image.Rect(0, 0, 2, 1) {
		t.Fatalf("decoded bounds = %v", decoded.Bounds())
	}
}

func TestSessionStateFilesAndValidation(t *testing.T) {
	machine := newFakeMachine()
	session, err := New(machine, Options{FrameDuration: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Step(context.Background(), -1); err == nil {
		t.Fatal("negative step count unexpectedly succeeded")
	}
	if err := session.Tap(context.Background(), "ok", 0); err == nil {
		t.Fatal("zero-duration tap unexpectedly succeeded")
	}
	if err := session.Step(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "states", "slot.bin")
	if err := session.SaveState(path); err != nil {
		t.Fatal(err)
	}
	machine.steps = 99
	if err := session.LoadState(path); err != nil {
		t.Fatal(err)
	}
	if machine.steps != 7 {
		t.Fatalf("loaded steps = %d, want 7", machine.steps)
	}
	if err := session.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := session.Status(); status.Frame != 0 || status.ElapsedMS != 0 {
		t.Fatalf("reset status = %+v", status)
	}
}

func TestLuaScenarioEmitsJSONEvents(t *testing.T) {
	machine := newFakeMachine()
	session, err := New(machine, Options{
		Diagnostics: func() map[string]any {
			return map[string]any{
				"wipi": map[string]any{
					"present_count": uint32(3),
					"last_api":      "paint",
				},
				"unimplemented": []string{"missing"},
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	screenshot := filepath.ToSlash(filepath.Join(t.TempDir(), "frame.png"))
	script := `
		aram.start()
		local stepped = aram.step(2)
		assert(stepped.frame == 2)
		local cpu = aram.cpu()
		assert(cpu.registers.pc == 0x1004)
		local runtime = aram.runtime()
		assert(runtime.wipi.present_count == 3)
		assert(runtime.unimplemented[1] == "missing")
		aram.tap("ok", 1)
		local pixel = aram.pixel(0, 0)
		assert(pixel.r == 4)
		local screen = aram.screenshot(` + quoteLua(screenshot) + `)
		aram.assert_screen(screen.rgba_sha256)
		print("done", stepped.state)
	`
	var output bytes.Buffer
	if err := session.RunLua(
		context.Background(),
		"scenario.lua",
		script,
		&output,
		io.Discard,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(screenshot); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 9 {
		t.Fatalf("event lines = %d, want 9:\n%s", len(lines), output.String())
	}
	for index, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line %d is not JSON: %v\n%s", index, err, line)
		}
		if event["event"] == nil {
			t.Fatalf("line %d has no event: %s", index, line)
		}
	}
}

func TestProtocolReportsErrorsInBandAndContinues(t *testing.T) {
	machine := newFakeMachine()
	session, err := New(machine, Options{
		Diagnostics: func() map[string]any {
			return map[string]any{"kind": "fake"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("\ufeff" + strings.Join([]string{
		`{"id":1,"command":"start"}`,
		`{"id":2,"command":"step","count":2}`,
		`{"id":3,"command":"key_down","control":"ok"}`,
		`{"id":4,"command":"key_up","control":"ok"}`,
		`{"id":5,"command":"pixel","x":0,"y":0}`,
		`{"id":6,"command":"cpu"}`,
		`{"id":7,"command":"runtime"}`,
		`{"id":8,"command":"does_not_exist"}`,
		`{"id":9,"command":"status"}`,
		`{"id":10,"command":"quit"}`,
	}, "\n"))
	var output bytes.Buffer
	if err := session.ServeProtocol(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 10 {
		t.Fatalf("response lines = %d, want 10:\n%s", len(lines), output.String())
	}
	for index, line := range lines {
		var response ProtocolResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("response %d: %v", index, err)
		}
		if index == 7 {
			if response.OK || !strings.Contains(response.Error, "unknown command") {
				t.Fatalf("unknown response = %+v", response)
			}
			continue
		}
		if !response.OK {
			t.Fatalf("response %d failed: %+v", index, response)
		}
	}
}

func quoteLua(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
