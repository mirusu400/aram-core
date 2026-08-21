package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/firmwareset"
	"github.com/mirusu400/aram-core/loader/samsung"
)

func TestSCHW830PrivateReferenceReachesMMUEnableBoundary(t *testing.T) {
	root := os.Getenv("ARAM_REFERENCE_REPO")
	if root == "" {
		t.Skip("ARAM_REFERENCE_REPO is not configured")
	}
	set := openSCHW830ReferenceSet(t, filepath.Join(root, "SCH-W380_DL21"))
	pkg, err := samsung.Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := samsung.BuiltinRegistry().Match(pkg)
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := profile.BootImage("qcsbl")
	if !ok {
		t.Fatal("SCH-W830 profile has no QCSBL image")
	}
	image, err := samsung.ReconstructBootImage(set, pkg, spec)
	if err != nil {
		t.Fatal(err)
	}
	flashImage, err := samsung.AssembleFlash(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	flash, err := NewCOWFlash(flashImage, samsung.EraseBlockSize, flashImage.Identity())
	if err != nil {
		t.Fatal(err)
	}
	board := SCHW830DL21BoardProfile()
	nandReady := NewLevelSignal()
	nandConfig := Qualcomm2K8BitNANDConfig(board.NANDReadID, nandReady)
	if nandConfig.PageSize != samsung.PageSize {
		t.Fatal("SCH-W830 NAND profile page size does not match normalized flash")
	}
	nand, err := NewQualcommNAND(flash, nandConfig)
	if err != nil {
		t.Fatal(err)
	}
	bootControl, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
		EBIMemoryConfiguration: 0x5880, ClockModeStatus: 1, NANDReady: nandReady,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondaryClock := NewQualcommSecondaryClockControl()
	panel := NewParallelPanelInterface()
	handoff, err := NewQualcommNANDPBLHandoff(QualcommNANDPBLConfig{
		Entry: image.EntryAddress, TableAddress: 0x78001000,
		PageSize: samsung.PageSize, EraseBlockSize: samsung.EraseBlockSize,
		FlashSize: uint64(flash.Size()), BadBlockLimit: 0x14,
	})
	if err != nil {
		t.Fatal(err)
	}
	handoff.Memory = append(handoff.Memory, MemorySeed{
		Address: image.LoadAddress,
		Bytes:   append([]byte(nil), image.Bytes...),
	})

	bus := NewBus()
	if err := board.ApplyMemory(bus); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO("qualcomm-boot-control", 0x80000000, QualcommBootControlWindowSize, bootControl); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO("qualcomm-nand", 0x60000000, QualcommNANDWindowSize, nand); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO(
		"qualcomm-secondary-clock",
		0x84004000,
		QualcommSecondaryClockWindowSize,
		secondaryClock,
	); err != nil {
		t.Fatal(err)
	}
	if err := bus.MapMMIO(
		"parallel-panel",
		0x20000000,
		ParallelPanelWindowSize,
		panel,
	); err != nil {
		t.Fatal(err)
	}
	backend := interpreter.New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	if err := handoff.Apply(bus, backend); err != nil {
		t.Fatal(err)
	}
	fatalDiagnostic := errors.New("unexpected OEM fatal diagnostic")
	flashInitFailure := errors.New("OEM flash initialization failed")
	calls := []HLECallProfile{
		{
			ID: "diagnostic-oem-fatal", Contract: "diagnostic.oem-fatal",
			Address: 0x00107ffc, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
		{
			ID: "diagnostic-flash-init-failure", Contract: "diagnostic.flash-init-failure",
			Address: 0x000a6ae0, Mode: cpu.ModeARM, Return: HLEReturnLinkRegister,
		},
	}
	runner, err := NewHLERunner(bus, backend, calls, map[string]HLECallHandler{
		"diagnostic.oem-fatal": HLECallHandlerFunc(func(HLECallContext) error {
			return fatalDiagnostic
		}),
		"diagnostic.flash-init-failure": HLECallHandlerFunc(func(HLECallContext) error {
			return flashInitFailure
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), handoff.Entry, handoff.Mode, 1_195_629)
	if result.Err != nil || result.Reason != cpu.StopBudget ||
		result.Instructions != 1_195_629 || result.PC != 0x000a07d8 {
		t.Fatalf("unexpected QCSBL OEM callback boundary: %+v", result)
	}

	result = runner.Run(context.Background(), result.PC, cpu.ModeARM, 10_000_000)
	if result.Reason != cpu.StopFault ||
		!errors.Is(result.Err, interpreter.ErrMMUTranslationUnavailable) ||
		result.Instructions != 6_036_311 || result.PC != 0x000bc2dc {
		t.Fatalf("unexpected OEMSBL MMU-enable boundary: %+v", result)
	}
	if invocations := runner.Invocations(); len(invocations) != 0 {
		t.Fatalf("diagnostic HLE was invoked: %+v", invocations)
	}
	commands, data := panel.WriteCounts()
	if commands != 57 || data != 110_114 || panel.CurrentCommand() != 0x29 || panel.LastData() != 0xffff {
		t.Fatalf(
			"panel terminal state = %d/%d command %#x data %#x",
			commands, data, panel.CurrentCommand(), panel.LastData(),
		)
	}
	if bootControl.WatchdogServices() != 721 {
		t.Fatalf("watchdog services = %d", bootControl.WatchdogServices())
	}
	t.Logf(
		"post-panel boundary: instructions=%d pc=0x%08x err=%v panel=%d/%d command=0x%x data=0x%x watchdog=%d",
		result.Instructions,
		result.PC,
		result.Err,
		commands,
		data,
		panel.CurrentCommand(),
		panel.LastData(),
		bootControl.WatchdogServices(),
	)
}

func openSCHW830ReferenceSet(t *testing.T, directory string) firmwareset.Set {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("configured reference directory: %v", err)
	}
	var sources []firmwareset.Source
	for _, entry := range entries {
		if entry.IsDir() || !schReferenceExtension(filepath.Ext(entry.Name())) {
			continue
		}
		file, err := os.Open(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, firmwareset.Source{ReaderAt: file, Size: info.Size()})
	}
	if len(sources) != 4 {
		t.Fatalf("configured reference contains %d SCH download pieces, want 4", len(sources))
	}
	set, err := firmwareset.NewSet(sources)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func schReferenceExtension(extension string) bool {
	switch extension {
	case ".wbt", ".wbin", ".dat", ".fnt":
		return true
	default:
		return false
	}
}
