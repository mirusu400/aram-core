package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/firmwareset"
	"github.com/mirusu400/aram-core/loader/samsung"
)

func TestSCHW830QCSBLPrivateReferenceEntersOriginalCode(t *testing.T) {
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

	bus := NewBus()
	if err := bus.MapRAMImage("qcsbl", image.LoadAddress, uint32(len(image.Bytes)), image.Bytes); err != nil {
		t.Fatal(err)
	}
	if err := SCHW830DL21BoardProfile().ApplyMemory(bus); err != nil {
		t.Fatal(err)
	}
	probe := &boundedMMIOProbe{limit: 32}
	if err := bus.MapMMIO("qcsbl-hardware-probe", 0x80000000, 0x10000, probe); err != nil {
		t.Fatal(err)
	}
	backend := interpreter.New()
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), image.EntryAddress, cpu.ModeARM, 1_000_000)
	if result.Instructions == 0 || result.PC == image.EntryAddress {
		t.Fatalf("QCSBL did not enter original code: %+v", result)
	}
	if result.Instructions == 1 && errors.Is(result.Err, cpu.ErrUnsupportedInstruction) {
		t.Fatalf("QCSBL stopped on its first instruction: %+v", result)
	}
	var fault *Fault
	if result.Instructions != 56069 || result.PC != 0x000831a0 ||
		!errors.As(result.Err, &fault) || fault.Address != 0x8000540c ||
		fault.Permission != cpu.PermissionWrite {
		t.Fatalf("unexpected QCSBL trace boundary: %+v", result)
	}
	t.Logf(
		"QCSBL trace boundary: instructions=%d pc=0x%08x reason=%d err=%v accesses=%v",
		result.Instructions,
		result.PC,
		result.Reason,
		result.Err,
		probe.accesses,
	)
}

type boundedMMIOProbe struct {
	limit    int
	accesses []string
}

func (p *boundedMMIOProbe) Reset() error {
	p.accesses = nil
	return nil
}

func (p *boundedMMIOProbe) Read(offset uint32, width Width) (uint32, error) {
	if len(p.accesses) >= p.limit {
		return 0, errors.New("MMIO probe access limit reached")
	}
	p.accesses = append(p.accesses, fmt.Sprintf("read%d@0x%x=0", width*8, offset))
	return 0, nil
}

func (p *boundedMMIOProbe) Write(offset uint32, width Width, value uint32) error {
	p.accesses = append(p.accesses, fmt.Sprintf("write%d@0x%x=0x%x", width*8, offset, value))
	return errors.New("unmodeled MMIO write")
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
