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
	check(t, panel.Write(0, Width16, 0xf0))
	check(t, panel.Write(ParallelPanelDataOffset, Width16, 0x5a))
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
	check(t, err)
	restored := NewParallelPanelInterface()
	check(t, restored.LoadState(state))
	if restored.CurrentCommand() != 0x11 || restored.LastData() != 0x22 {
		t.Fatal("parallel panel state did not round trip")
	}
	if err := restored.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated parallel panel state error = %v", err)
	}
	check(t, restored.Reset())
	commands, data := restored.WriteCounts()
	if restored.CurrentCommand() != 0 || restored.LastData() != 0 || commands != 0 || data != 0 {
		t.Fatal("parallel panel reset retained state")
	}
}

func TestParallelPanelSparsePortsShareTransportAndState(t *testing.T) {
	panel := NewParallelPanelInterface()
	command, err := NewParallelPanelCommandPort(panel)
	check(t, err)
	data, err := NewParallelPanelDataPort(panel)
	check(t, err)
	check(t, command.Write(0, Width16, 0x2c))
	check(t, data.Write(0, Width16, 0x1234))
	if panel.CurrentCommand() != 0x2c || panel.LastData() != 0x1234 {
		t.Fatalf("sparse ports left command %#x data %#x", panel.CurrentCommand(), panel.LastData())
	}
	state, err := command.SaveState()
	check(t, err)
	restoredPanel := NewParallelPanelInterface()
	restoredCommand, _ := NewParallelPanelCommandPort(restoredPanel)
	check(t, restoredCommand.LoadState(state))
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
	check(t, err)
	panel, err := NewParallelPanelInterfaceWithController(controller)
	check(t, err)
	command, err := NewParallelPanelCommandPort(panel)
	check(t, err)
	data, err := NewParallelPanelDataPort(panel)
	check(t, err)
	commandState, err := command.SaveState()
	check(t, err)
	dataState, err := data.SaveState()
	check(t, err)
	if len(dataState) != 9 {
		t.Fatalf("sparse data port serialized %d bytes of shared transport state", len(dataState)-9)
	}
	if len(commandState) <= 9 {
		t.Fatal("sparse command port did not serialize the shared transport")
	}
	check(t, data.LoadState(dataState))
	if err := data.LoadState(append(append([]byte(nil), dataState...), 0)); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("sparse data port accepted trailing state: %v", err)
	}
	check(t, command.Write(0, Width16, 0x2c))
	check(t, data.Reset())
	if panel.CurrentCommand() != 0x2c {
		t.Fatal("sparse data-port reset cleared the shared transport")
	}
	check(t, command.Reset())
	if panel.CurrentCommand() != 0 {
		t.Fatal("sparse command-port reset did not clear the shared transport")
	}
}
