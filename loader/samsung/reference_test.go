package samsung

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/firmwareset"
)

func TestSCHW830DL21PrivateReference(t *testing.T) {
	root := os.Getenv("ARAM_REFERENCE_REPO")
	if root == "" {
		t.Skip("ARAM_REFERENCE_REPO is not configured")
	}
	directory := filepath.Join(root, "SCH-W380_DL21")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("configured reference directory: %v", err)
	}

	var (
		sources []firmwareset.Source
		files   []*os.File
	)
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	for _, entry := range entries {
		if entry.IsDir() || !isReferencePieceExtension(filepath.Ext(entry.Name())) {
			continue
		}
		file, err := os.Open(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		files = append(files, file)
		sources = append(sources, firmwareset.Source{ReaderAt: file, Size: info.Size()})
	}
	if len(sources) != 4 {
		t.Fatalf("configured reference contains %d SCH download pieces, want 4", len(sources))
	}

	set, err := firmwareset.NewSet(sources)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := BuiltinRegistry().Match(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != SCHW830DL21ProfileID {
		t.Fatalf("profile = %q, want %q", profile.ID, SCHW830DL21ProfileID)
	}
	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if layout.MIBIBGeneration != 2 || len(layout.Partitions) != 10 {
		t.Fatalf("MIBIB = generation %d, %d partitions", layout.MIBIBGeneration, len(layout.Partitions))
	}
	assertReferencePartition(t, layout, "0:AMSS", 0x002a0000, 0x01900000)
	assertReferencePartition(t, layout, "0:RSRC", 0x01c00000, 0x03300000)
	assertReferencePartition(t, layout, "0:FONT", 0x04f00000, 0x04500000)

	for _, id := range []string{"oemsbl", "qcsbl"} {
		spec, ok := profile.BootImage(id)
		if !ok {
			t.Fatalf("profile has no %s boot image", id)
		}
		image, err := ReconstructBootImage(set, pkg, spec)
		if err != nil {
			t.Fatal(err)
		}
		if image.SHA256 != spec.LogicalSHA256 {
			t.Fatalf("%s image hash = %s", id, image.SHA256)
		}
	}
}

func isReferencePieceExtension(extension string) bool {
	switch extension {
	case ".wbt", ".wbin", ".dat", ".fnt":
		return true
	default:
		return false
	}
}

func assertReferencePartition(t *testing.T, layout Layout, name string, start, size uint64) {
	t.Helper()
	for _, partition := range layout.Partitions {
		if partition.Name == name {
			if partition.Start != start || partition.Size != size {
				t.Fatalf("%s partition = start %#x size %#x", name, partition.Start, partition.Size)
			}
			return
		}
	}
	t.Fatalf("partition %s is missing", name)
}
