package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/gnex32"
	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestGNEX32VideoPresentationUsesGuestTimeline(t *testing.T) {
	machine := newApplicationGNEX32PresentationMachine(t)
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := machine.VideoPresentation()
	if first.Image == nil || first.Sequence != 1 || first.GuestNS != 0 || first.Generation != 1 {
		t.Fatalf("first video presentation = %+v", first)
	}
	legacyImage, legacySequence := machine.FramePresentation()
	if legacyImage != first.Image || legacySequence != first.Sequence {
		t.Fatal("legacy and timestamped GNEX32 presentations diverged")
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.runtime.PresentCount() != 2 {
		t.Fatalf("identical timer Flush count = %d, want 2", machine.runtime.PresentCount())
	}
	advanced := machine.VideoPresentation()
	if advanced.Image != first.Image || advanced.Sequence != first.Sequence ||
		advanced.GuestNS != int64(machine.runtime.AudioTime()) || advanced.GuestNS <= first.GuestNS ||
		advanced.Generation != first.Generation {
		t.Fatalf("advanced video presentation = %+v, first = %+v", advanced, first)
	}

	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	restored := machine.VideoPresentation()
	if restored.Image == nil || restored.Sequence != 1 || restored.GuestNS != advanced.GuestNS ||
		restored.Generation != 2 {
		t.Fatalf("restored video presentation = %+v", restored)
	}
	if restored.Image == first.Image {
		t.Fatal("load reused a host frame from the previous generation")
	}

	if err := machine.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	reset := machine.VideoPresentation()
	if reset.Image != nil || reset.Sequence != 0 || reset.GuestNS != 0 || reset.Generation != 3 {
		t.Fatalf("reset video presentation = %+v", reset)
	}
}

func newApplicationGNEX32PresentationMachine(t *testing.T) *gnex32Machine {
	t.Helper()
	words := []uint16{
		0x05, 16, // pushc timer period
		0x05, 0, // pushc one-shot
		0x92, 0x95, // SetTimer0
		0x05, 0, // pushc palette index 0
		0x92, 0x36, // Clear
		0x92, 0x5f, // Flush
		0x86,       // return
		0x92, 0x5f, // timer callback: identical Flush
		0x86, // return
	}
	code := make([]byte, len(words)*2)
	for index, word := range words {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	symbols := make([]gnex.GNEX32Symbol, 10)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	image := gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160, Code: code, Symbols: symbols,
	}
	image.Callbacks.Timeout = 186
	runtime, err := gnex32.NewRuntime(image, gnex32VirusProfile)
	if err != nil {
		t.Fatal(err)
	}
	machine := &gnex32Machine{
		state: machinecore.StateReady, source: machinecore.Source{SHA256: strings.Repeat("b", 64)},
		image: image, profile: gnex32VirusProfile, runtime: runtime, budget: 16,
		audioGeneration: 1, mediaOutputRevision: runtime.AudioOutputRevision(),
	}
	t.Cleanup(func() { _ = machine.Close() })
	return machine
}
