package system

import (
	"errors"
	"testing"
)

func TestParallelPanelInterfaceSeparatesCommandAndDataPorts(t *testing.T) {
	panel := NewParallelPanelInterface()
	if err := panel.Write(0, Width16, 0xf0); err != nil {
		t.Fatal(err)
	}
	if err := panel.Write(ParallelPanelDataOffset, Width16, 0x5a); err != nil {
		t.Fatal(err)
	}
	commands, data := panel.WriteCounts()
	if panel.CurrentCommand() != 0xf0 || panel.LastData() != 0x5a ||
		commands != 1 || data != 1 {
		t.Fatalf(
			"parallel panel state = command %#x data %#x counts %d/%d",
			panel.CurrentCommand(), panel.LastData(), commands, data,
		)
	}
	if err := panel.Write(4, Width16, 0); !errors.Is(err, ErrParallelPanelMMIO) {
		t.Fatalf("unknown panel-port error = %v", err)
	}
	if _, err := panel.Read(0, Width16); !errors.Is(err, ErrParallelPanelMMIO) {
		t.Fatalf("unsupported panel read error = %v", err)
	}
	if err := panel.Write(0, Width32, 0); !errors.Is(err, ErrParallelPanelMMIO) {
		t.Fatalf("wrong-width panel write error = %v", err)
	}
}

func TestParallelPanelInterfaceStateRoundTripAndReset(t *testing.T) {
	panel := NewParallelPanelInterface()
	_ = panel.Write(0, Width16, 0x11)
	_ = panel.Write(ParallelPanelDataOffset, Width16, 0x22)
	state, err := panel.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restored := NewParallelPanelInterface()
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if restored.CurrentCommand() != 0x11 || restored.LastData() != 0x22 {
		t.Fatal("parallel panel state did not round trip")
	}
	if err := restored.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated parallel panel state error = %v", err)
	}
	if err := restored.Reset(); err != nil {
		t.Fatal(err)
	}
	commands, data := restored.WriteCounts()
	if restored.CurrentCommand() != 0 || restored.LastData() != 0 || commands != 0 || data != 0 {
		t.Fatal("parallel panel reset retained state")
	}
}
