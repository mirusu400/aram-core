package application

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader"
)

func readyVirusProductMachine(t *testing.T) machinecore.Machine {
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
	return machine
}

func virusProductMachine(t *testing.T) machinecore.Machine {
	t.Helper()
	machine := readyVirusProductMachine(t)
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

func TestGNEX32VirusProductBackPersistsUserNV(t *testing.T) {
	machine := virusProductMachine(t)
	stepVirusFrames(t, machine, 600)
	pressVirusKey(t, machine, "select")
	stepVirusFrames(t, machine, 1200)
	pressVirusKey(t, machine, "back")
	stepVirusFrames(t, machine, 1)
}

func TestGNEX32VirusMenuAndHelp(t *testing.T) {
	machine := virusProductMachine(t)
	splash, title := advanceVirusToMenu(t, machine)
	if title == splash {
		t.Fatal("select did not advance from splash to title")
	}
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

func advanceVirusToMenu(t *testing.T, machine machinecore.Machine) ([32]byte, [32]byte) {
	t.Helper()
	stepVirusFrames(t, machine, 600)
	splash := brewFrameHash(machine.Framebuffer())
	pressVirusKey(t, machine, "select")
	stepVirusFrames(t, machine, 1200)
	title := brewFrameHash(machine.Framebuffer())
	pressVirusKey(t, machine, "select")
	stepVirusFrames(t, machine, 600)
	return splash, title
}

func TestGNEX32VirusMenuBranchProbe(t *testing.T) {
	if os.Getenv("ARAM_TEST_DATA") == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	selectedFrames := make(map[[32]byte]bool)
	resultFrames := make(map[[32]byte]bool)
	reachedServices := make(map[uint16]bool)
	var firstResult [32]byte
	for selection := 0; selection <= 6; selection++ {
		t.Run(fmt.Sprintf("selection-%d", selection), func(t *testing.T) {
			machine := tracedVirusProductMachine(t)
			advanceVirusToMenu(t, machine)
			for move := 0; move < selection; move++ {
				pressVirusKey(t, machine, "down")
				stepVirusFrames(t, machine, 30)
			}
			selected := brewFrameHash(machine.Framebuffer())
			pressVirusKey(t, machine, "select")
			product := machine.(*gnex32Machine)
			if product.state == machinecore.StateRunning {
				stepVirusFrames(t, machine, 600)
			}
			if product.state != machinecore.StateRunning {
				t.Fatalf("selection %d ended in %s", selection, product.state)
			}
			trace := product.GNEX32ExecutionTrace()
			result := brewFrameHash(machine.Framebuffer())
			for _, service := range trace.Services {
				reachedServices[service.ID] = true
			}
			if selection < 6 {
				selectedFrames[selected] = true
				resultFrames[result] = true
			} else if result != firstResult {
				t.Fatalf("six downward moves did not wrap to first menu branch: %x != %x", result[:4], firstResult[:4])
			}
			if selection == 0 {
				firstResult = result
			}
			t.Logf("selection=%d state=%s selected=%x result=%x instructions=%d services=%d",
				selection, product.state, selected[:4], result[:4], trace.InstructionCount, len(trace.Services))
		})
	}
	if len(selectedFrames) != 6 || len(resultFrames) < 5 {
		t.Fatalf("menu coverage: selected=%d result branches=%d", len(selectedFrames), len(resultFrames))
	}
	t.Logf("menu coverage: entries=%d result branches=%d reached services=%d", len(selectedFrames), len(resultFrames), len(reachedServices))
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

func TestGNEX32VirusAutomatedInputExecutionTrace(t *testing.T) {
	t.Run("menu-help", func(t *testing.T) {
		machine := tracedVirusProductMachine(t)
		stepVirusFrames(t, machine, 600)
		pressVirusKey(t, machine, "select")
		stepVirusFrames(t, machine, 1200)
		pressVirusKey(t, machine, "select")
		stepVirusFrames(t, machine, 600)
		pressVirusKey(t, machine, "down")
		stepVirusFrames(t, machine, 30)
		pressVirusKey(t, machine, "down")
		stepVirusFrames(t, machine, 30)
		pressVirusKey(t, machine, "select")
		stepVirusFrames(t, machine, 600)
		assertVirusExecutionTrace(t, machine)
	})

	t.Run("gameplay", func(t *testing.T) {
		machine := tracedVirusProductMachine(t)
		advanceVirusToGameplay(t, machine)
		for _, control := range []string{"left", "right", "up", "down", "select"} {
			pressVirusKey(t, machine, control)
			stepVirusFrames(t, machine, 120)
		}
		assertVirusExecutionTrace(t, machine)
		assertVirusSoundTrace(t, machine)
	})
}

func TestGNEX32VirusLongAutomatedGameplayProbe(t *testing.T) {
	machine := tracedVirusProductMachine(t)
	advanceVirusToGameplay(t, machine)
	product := machine.(*gnex32Machine)
	analysis, err := gnex32.AnalyzeServices(product.image)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.UnknownOpcodes) != 0 || len(analysis.UnsupportedServices) != 0 {
		t.Fatalf("static capability gaps: opcodes=%v services=%v", analysis.UnknownOpcodes, analysis.UnsupportedServices)
	}

	frames := map[[32]byte]bool{brewFrameHash(machine.Framebuffer()): true}
	controls := []string{"left", "right", "up", "down", "select"}
	var audioSamples int
	for cycle := 0; cycle < 160; cycle++ {
		control := controls[cycle%len(controls)]
		if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}); err != nil {
			t.Fatalf("cycle %d press %s: %v", cycle, control, err)
		}
		stepVirusFrames(t, machine, 45) // includes the deterministic long-key repeat boundary
		if err := machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}); err != nil {
			t.Fatalf("cycle %d release %s: %v", cycle, control, err)
		}
		stepVirusFrames(t, machine, 15)
		frames[brewFrameHash(machine.Framebuffer())] = true
		for {
			chunk := machine.DrainAudio()
			if len(chunk.PCM16) == 0 {
				break
			}
			if err := chunk.Validate(); err != nil {
				t.Fatalf("cycle %d audio: %v", cycle, err)
			}
			audioSamples += len(chunk.PCM16)
		}
	}
	assertVirusExecutionTrace(t, machine)
	trace := product.GNEX32ExecutionTrace()
	reachedServices := make(map[uint16]bool, len(trace.Services))
	for _, service := range trace.Services {
		reachedServices[service.ID] = true
	}
	missingServices := make([]uint16, 0)
	for _, service := range analysis.ServiceCalls {
		if !reachedServices[service.ID] {
			missingServices = append(missingServices, service.ID)
		}
	}
	if trace.InstructionCount < 1_000_000 || len(frames) < 10 || audioSamples == 0 {
		t.Fatalf("long probe coverage: instructions=%d frames=%d audio_samples=%d", trace.InstructionCount, len(frames), audioSamples)
	}
	t.Logf("long probe: instructions=%d opcodes=%d services=%d/%d missing_services=%#x frames=%d audio_samples=%d",
		trace.InstructionCount, len(trace.Opcodes), len(trace.Services), len(analysis.ServiceCalls), missingServices, len(frames), audioSamples)
}

