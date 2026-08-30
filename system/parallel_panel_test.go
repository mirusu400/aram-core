package system

import (
	"errors"
	"testing"
)

func TestParallelPanelInterfaceSeparatesCommandAndDataPorts(t *testing.T) {
	panel := NewParallelPanelInterface()
	var writes []ParallelPanelWrite
	panel.SetWriteObserver(func(write ParallelPanelWrite) {
		writes = append(writes, write)
	})
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
	wantWrites := []ParallelPanelWrite{
		{Command: 0xf0, Value: 0xf0},
		{Command: 0xf0, Value: 0x5a, Data: true},
	}
	if len(writes) != len(wantWrites) {
		t.Fatalf("observed panel writes = %+v", writes)
	}
	for index := range writes {
		if writes[index] != wantWrites[index] {
			t.Fatalf("observed panel write %d = %+v", index, writes[index])
		}
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

func TestParallelPanelSparsePortsShareTransportAndState(t *testing.T) {
	panel := NewParallelPanelInterface()
	command, err := NewParallelPanelCommandPort(panel)
	if err != nil {
		t.Fatal(err)
	}
	data, err := NewParallelPanelDataPort(panel)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Write(0, Width16, 0x2c); err != nil {
		t.Fatal(err)
	}
	if err := data.Write(0, Width16, 0x1234); err != nil {
		t.Fatal(err)
	}
	if panel.CurrentCommand() != 0x2c || panel.LastData() != 0x1234 {
		t.Fatalf("sparse ports left command %#x data %#x", panel.CurrentCommand(), panel.LastData())
	}
	state, err := command.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restoredPanel := NewParallelPanelInterface()
	restoredCommand, _ := NewParallelPanelCommandPort(restoredPanel)
	if err := restoredCommand.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if restoredPanel.CurrentCommand() != 0x2c || restoredPanel.LastData() != 0x1234 {
		t.Fatal("sparse command-port state did not restore shared transport")
	}
	restoredData, _ := NewParallelPanelDataPort(restoredPanel)
	if err := restoredData.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("cross-role sparse-port state error = %v", err)
	}
	if _, err := NewParallelPanelCommandPort(nil); err == nil {
		t.Fatal("accepted nil sparse panel transport")
	}
}

func TestParallelPanelSparseDataPortDoesNotDuplicateTransportState(t *testing.T) {
	controller, err := NewDCSPanelController(DCSPanelConfig{Width: 240, Height: 400, NativeAddressMode: 0x88})
	if err != nil {
		t.Fatal(err)
	}
	panel, err := NewParallelPanelInterfaceWithController(controller)
	if err != nil {
		t.Fatal(err)
	}
	command, err := NewParallelPanelCommandPort(panel)
	if err != nil {
		t.Fatal(err)
	}
	data, err := NewParallelPanelDataPort(panel)
	if err != nil {
		t.Fatal(err)
	}
	commandState, err := command.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	dataState, err := data.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	if len(dataState) != 9 {
		t.Fatalf("sparse data port serialized %d bytes of shared transport state", len(dataState)-9)
	}
	if len(commandState) <= 9 {
		t.Fatal("sparse command port did not serialize the shared transport")
	}
	if err := data.LoadState(dataState); err != nil {
		t.Fatal(err)
	}
	if err := data.LoadState(append(append([]byte(nil), dataState...), 0)); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("sparse data port accepted trailing state: %v", err)
	}
	if err := command.Write(0, Width16, 0x2c); err != nil {
		t.Fatal(err)
	}
	if err := data.Reset(); err != nil {
		t.Fatal(err)
	}
	if panel.CurrentCommand() != 0x2c {
		t.Fatal("sparse data-port reset cleared the shared transport")
	}
	if err := command.Reset(); err != nil {
		t.Fatal(err)
	}
	if panel.CurrentCommand() != 0 {
		t.Fatal("sparse command-port reset did not clear the shared transport")
	}
}
