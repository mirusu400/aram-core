package system

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	testLCDCommandAddress = uint32(0x20000000)
	testLCDDataAddress    = uint32(0x20020000)
)

func TestLCDTransferProbeReportsQualifiedDCSRGB565Pair(t *testing.T) {
	controller, err := NewDCSPanelController(DCSPanelConfig{Width: 2, Height: 2})
	if err != nil {
		t.Fatal(err)
	}
	bus, panel, probe := newLCDTransferProbeHarness(t, controller)

	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetPixelFormat, 0x55)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetAddressMode, dcsAddressModeBGR)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetColumnAddress, 0, 0, 0, 1)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetPageAddress, 0, 0, 0, 1)
	writeLCDTransaction(
		t,
		bus,
		testLCDCommandAddress,
		testLCDDataAddress,
		dcsWriteMemoryStart,
		0xf800,
		0x07e0,
		0x001f,
		0xffff,
	)

	report := probe.Report()
	if report.Schema != LCDTransferReportSchema || report.Status != "candidate" ||
		report.LogicalWrites != 19 || report.MatchedPhysicalWrites != report.LogicalWrites ||
		report.CorrelationFailures != 0 || report.DroppedEvidence != 0 || len(report.Warnings) != 0 {
		t.Fatalf("qualified LCD report = %+v", report)
	}
	if len(report.Candidates) != 1 {
		t.Fatalf("qualified LCD candidates = %+v", report.Candidates)
	}
	candidate := report.Candidates[0]
	if candidate.CommandAddress != testLCDCommandAddress || candidate.DataAddress != testLCDDataAddress ||
		candidate.CommandWidthBits != 16 || candidate.DataWidthBits != 16 ||
		candidate.ParameterPacking != "low-byte-per-halfword" ||
		candidate.PixelPacking != "one-rgb565-pixel-per-halfword" ||
		candidate.PixelFormat != "rgb565" || candidate.PixelFormatEvidence != "dcs-pixel-format-command" ||
		candidate.ColorOrder != "bgr" || candidate.Confidence != "high" {
		t.Fatalf("qualified LCD candidate = %+v", candidate)
	}
	if candidate.Evidence.MatchedCommandWrites != 5 || candidate.Evidence.MatchedDataWrites != 14 ||
		candidate.Evidence.RecognizedDCSCommands != 5 || candidate.Evidence.ColumnWindows != 1 ||
		candidate.Evidence.PageWindows != 1 ||
		candidate.Evidence.PixelFormatWrites != 1 || candidate.Evidence.AddressModeWrites != 1 ||
		candidate.Evidence.MemoryWriteCommands != 1 || candidate.Evidence.PixelDataWrites != 4 {
		t.Fatalf("qualified LCD evidence = %+v", candidate.Evidence)
	}
	if frame := controller.FrameRGB565(); len(frame) != 4 {
		t.Fatalf("qualified stream produced %d pixels", len(frame))
	}
	commands, data := panel.WriteCounts()
	if commands != candidate.Evidence.MatchedCommandWrites || data != candidate.Evidence.MatchedDataWrites {
		t.Fatalf("panel/probe write counts = %d/%d and %+v", commands, data, candidate.Evidence)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "63488") || strings.Contains(string(encoded), "65535") {
		t.Fatalf("LCD report retained pixel payload: %s", encoded)
	}
}

func TestLCDTransferProbeKeepsUnprovenPairLowConfidence(t *testing.T) {
	bus, _, probe := newLCDTransferProbeHarness(t, nil)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, 0xf0, 0x1234)

	report := probe.Report()
	if report.Status != "insufficient-evidence" || len(report.Candidates) != 1 ||
		len(report.Warnings) != 1 || report.Warnings[0] != "protocol-grammar-not-proven" {
		t.Fatalf("unproven LCD report = %+v", report)
	}
	candidate := report.Candidates[0]
	if candidate.Confidence != "low" || candidate.PixelFormat != "unknown" ||
		candidate.PixelFormatEvidence != "insufficient-evidence" ||
		candidate.ParameterPacking != "not-observed" || candidate.PixelPacking != "not-observed" ||
		candidate.Evidence.RecognizedDCSCommands != 0 || candidate.Evidence.PixelDataWrites != 0 {
		t.Fatalf("unproven LCD candidate = %+v", candidate)
	}
}

func TestLCDTransferProbeHonorsExplicitNonRGB565Format(t *testing.T) {
	bus, _, probe := newLCDTransferProbeHarness(t, nil)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetPixelFormat, 0x66)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetColumnAddress, 0, 0, 0, 1)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetPageAddress, 0, 0, 0, 1)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsWriteMemoryStart, 0x1234, 0xabcd)

	report := probe.Report()
	if report.Status != "candidate" || len(report.Candidates) != 1 {
		t.Fatalf("RGB666 LCD report = %+v", report)
	}
	candidate := report.Candidates[0]
	if candidate.PixelFormat != "rgb666" || candidate.PixelFormatEvidence != "dcs-pixel-format-command" ||
		candidate.PixelPacking != "one-16-bit-value-per-write" || candidate.Confidence != "medium" {
		t.Fatalf("RGB666 LCD candidate = %+v", candidate)
	}
}

func TestLCDTransferProbeDoesNotGiveMixedFormatsHighConfidence(t *testing.T) {
	bus, _, probe := newLCDTransferProbeHarness(t, nil)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetPixelFormat, 0x55)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetPixelFormat, 0x66)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetPixelFormat, 0x05)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetColumnAddress, 0, 0, 0, 1)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetPageAddress, 0, 0, 0, 1)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsWriteMemoryStart, 1, 2, 3, 4)

	report := probe.Report()
	if len(report.Candidates) != 1 || report.Candidates[0].PixelFormat != "rgb565" ||
		report.Candidates[0].Confidence != "medium" {
		t.Fatalf("mixed-format LCD report = %+v", report)
	}
}

