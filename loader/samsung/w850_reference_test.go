package samsung

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/firmwareset"
)

func TestSamsungW850PrivateReference(t *testing.T) {
	directory := os.Getenv("ARAM_SAMSUNG_W850_REFERENCE_DIR")
	if directory == "" {
		t.Skip("ARAM_SAMSUNG_W850_REFERENCE_DIR is not configured")
	}
	set := openW850ReferenceSet(t, directory)
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Family != FamilySCHFlexOneNANDDownload || !pkg.Complete() {
		t.Fatalf("W850 package = family %q missing %v", pkg.Family, pkg.MissingRoles())
	}
	profile, err := BuiltinRegistry().Match(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != SCHW850CF11ProfileID {
		t.Fatalf("W850 profile = %q", profile.ID)
	}
	var oemsbl BootImage
	var oemsblSpec BootImageSpec
	for _, id := range []string{"qcsbl", "oemsbl"} {
		spec, ok := profile.BootImage(id)
		if !ok {
			t.Fatalf("W850 profile has no %s image", id)
		}
		image, err := ReconstructBootImage(set, pkg, spec)
		if err != nil {
			t.Fatal(err)
		}
		if id == "oemsbl" && (spec.PBLPreload || spec.LoadAddress != 0x00900000 || spec.EntryOffset != 0 ||
			len(spec.PBLBytePatches) != 0) {
			t.Fatalf("W850 OEMSBL image = %+v", spec)
		}
		if id == "oemsbl" {
			oemsbl = image
			oemsblSpec = spec
		}
	}
	wbtPiece, err := set.Piece(pkg.Pieces[RoleWBT].Index)
	if err != nil {
		t.Fatal(err)
	}
	containerHeader := make([]byte, oemsblSpec.HeaderSize)
	if _, err := wbtPiece.ReadAt(containerHeader, oemsblSpec.BlockOffsets[0]); err != nil {
		t.Fatal(err)
	}
	logicalEntry := binary.LittleEndian.Uint32(oemsbl.Bytes[0x18:0x1c])
	if logicalEntry < oemsbl.LoadAddress ||
		uint64(logicalEntry) >= uint64(oemsbl.LoadAddress)+uint64(len(oemsbl.Bytes)) {
		t.Fatal("W850 reconstructed OEMSBL header entry is outside its image")
	}
	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	wantStarts := map[Role]uint64{
		RoleWBT: 0, RoleWBIN: 0x00400000, RoleABIN: 0x01a00000,
		RoleDAT: 0x04700000, RoleFont: 0x09a00000,
	}
	for role, start := range wantStarts {
		region := layout.Region(role)
		if region == nil || region.Start != start {
			t.Fatalf("W850 %s region = %+v, want start %#x", role, region, start)
		}
	}
	flash, err := AssembleFlash(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if flash.Size() != int64(flexOneNANDPhysicalSize) {
		t.Fatalf("W850 Flex-OneNAND size = %#x", flash.Size())
	}
	target, err := flexOneNANDBootTarget(oemsblSpec, layout.Partitions)
	if err != nil {
		t.Fatal(err)
	}
	loadedHeader := make([]byte, len(containerHeader))
	if _, err := flash.ReadAt(loadedHeader, int64(target)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loadedHeader, containerHeader) {
		t.Fatal("W850 raw OEMSBL FBA does not contain its block header")
	}
	payloadTarget := target + uint64(oemsblSpec.HeaderSize)
	loaded := make([]byte, int(oemsbl.UsedSize))
	if _, err := flash.ReadAt(loaded, int64(payloadTarget)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, oemsbl.Bytes[:oemsbl.UsedSize]) {
		t.Fatal("W850 raw OEMSBL FBA does not contain the reconstructed used image")
	}
	var bootRegion *FlashRegion
	for _, region := range flash.Regions() {
		if region.Role == RoleWBT {
			candidate := region
			bootRegion = &candidate
			break
		}
	}
	if bootRegion == nil || bootRegion.Transform != TransformFlexOneNANDBoot ||
		bootRegion.OutputSHA256 == bootRegion.SourceSHA256 {
		t.Fatalf("W850 transformed boot region = %+v", bootRegion)
	}
}

func openW850ReferenceSet(t *testing.T, directory string) firmwareset.Set {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	profile := schW850CF11Profile()
	wbtHash := profile.PieceHashes[RoleWBT]
	var sources []firmwareset.Source
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension != ".wbt" && extension != ".mbin" && extension != ".abin" &&
			extension != ".dat" && extension != ".fnt" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if extension == ".wbt" {
			hash := sha256.New()
			buffer := make([]byte, 1024*1024)
			for offset := int64(0); offset < info.Size(); {
				count := min(int64(len(buffer)), info.Size()-offset)
				if _, err := file.ReadAt(buffer[:count], offset); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				_, _ = hash.Write(buffer[:count])
				offset += count
			}
			if hex.EncodeToString(hash.Sum(nil)) != wbtHash {
				_ = file.Close()
				continue
			}
		}
		t.Cleanup(func() { _ = file.Close() })
		sources = append(sources, firmwareset.Source{ReaderAt: file, Size: info.Size()})
	}
	if len(sources) != len(flexOneNANDRequiredRoles) {
		t.Fatalf("configured W850 reference contains %d selected pieces, want %d", len(sources), len(flexOneNANDRequiredRoles))
	}
	set, err := firmwareset.NewSet(sources)
	if err != nil {
		t.Fatal(err)
	}
	return set
}
