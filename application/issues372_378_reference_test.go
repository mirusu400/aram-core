package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/brewrt"
	machinecore "github.com/mirusu400/aram-core/core"
)

const (
	tengaiBREWSHA256 = "01f8fe8784138792822df9306127279f038813ea62c00385d256b77b61a025cf"
	theSINBREWSHA256 = "8b33b7b3c20dabfe38cdfc905c482ef179827cf8b55b47c9057f60995bea96aa"
)

func TestIssue372TengaiBREWPublishesAudio(t *testing.T) {
	path, data := findAuthorizedPackage(t, tengaiBREWSHA256)
	factory := NewFactory()
	factory.AllowUntrustedBREW = true
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine, ok := created.(*brewMachine)
	if !ok {
		t.Fatalf("machine type = %T, want BREW", created)
	}
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, event := range []machinecore.InputEvent{
		{Control: "select", Pressed: true, At: 1600 * time.Millisecond},
		{Control: "select", Pressed: false, At: 1640 * time.Millisecond},
		{Control: "down", Pressed: true, At: 2200 * time.Millisecond},
		{Control: "down", Pressed: false, At: 2240 * time.Millisecond},
		{Control: "down", Pressed: true, At: 2400 * time.Millisecond},
		{Control: "down", Pressed: false, At: 2440 * time.Millisecond},
		{Control: "select", Pressed: true, At: 3000 * time.Millisecond},
		{Control: "select", Pressed: false, At: 3040 * time.Millisecond},
		{Control: "right", Pressed: true, At: 3400 * time.Millisecond},
		{Control: "right", Pressed: false, At: 3440 * time.Millisecond},
	} {
		if err := machine.QueueInput(event); err != nil {
			t.Fatal(err)
		}
	}
	nonzero := 0
	for frame := 0; frame < 1200 && nonzero == 0; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		chunk := machine.DrainAudio()
		if err := chunk.Validate(); err != nil {
			t.Fatalf("frame %d audio: %v", frame, err)
		}
		for _, sample := range chunk.PCM16 {
			if sample != 0 {
				nonzero++
			}
		}
	}
	if nonzero == 0 {
		t.Fatal("Tengai produced no audible BREW PCM after enabling sound")
	}
}

func TestIssue378TheSINSelectsPackedKoreanText(t *testing.T) {
	_, data := findAuthorizedPackage(t, theSINBREWSHA256)
	pkg, matched, err := brewrt.Match(data)
	if err != nil || !matched {
		t.Fatalf("match The SIN: matched=%v err=%v", matched, err)
	}
	if !pkg.PreferPackedAECHAR {
		t.Fatal("The SIN did not select packed Korean AECHAR decoding")
	}
}
