package system

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/firmwareset"
	"github.com/mirusu400/aram-core/loader/samsung"
)

func TestSCHW830PrivateReferenceReachesMissingSecureModuleBoundary(t *testing.T) {
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
	assertSCHW830MissingSecurePartition(t, flashImage)
	flash, err := NewCOWFlash(flashImage, samsung.EraseBlockSize, flashImage.Identity())
	if err != nil {
		t.Fatal(err)
	}
	nand, err := NewQualcommNAND(flash, samsung.PageSize)
	if err != nil {
		t.Fatal(err)
	}
	bootControl, err := NewQualcommBootControl(QualcommBootControlConfig{
		HardwareRevision: 0x10000000, NANDInterfaceMode: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondaryClock := NewQualcommSecondaryClockControl()
	handoff, err := NewQualcommNANDPBLHandoff(QualcommNANDPBLConfig{
		Entry: image.EntryAddress, TableAddress: 0x78001000,
		PageSize: samsung.PageSize, EraseBlockSize: samsung.EraseBlockSize,
		FlashSize: uint64(flash.Size()), BadBlockLimit: 0x14,
	})
	if err != nil {
		t.Fatal(err)
	}

	bus := NewBus()
	ram := make([]byte, 0x04000000)
	copy(ram[image.LoadAddress:], image.Bytes)
	if err := bus.MapRAMImage("ebi-ram", 0, uint32(len(ram)), ram); err != nil {
		t.Fatal(err)
	}
	if err := SCHW830DL21BoardProfile().ApplyMemory(bus); err != nil {
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
	backend := interpreter.New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	if err := handoff.Apply(bus, backend); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), handoff.Entry, handoff.Mode, 1_195_629)
	if result.Err != nil || result.Reason != cpu.StopBudget ||
		result.Instructions != 1_195_629 || result.PC != 0x000a07d8 {
		t.Fatalf("unexpected QCSBL-to-OEMSBL handoff: %+v", result)
	}

	result = backend.Run(context.Background(), result.PC, cpu.ModeARM, 5_402_441)
	if result.Err != nil || result.Reason != cpu.StopBudget ||
		result.Instructions != 5_402_441 || result.PC != 0x00107ffc {
		t.Fatalf("unexpected secure-module boundary: %+v", result)
	}
	missingCode := make([]byte, 4)
	if err := bus.Read(result.PC, missingCode, cpu.PermissionExecute); err != nil {
		t.Fatal(err)
	}
	if missingCode[0] != 0 || missingCode[1] != 0 || missingCode[2] != 0 || missingCode[3] != 0 {
		t.Fatal("secure-module boundary unexpectedly contains executable input bytes")
	}
	selector, selectorErr := secondaryClock.Read(0x0430, Width32)
	data, dataErr := secondaryClock.Read(0x0434, Width32)
	if selectorErr != nil || dataErr != nil || selector != 0x36 || data != 4 {
		t.Fatalf("secondary clock terminal state = selector %#x data %#x", selector, data)
	}
	if bootControl.WatchdogServices() == 0 {
		t.Fatal("original boot stages never serviced the watchdog")
	}
	t.Logf(
		"missing secure-module boundary: instructions=%d pc=0x%08x watchdog=%d",
		result.Instructions,
		result.PC,
		bootControl.WatchdogServices(),
	)
}

func assertSCHW830MissingSecurePartition(t *testing.T, flash samsung.FlashImage) {
	t.Helper()
	var secure samsung.Partition
	found := false
	for _, partition := range flash.Partitions() {
		if partition.Name == "0:SIM_SECURE" {
			secure = partition
			found = true
			break
		}
	}
	if !found {
		t.Fatal("normalized flash has no SIM_SECURE partition")
	}
	buffer := make([]byte, 4096)
	for position := uint64(0); position < secure.Size; position += uint64(len(buffer)) {
		count := min(uint64(len(buffer)), secure.Size-position)
		if _, err := flash.ReadAt(buffer[:count], int64(secure.Start+position)); err != nil {
			t.Fatal(err)
		}
		for index, value := range buffer[:count] {
			if value != 0 {
				t.Fatalf(
					"SIM_SECURE partition has input data at relative offset 0x%x",
					position+uint64(index),
				)
			}
		}
	}
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
