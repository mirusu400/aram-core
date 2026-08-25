package samsung

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/firmwareset"
)

func TestSCHW860DA06PrivateReferenceStructuralBoundary(t *testing.T) {
	directory := os.Getenv("ARAM_SCHW860_DA06_DIR")
	if directory == "" {
		t.Skip("ARAM_SCHW860_DA06_DIR is not configured")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("configured SCH-W860 reference directory: %v", err)
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
		t.Fatalf("configured SCH-W860 reference contains %d SCH download pieces, want 4", len(sources))
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
	if profile.ID != SCHW860DA06ProfileID {
		t.Fatalf("SCH-W860 profile = %q, want %q", profile.ID, SCHW860DA06ProfileID)
	}
	wbtMetadata := pkg.Pieces[RoleWBT]
	wbt, err := set.Piece(wbtMetadata.Index)
	if err != nil {
		t.Fatal(err)
	}
	wbin, err := set.Piece(pkg.Pieces[RoleWBIN].Index)
	if err != nil {
		t.Fatal(err)
	}
	copies, err := parseMIBIBCopies(wbt)
	if err != nil {
		t.Fatal(err)
	}
	selected := copies[0]
	for _, candidate := range copies[1:] {
		if candidate.Generation > selected.Generation {
			selected = candidate
		}
	}
	footer, footerOffset, err := readWBINFooter(wbin)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SCH-W860 WBIN footer at %#x = %#x", footerOffset, footer)
	for _, partition := range selected.Partitions {
		t.Logf("SCH-W860 partition %q start=%#x size=%#x", partition.Name, partition.Start, partition.Size)
	}
	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	progressive, err := DecodeWBIN(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	flash, err := AssembleFlash(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	type bootSpan struct {
		id      string
		offsets []int64
	}
	for _, span := range []bootSpan{
		{id: "oemsbl", offsets: []int64{0x160000, 0x180000, 0x1a0000}},
		{id: "qcsbl", offsets: []int64{0x220000}},
	} {
		logical := make([]byte, 0, len(span.offsets)*int(EraseBlockSize-PageSize))
		markers := make([]string, 0, len(span.offsets))
		for _, offset := range span.offsets {
			var marker [8]byte
			if _, err := wbt.ReadAt(marker[:], offset); err != nil {
				t.Fatal(err)
			}
			markers = append(markers, hex.EncodeToString(marker[:]))
			block := make([]byte, EraseBlockSize-PageSize)
			if _, err := wbt.ReadAt(block, offset+PageSize); err != nil {
				t.Fatal(err)
			}
			logical = append(logical, block...)
		}
		used := len(logical)
		for used != 0 && logical[used-1] == 0xff {
			used--
		}
		t.Logf("SCH-W860 %s markers=%v logical-size=%#x used-size=%#x logical-sha256=%x",
			span.id, markers, len(logical), used, sha256.Sum256(logical))
	}
	for _, id := range []string{"oemsbl", "qcsbl"} {
		spec, ok := profile.BootImage(id)
		if !ok {
			t.Fatalf("SCH-W860 profile has no %s image", id)
		}
		image, err := ReconstructBootImage(set, pkg, spec)
		if err != nil {
			t.Fatal(err)
		}
		dumpEnvironment := "ARAM_SCHW860_DUMP_" + strings.ToUpper(id)
		if dumpPath := os.Getenv(dumpEnvironment); dumpPath != "" {
			if err := os.WriteFile(dumpPath, image.Bytes, 0o600); err != nil {
				t.Fatalf("dump SCH-W860 %s: %v", id, err)
			}
		}
	}
	t.Logf(
		"SCH-W860 normalized: MIBIB generation=%d partitions=%d packaged-end=%#x flash=%#x encrypted=%#x logical-end=%#x program-headers=%d progressive-sha256=%s",
		selected.Generation,
		len(selected.Partitions),
		layout.PackagedEnd,
		flash.Size(),
		progressive.EncryptedLength,
		progressive.ELF.LogicalFileEnd,
		len(progressive.ELF.ProgramHeaders),
		progressive.SHA256,
	)
}
