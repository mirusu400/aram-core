package samsung

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/firmwareset"
)

func TestSCHW830DA18PrivateReference(t *testing.T) {
	directory := os.Getenv("ARAM_SCHW830_DA18_DIR")
	if directory == "" {
		t.Skip("ARAM_SCHW830_DA18_DIR is not configured")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("configured DA18 reference directory: %v", err)
	}

	var sources []firmwareset.Source
	for _, entry := range entries {
		if entry.IsDir() || !isReferencePieceExtension(filepath.Ext(entry.Name())) {
			continue
		}
		file, openErr := os.Open(filepath.Join(directory, entry.Name()))
		if openErr != nil {
			t.Fatal(openErr)
		}
		t.Cleanup(func() { _ = file.Close() })
		info, statErr := file.Stat()
		if statErr != nil {
			t.Fatal(statErr)
		}
		sources = append(sources, firmwareset.Source{ReaderAt: file, Size: info.Size()})
	}
	if len(sources) != 4 {
		t.Fatalf("configured DA18 reference contains %d SCH download pieces, want 4", len(sources))
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
	if profile.ID != SCHW830DA18ProfileID {
		t.Fatalf("profile = %q, want %q", profile.ID, SCHW830DA18ProfileID)
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
		if _, err := ReconstructBootImage(set, pkg, spec); err != nil {
			t.Fatal(err)
		}
	}
	progressive, err := DecodeWBIN(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if progressive.EncryptedLength == 0 || len(progressive.ELF.ProgramHeaders) == 0 {
		t.Fatalf("decoded DA18 WBIN has no progressive image metadata")
	}
	flash, err := AssembleFlash(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(
		"DA18 normalized: flash=%#x encrypted=%#x logical-end=%#x program-headers=%d hash=%s",
		flash.Size(),
		progressive.EncryptedLength,
		progressive.ELF.LogicalFileEnd,
		len(progressive.ELF.ProgramHeaders),
		progressive.SHA256,
	)
}