func TestLCDTransferProbeKeepsEvidenceScopedToPhysicalPair(t *testing.T) {
	bus, panel, probe := newLCDTransferProbeHarness(t, nil)
	const secondCommand = uint32(0x21000000)
	const secondData = uint32(0x21020000)
	commandPort, err := NewParallelPanelCommandPort(panel)
	if err != nil {
		t.Fatal(err)
	}
	dataPort, err := NewParallelPanelDataPort(panel)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO("second-lcd-command", secondCommand, uint32(Width16), commandPort); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO("second-lcd-data", secondData, uint32(Width16), dataPort); err != nil {
		t.Fatal(err)
	}

	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetPixelFormat, 0x55)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetColumnAddress, 0, 0, 0, 1)
	writeLCDTransaction(t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetPageAddress, 0, 0, 0, 1)
	writeLCDTransaction(
		t,
		bus,
		testLCDCommandAddress,
		testLCDDataAddress,
		dcsWriteMemoryStart,
		0x1234,
		0x5678,
		0x9abc,
		0xdef0,
	)
	writeLCDTransaction(t, bus, secondCommand, secondData, 0xf0, 0x55)

	report := probe.Report()
	if report.Status != "candidate" || len(report.Candidates) != 2 {
		t.Fatalf("multi-pair LCD report = %+v", report)
	}
	if first, second := report.Candidates[0], report.Candidates[1]; first.CommandAddress != testLCDCommandAddress || first.Confidence != "high" ||
		second.CommandAddress != secondCommand || second.Confidence != "low" ||
		second.PixelFormat != "unknown" || second.Evidence.RecognizedDCSCommands != 0 {
		t.Fatalf("multi-pair LCD candidates = %+v", report.Candidates)
	}
}

func TestLCDTransferProbeRejectsInvalidOrRepeatedAttachment(t *testing.T) {
	probe := NewLCDTransferProbe()
	bus := NewBus()
	panel := NewParallelPanelInterface()
	if err := probe.Attach(nil, panel); !errors.Is(err, ErrLCDTransferProbe) {
		t.Fatalf("nil bus attachment error = %v", err)
	}
	if err := probe.Attach(bus, nil); !errors.Is(err, ErrLCDTransferProbe) {
		t.Fatalf("nil panel attachment error = %v", err)
	}
	if err := probe.Attach(bus, panel); err != nil {
		t.Fatal(err)
	}
	if err := probe.Attach(bus, panel); !errors.Is(err, ErrLCDTransferProbe) {
		t.Fatalf("repeated attachment error = %v", err)
	}
	var nilProbe *LCDTransferProbe
	if report := nilProbe.Report(); report.Status != "insufficient-evidence" ||
		len(report.Warnings) != 1 || report.Warnings[0] != "nil-probe" {
		t.Fatalf("nil probe report = %+v", report)
	}
}

func TestLCDTransferProbeSurvivesUnterminatedAddressParameterStream(t *testing.T) {
	bus, _, probe := newLCDTransferProbeHarness(t, nil)
	parameters := make([]uint16, 600)
	for index := range parameters {
		parameters[index] = uint16(index & 0xff)
	}
	writeLCDTransaction(
		t, bus, testLCDCommandAddress, testLCDDataAddress, dcsSetColumnAddress, parameters...,
	)

	report := probe.Report()
	if len(report.Candidates) != 1 {
		t.Fatalf("unterminated address parameter report = %+v", report)
	}
	candidate := report.Candidates[0]
	// The first four parameters complete one window; every later parameter
	// must saturate the counter instead of wrapping back into the array.
	if candidate.Evidence.ColumnWindows != 1 || candidate.Evidence.PageWindows != 0 ||
		candidate.Confidence != "low" {
		t.Fatalf("unterminated address parameter candidate = %+v", candidate)
	}
	if candidate.Evidence.MatchedDataWrites != uint64(len(parameters)) {
		t.Fatalf("unterminated address parameter data writes = %d", candidate.Evidence.MatchedDataWrites)
	}
}

func newLCDTransferProbeHarness(
	t *testing.T,
	controller ParallelPanelController,
) (*Bus, *ParallelPanelInterface, *LCDTransferProbe) {
	t.Helper()
	panel := NewParallelPanelInterface()
	if controller != nil {
		var err error
		panel, err = NewParallelPanelInterfaceWithController(controller)
		if err != nil {
			t.Fatal(err)
		}
	}
	commandPort, err := NewParallelPanelCommandPort(panel)
	if err != nil {
		t.Fatal(err)
	}
	dataPort, err := NewParallelPanelDataPort(panel)
	if err != nil {
		t.Fatal(err)
	}
	bus := NewBus()
	if err := bus.MapMMIO("test-lcd-command", testLCDCommandAddress, uint32(Width16), commandPort); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO("test-lcd-data", testLCDDataAddress, uint32(Width16), dataPort); err != nil {
		t.Fatal(err)
	}
	probe := NewLCDTransferProbe()
	if err := probe.Attach(bus, panel); err != nil {
		t.Fatal(err)
	}
	return bus, panel, probe
}

func writeLCDTransaction(
	t *testing.T,
	bus *Bus,
	commandAddress uint32,
	dataAddress uint32,
	command uint16,
	data ...uint16,
) {
	t.Helper()
	writeLCDHalfword(t, bus, commandAddress, command)
	for _, value := range data {
		writeLCDHalfword(t, bus, dataAddress, value)
	}
}

func writeLCDHalfword(t *testing.T, bus *Bus, address uint32, value uint16) {
	t.Helper()
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	if err := bus.Write(address, encoded[:], cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
}