func tracedVirusProductMachine(t *testing.T) machinecore.Machine {
	t.Helper()
	machine := readyVirusProductMachine(t)
	tracer, ok := machine.(interface{ EnableGNEX32ExecutionTrace() })
	if !ok {
		t.Fatalf("GNEX32 machine %T has no execution trace", machine)
	}
	tracer.EnableGNEX32ExecutionTrace()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return machine
}

func assertVirusExecutionTrace(t *testing.T, machine machinecore.Machine) {
	t.Helper()
	tracer, ok := machine.(interface{ GNEX32ExecutionTrace() gnex32.ExecutionTrace })
	if !ok {
		t.Fatalf("GNEX32 machine %T has no execution trace snapshot", machine)
	}
	trace := tracer.GNEX32ExecutionTrace()
	if trace.InstructionCount < 1000 || len(trace.Opcodes) < 10 || len(trace.Services) < 10 {
		t.Fatalf("execution trace is unexpectedly small: instructions=%d opcodes=%d services=%d",
			trace.InstructionCount, len(trace.Opcodes), len(trace.Services))
	}
	for _, service := range trace.Services {
		name, supported := gnex32.ServiceName(service.ID)
		if !supported || name == "" || service.Name != name {
			t.Errorf("reached unsupported service %#x (%q)", service.ID, service.Name)
		}
		if service.Count == 0 || len(service.Calls) == 0 ||
			uint64(len(service.Calls))+service.DroppedSamples != service.Count {
			t.Errorf("service %#x has inconsistent samples: %+v", service.ID, service)
		}
		for _, call := range service.Calls {
			if call.Offset < 0 || call.StackDepth < len(call.Stack) ||
				call.StackBase != call.StackDepth-len(call.Stack) {
				t.Errorf("service %#x has invalid stack sample: %+v", service.ID, call)
			}
		}
	}
}

func assertVirusSoundTrace(t *testing.T, machine machinecore.Machine) {
	t.Helper()
	tracer := machine.(interface{ GNEX32ExecutionTrace() gnex32.ExecutionTrace })
	trace := tracer.GNEX32ExecutionTrace()
	played := make(map[uint32]bool)
	var stops uint64
	for _, service := range trace.Services {
		switch service.ID {
		case 0x82:
			for _, call := range service.Calls {
				if call.StackDepth != 1 || len(call.Stack) != 1 || call.Stack[0].Reference {
					t.Fatalf("PlaySound stack = %+v", call)
				}
				played[call.Stack[0].Value] = true
				product := machine.(*gnex32Machine)
				index := int(call.Stack[0].Value)
				if index < 0 || index >= len(product.image.Media) || product.image.Media[index].Flags != 0x200 {
					t.Fatalf("PlaySound selected non-sound media %d", index)
				}
			}
		case 0x83:
			stops = service.Count
		}
	}
	if len(played) < 4 || stops == 0 {
		t.Fatalf("gameplay sound routes = %v, StopSound calls = %d", played, stops)
	}

	var chunks, samples int
	var peak int16
	var previousStart int64 = -1
	for drain := 0; drain < 128; drain++ {
		chunk := machine.DrainAudio()
		if len(chunk.PCM16) == 0 {
			break
		}
		if err := chunk.Validate(); err != nil {
			t.Fatal(err)
		}
		if chunk.StartGuestNS < previousStart {
			t.Fatalf("audio timestamp moved backward: %d after %d", chunk.StartGuestNS, previousStart)
		}
		previousStart = chunk.StartGuestNS
		chunks++
		samples += len(chunk.PCM16)
		for _, sample := range chunk.PCM16 {
			absolute := sample
			if absolute < 0 {
				absolute = -absolute
			}
			if absolute > peak {
				peak = absolute
			}
		}
	}
	if chunks == 0 || samples == 0 || peak == 0 {
		t.Fatalf("gameplay audio = chunks %d samples %d peak %d", chunks, samples, peak)
	}
}
