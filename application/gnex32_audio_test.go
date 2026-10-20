package application

import (
	"context"
	"encoding/binary"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32MachinePublishesAudioAndResetMarker(t *testing.T) {
	machine := newApplicationGNEX32AudioMachine(t)

	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := machine.DrainAudio()
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	if first.SampleRate != 44_100 || first.Channels != 1 || len(first.PCM16) == 0 ||
		first.StartGuestNS != 0 || first.StartSample != 0 || first.Generation != 1 {
		t.Fatalf("first audio chunk = %+v", first)
	}

	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := machine.DrainAudio()
	if err := second.Validate(); err != nil {
		t.Fatal(err)
	}
	if second.Generation != 1 || second.StartSample != uint64(len(first.PCM16)) {
		t.Fatalf("second audio timeline = generation %d sample %d, want 1/%d",
			second.Generation, second.StartSample, len(first.PCM16))
	}

	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	marker := machine.DrainAudio()
	if err := marker.Validate(); err != nil {
		t.Fatal(err)
	}
	if marker.SampleRate != 44_100 || marker.Channels != 1 || len(marker.PCM16) != 0 ||
		marker.StartGuestNS != 0 || marker.StartSample != 0 || marker.Generation != 2 {
		t.Fatalf("reset audio marker = %+v", marker)
	}
	if next := machine.DrainAudio(); next.Generation != 0 {
		t.Fatalf("reset marker repeated: %+v", next)
	}
}

func TestGNEX32MachineStopPublishesAudioMarker(t *testing.T) {
	machine := newApplicationGNEX32AudioMachine(t)
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if chunk := machine.DrainAudio(); len(chunk.PCM16) == 0 {
		t.Fatal("machine produced no audio before stop")
	}
	if err := machine.Stop(); err != nil {
		t.Fatal(err)
	}
	marker := machine.DrainAudio()
	if err := marker.Validate(); err != nil {
		t.Fatal(err)
	}
	if marker.Generation != 2 || len(marker.PCM16) != 0 ||
		marker.SampleRate != 44_100 || marker.Channels != 1 {
		t.Fatalf("stop audio marker = %+v", marker)
	}
	if next := machine.DrainAudio(); next.Generation != 0 {
		t.Fatalf("stop marker repeated: %+v", next)
	}
}

func TestGNEX32MachineClosePublishesAudioMarkerAndKeepsDiagnosticsSafe(t *testing.T) {
	machine := newApplicationGNEX32AudioMachine(t)
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if chunk := machine.DrainAudio(); len(chunk.PCM16) == 0 {
		t.Fatal("machine produced no audio before close")
	}
	if err := machine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := machine.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
	marker := machine.DrainAudio()
	if err := marker.Validate(); err != nil {
		t.Fatal(err)
	}
	if marker.Generation != 2 || len(marker.PCM16) != 0 {
		t.Fatalf("close audio marker = %+v", marker)
	}
	_ = machine.Framebuffer()
	_ = machine.GNEX32PresentCount()

	empty := &gnex32Machine{}
	if empty.Framebuffer() != nil || empty.GNEX32PresentCount() != 0 {
		t.Fatal("empty machine diagnostics are not zero-valued")
	}
}

func newApplicationGNEX32AudioMachine(t *testing.T) *gnex32Machine {
	t.Helper()
	image := applicationGNEX32AudioImage()
	runtime, err := gnex32.NewRuntime(image, gnex32VirusIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.RunMain(3); err != nil {
		t.Fatal(err)
	}
	return &gnex32Machine{
		state:               machinecore.StateRunning,
		image:               image,
		runtime:             runtime,
		budget:              1,
		audioGeneration:     1,
		mediaOutputRevision: runtime.AudioOutputRevision(),
	}
}

func applicationGNEX32AudioImage() gnex.GNEX32Image {
	code := make([]byte, 10)
	for offset, word := range map[int]uint16{
		0: 0x06,
		2: 0,
		4: 0x92,
		6: 0x82,
		8: 0x86,
	} {
		binary.LittleEndian.PutUint16(code[offset:], word)
	}
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	return gnex.GNEX32Image{
		Header:  gnex.Header{BodyOffset: 160},
		Entry:   160,
		Code:    code,
		Symbols: symbols,
		Media:   []gnex.GNEX32Media{{Flags: 0x200, Data: presentationTestWave()}},
	}
}
