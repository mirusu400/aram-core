package samsung

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/firmwareset"
)

func TestSamsungRawDownloadPrivateReferences(t *testing.T) {
	configured := os.Getenv("ARAM_SAMSUNG_RAW_REFERENCE_DIRS")
	if configured == "" {
		t.Skip("ARAM_SAMSUNG_RAW_REFERENCE_DIRS is not configured")
	}
	for index, directory := range filepath.SplitList(configured) {
		directory := directory
		if strings.TrimSpace(directory) == "" {
			continue
		}
		t.Run(fmt.Sprintf("reference-%d", index), func(t *testing.T) {
			set := openRawReferenceSet(t, directory)
			pkg, err := Inspect(set)
			if err != nil {
				t.Fatal(err)
			}
			if pkg.Family != FamilySCHRawDownload || !pkg.Complete() {
				t.Fatalf("raw package = family %q missing %v", pkg.Family, pkg.MissingRoles())
			}
			profile, err := BuiltinRegistry().Match(pkg)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"qcsbl", "oemsbl"} {
				spec, ok := profile.BootImage(id)
				if !ok {
					t.Fatalf("profile %q has no %s image", profile.ID, id)
				}
				if _, err := ReconstructBootImage(set, pkg, spec); err != nil {
					t.Fatal(err)
				}
			}
			for _, pblID := range []string{"pbl-rom", "pbl-source"} {
				pblSpec, ok := profile.MemoryImage(pblID)
				if ok {
					if _, err := ReconstructMemoryImage(set, pkg, pblSpec); err != nil {
						t.Fatal(err)
					}
				}
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
			t.Logf(
				"raw download %s: MIBIB version=%d generation=%d partitions=%d packaged-end=%#x flash=%#x logical-end=%#x program-headers=%d",
				profile.ID,
				layout.MIBIBVersion,
				layout.MIBIBGeneration,
				len(layout.Partitions),
				layout.PackagedEnd,
				flash.Size(),
				progressive.ELF.LogicalFileEnd,
				len(progressive.ELF.ProgramHeaders),
			)
			for _, partition := range flash.Partitions() {
				t.Logf("partition %s: %#x..%#x", partition.Name, partition.Start, partition.End())
			}
			for _, region := range flash.Regions() {
				t.Logf("region %s: %#x..%#x source=%#x transform=%s", region.Role, region.Start, region.End(), region.SourceOffset, region.Transform)
			}
		})
	}
}

func openRawReferenceSet(t *testing.T, directory string) firmwareset.Set {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("configured raw reference directory: %v", err)
	}
	var sources []firmwareset.Source
	for _, entry := range entries {
		if entry.IsDir() || !isReferencePieceExtension(strings.ToLower(filepath.Ext(entry.Name()))) {
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
		t.Fatalf("configured raw reference contains %d SCH download pieces, want 4", len(sources))
	}
	set, err := firmwareset.NewSet(sources)
	if err != nil {
		t.Fatal(err)
	}
	return set
}
