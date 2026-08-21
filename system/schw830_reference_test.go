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

func TestSCHW830PrivateReferencePBLHLELoadsOriginalOEMSBL(t *testing.T) {
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
	nand, err := NewQualcommNAND(flash, samsung.PageSize)
	if err != nil {
		t.Fatal(err)
	}
	bootControl, err := NewQualcommBootControl(0x10000000)
	if err != nil {
		t.Fatal(err)
	}
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

	result = backend.Run(context.Background(), result.PC, cpu.ModeARM, 10_000_000)
	var fault *Fault
	if result.Instructions != 5_400_398 || result.PC != 0x000a7a6c ||
		!errors.As(result.Err, &fault) || fault.Address != 0x84004430 ||
		fault.Permission != cpu.PermissionWrite {
		t.Fatalf("unexpected OEMSBL platform boundary: %+v", result)
	}
	if bootControl.WatchdogServices() == 0 {
		t.Fatal("original boot stages never serviced the watchdog")
	}
	t.Logf(
		"original OEMSBL boundary: second-run instructions=%d pc=0x%08x err=%v watchdog=%d",
		result.Instructions,
		result.PC,
		result.Err,
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
