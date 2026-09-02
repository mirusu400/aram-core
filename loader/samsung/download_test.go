package samsung

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/firmwareset"
)

func TestInspectAndNormalizeSyntheticSCHDownloadSetWithoutFilenames(t *testing.T) {
	sources := syntheticDownloadSources(t)
	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleFont],
		sources[RoleWBT],
		sources[RoleDAT],
		sources[RoleWBIN],
	})
	if err != nil {
		t.Fatal(err)
	}

	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	if !pkg.Complete() || len(pkg.MissingRoles()) != 0 {
		t.Fatalf("package is incomplete: %v", pkg.MissingRoles())
	}
	if got := pkg.Pieces[RoleWBT].Index; got != 1 {
		t.Fatalf("WBT source index = %d, want 1", got)
	}
	if got := pkg.Pieces[RoleDAT].Header.Build; got != "DL211024" {
		t.Fatalf("DAT build = %q", got)
	}

	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Family != FamilySCHDownload || layout.MIBIBGeneration != 2 {
		t.Fatalf("layout identity = %+v", layout)
	}
	if layout.PackagedEnd != 0x0e0000 {
		t.Fatalf("packaged end = %#x, want %#x", layout.PackagedEnd, uint64(0x0e0000))
	}
	if len(layout.Partitions) != 5 {
		t.Fatalf("partition count = %d, want 5", len(layout.Partitions))
	}
	if got := layout.Partitions[3]; got.Name != "0:RSRC" || got.Start != 0x80000 {
		t.Fatalf("RSRC partition = %+v", got)
	}
	wbin := layout.Region(RoleWBIN)
	if wbin == nil || wbin.Start != 0x60000 || wbin.Transform != TransformSEEDFeedback {
		t.Fatalf("WBIN region = %+v", wbin)
	}
	if got := layout.Region(RoleDAT); got == nil || got.Start != 0x80000 {
		t.Fatalf("DAT region = %+v", got)
	}
	if got := layout.Region(RoleFont); got == nil || got.Start != 0xc0000 {
		t.Fatalf("FNT region = %+v", got)
	}
}

func TestInspectAndNormalizeSyntheticRawSCHDownloadSetWithoutFilenames(t *testing.T) {
	sources := syntheticRawDownloadSources(t)
	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleFont],
		sources[RoleDAT],
		sources[RoleWBT],
		sources[RoleWBIN],
	})
	if err != nil {
		t.Fatal(err)
	}

	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Family != FamilySCHRawDownload || !pkg.Complete() {
		t.Fatalf("raw package = family %q missing %v", pkg.Family, pkg.MissingRoles())
	}
	if got := pkg.Pieces[RoleWBT].Index; got != 2 {
		t.Fatalf("raw WBT source index = %d, want 2", got)
	}

	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Family != FamilySCHRawDownload || layout.MIBIBGeneration != 2 {
		t.Fatalf("raw layout identity = %+v", layout)
	}
	if layout.PageSize != PageSize || layout.EraseBlockSize != EraseBlockSize {
		t.Fatalf("raw NAND geometry = %#x/%#x", layout.PageSize, layout.EraseBlockSize)
	}
	wbin := layout.Region(RoleWBIN)
	if wbin == nil || wbin.Start != 0x60000 || wbin.SourceOffset != 0 ||
		wbin.Transform != TransformIdentity {
		t.Fatalf("raw WBIN region = %+v", wbin)
	}
	for _, role := range []Role{RoleWBT, RoleDAT, RoleFont} {
		region := layout.Region(role)
		if region == nil || region.SourceOffset != 0 {
			t.Fatalf("raw %s region = %+v", role, region)
		}
	}
}

func TestNormalizeFindsRawFooterBeforeDownloaderPadding(t *testing.T) {
	sources := syntheticRawDownloadSources(t)
	wbin := readSyntheticSource(t, sources[RoleWBIN])
	fixedFooter := len(wbin) - 62
	footer := append([]byte(nil), wbin[fixedFooter:fixedFooter+16]...)
	clear(wbin[fixedFooter : fixedFooter+16])
	displacedFooter := len(wbin) - 0x60
	copy(wbin[displacedFooter:displacedFooter+16], footer)
	sources[RoleWBIN] = firmwareset.Source{ReaderAt: bytes.NewReader(wbin), Size: int64(len(wbin))}

	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleWBT], sources[RoleWBIN], sources[RoleDAT], sources[RoleFont],
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if layout.PackagedEnd != 0x0e0000 {
		t.Fatalf("displaced-footer packaged end = %#x", layout.PackagedEnd)
	}
}

func TestInspectAndNormalizeSmallPageRawSCHDownload(t *testing.T) {
	sources := syntheticSmallPageRawDownloadSources(t)
	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleFont], sources[RoleWBIN], sources[RoleWBT], sources[RoleDAT],
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Family != FamilySCHRawDownload || !pkg.Complete() {
		t.Fatalf("small-page package = family %q missing %v", pkg.Family, pkg.MissingRoles())
	}
	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if layout.PageSize != smallPageSize || layout.EraseBlockSize != smallEraseBlockSize ||
		layout.MIBIBVersion != 1 || layout.MIBIBGeneration != 2 || layout.PackagedEnd != 0x0e0000 {
		t.Fatalf("small-page layout = %+v", layout)
	}
	if got := layout.Region(RoleWBIN); got == nil || got.Start != 0x60000 {
		t.Fatalf("small-page WBIN region = %+v", got)
	}
	if got := layout.Region(RoleDAT); got == nil || got.Start != 0x80000 {
		t.Fatalf("small-page DAT region = %+v", got)
	}
	if got := layout.Region(RoleFont); got == nil || got.Start != 0x0a0000 {
		t.Fatalf("small-page FONT region = %+v", got)
	}
}

func TestInspectRecognizesFlatARMAndZlibRawPieces(t *testing.T) {
	sources := syntheticSmallPageRawDownloadSources(t)
	wbin := make([]byte, 0x1000)
	for offset := 0; offset < 32; offset += 4 {
		binary.LittleEndian.PutUint32(wbin[offset:], 0xe59ff018)
		binary.LittleEndian.PutUint32(wbin[offset+32:], uint32(0x00100000+offset))
	}
	sources[RoleWBIN] = firmwareset.Source{ReaderAt: bytes.NewReader(wbin), Size: int64(len(wbin))}
	font := readSyntheticSource(t, sources[RoleFont])
	font[0], font[1] = 0x78, 0x9c
	sources[RoleFont] = firmwareset.Source{ReaderAt: bytes.NewReader(font), Size: int64(len(font))}

	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleFont], sources[RoleDAT], sources[RoleWBIN], sources[RoleWBT],
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Pieces[RoleWBIN].Header.Build != "raw-arm" ||
		pkg.Pieces[RoleFont].Header.Build != "raw-zlib" {
		t.Fatalf("raw structural headers = WBIN %+v FONT %+v", pkg.Pieces[RoleWBIN], pkg.Pieces[RoleFont])
	}

	clear(wbin[32:64])
	if rawARMVectorHeader(wbin[:64]) {
		t.Fatal("flat ARM detection accepted zero handler vectors")
	}
	if !zlibHeader([]byte{0x78, 0x9c}) || zlibHeader([]byte{0x78, 0x00}) {
		t.Fatal("zlib header validation accepted an invalid checksum")
	}
}

func TestInspectInfersOnlyExactMissingRawRole(t *testing.T) {
	sources := syntheticSmallPageRawDownloadSources(t)
	unknownFont := bytes.Repeat([]byte{0xa5}, 0x1000)
	sources[RoleFont] = firmwareset.Source{
		ReaderAt: bytes.NewReader(unknownFont), Size: int64(len(unknownFont)),
	}
	roles := []Role{RoleWBT, RoleWBIN, RoleDAT, RoleFont}
	setSources := make([]firmwareset.Source, len(roles))
	for index, role := range roles {
		setSources[index] = sources[role]
	}
	set, err := firmwareset.NewSet(setSources)
	if err != nil {
		t.Fatal(err)
	}
	hashes := make(map[Role]string, len(roles))
	for index, role := range roles {
		piece, pieceErr := set.Piece(index)
		if pieceErr != nil {
			t.Fatal(pieceErr)
		}
		hashes[role] = piece.SHA256()
	}
	registry, err := NewRegistry(BuildProfile{
		ID: "samsung.synthetic.inferred-font", Family: FamilySCHRawDownload,
		Manufacturer: "Samsung", Model: "Synthetic", Build: "INFERRED",
		PieceHashes: hashes,
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := inspectWithRegistry(set, registry)
	if err != nil {
		t.Fatal(err)
	}
	if font := pkg.Pieces[RoleFont]; font.Index != 3 || font.Header.Build != "raw-inferred-font" {
		t.Fatalf("inferred raw FONT = %+v", font)
	}
	if _, err := Inspect(set); !errors.Is(err, ErrNotSCHDownload) {
		t.Fatalf("unprofiled raw role inference error = %v", err)
	}
}

func TestNormalizeAcceptsAdjacentMIBIBTableVersions(t *testing.T) {
	tests := []struct {
		name                          string
		headerVersion, primaryVersion uint32
		trailingSentinel              bool
	}{
		{name: "v2-primary-v1-sentinel", headerVersion: 2, primaryVersion: 1, trailingSentinel: true},
		{name: "v3-primary-v2", headerVersion: 3, primaryVersion: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sources := syntheticSmallPageRawDownloadSources(t)
			wbt := readSyntheticSource(t, sources[RoleWBT])
			for _, copyOffset := range []int{0x0c000, 0x10000} {
				copyData := wbt[copyOffset : copyOffset+smallEraseBlockSize]
				binary.LittleEndian.PutUint32(copyData[8:12], test.headerVersion)
				primary := copyData[smallPageSize : 2*smallPageSize]
				binary.LittleEndian.PutUint32(primary[8:12], test.primaryVersion)
				if test.trailingSentinel {
					binary.LittleEndian.PutUint32(primary[12:16], 7)
					clear(primary[16+6*mibibEntrySize : 16+7*mibibEntrySize])
				}
			}
			sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}
			set, err := firmwareset.NewSet([]firmwareset.Source{
				sources[RoleWBT], sources[RoleWBIN], sources[RoleDAT], sources[RoleFont],
			})
			if err != nil {
				t.Fatal(err)
			}
			pkg, err := Inspect(set)
			if err != nil {
				t.Fatal(err)
			}
			layout, err := Normalize(set, pkg)
			if err != nil {
				t.Fatal(err)
			}
			if layout.MIBIBVersion != test.headerVersion || layout.MIBIBGeneration != 2 ||
				len(layout.Partitions) != 6 {
				t.Fatalf("adjacent-version MIBIB layout = %+v", layout)
			}
		})
	}
}

func TestNormalizeAndAssembleFooterlessNon64KDataLayout(t *testing.T) {
	sources := syntheticSmallPageRawDownloadSources(t)
	wbt := readSyntheticSource(t, sources[RoleWBT])
	entries := []struct {
		name        string
		start, size uint32
	}{
		{"0:MIBIB", 0, 16},
		{"0:QCSBL", 16, 8},
		{"0:AMSS", 24, 5},
		{"0:DATA", 29, 4},
		{"0:FONT", 33, 4},
		{"0:EFS2", 37, 4},
	}
	for _, copyOffset := range []int{0x0c000, 0x10000} {
		primary := wbt[copyOffset+smallPageSize : copyOffset+2*smallPageSize]
		binary.LittleEndian.PutUint32(primary[12:16], uint32(len(entries)))
		for index, entry := range entries {
			offset := 16 + index*mibibEntrySize
			clear(primary[offset : offset+mibibEntrySize])
			copy(primary[offset:offset+16], entry.name)
			putU32s(primary, offset+16, entry.start, entry.size, 0x00ffffff)
		}
	}
	sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}
	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleWBT], sources[RoleWBIN], sources[RoleDAT], sources[RoleFont],
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	wbinRegion, datRegion, fontRegion := layout.Region(RoleWBIN), layout.Region(RoleDAT), layout.Region(RoleFont)
	if layout.PackagedEnd != 0x0a4000 || wbinRegion == nil || datRegion == nil || fontRegion == nil ||
		wbinRegion.Start != 0x60000 || datRegion.Start != 0x74000 || fontRegion.Start != 0x84000 {
		t.Fatalf("footerless non-64-KiB layout = %+v", layout)
	}
	image, err := AssembleFlash(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if image.Size() != 0x0a4000 || image.PageSize() != smallPageSize ||
		image.EraseBlockSize() != smallEraseBlockSize {
		t.Fatalf("footerless non-64-KiB flash = %#x/%#x/%#x", image.Size(), image.PageSize(), image.EraseBlockSize())
	}
}

func TestInspectRejectsMixedWrappedAndRawSCHDownloadPieces(t *testing.T) {
	wrapped := syntheticDownloadSources(t)
	raw := syntheticRawDownloadSources(t)
	set, err := firmwareset.NewSet([]firmwareset.Source{
		wrapped[RoleWBT], raw[RoleWBIN], raw[RoleDAT], raw[RoleFont],
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(set); !errors.Is(err, ErrNotSCHDownload) {
		t.Fatalf("Inspect mixed-family error = %v", err)
	}
}

func TestNormalizeAcceptsTrailingWritablePartitionAfterPackagedEnd(t *testing.T) {
	sources := syntheticDownloadSources(t)
	wbt := readSyntheticSource(t, sources[RoleWBT])
	primaryOffset := WrapperSize + 2*EraseBlockSize + PageSize
	primary := wbt[primaryOffset : primaryOffset+PageSize]
	binary.LittleEndian.PutUint32(primary[12:16], 6)
	entryOffset := 16 + 5*mibibEntrySize
	copy(primary[entryOffset:entryOffset+16], "0:EFS2")
	putU32s(primary, entryOffset+16, 7, 2, 0x00ffffff)
	sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}

	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleWBT], sources[RoleWBIN], sources[RoleDAT], sources[RoleFont],
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if layout.PackagedEnd != 0x0e0000 || len(layout.Partitions) != 6 {
		t.Fatalf("layout with trailing writable partition = %+v", layout)
	}
	last := layout.Partitions[len(layout.Partitions)-1]
	if last.Name != "0:EFS2" || last.Start != layout.PackagedEnd || last.End() != 0x120000 {
		t.Fatalf("trailing writable partition = %+v", last)
	}
}

func TestNormalizeAcceptsVersionOneOpenEndedFinalPartition(t *testing.T) {
	sources := syntheticDownloadSources(t)
	wbt := readSyntheticSource(t, sources[RoleWBT])
	copyOffset := WrapperSize + 2*EraseBlockSize
	copyData := wbt[copyOffset : copyOffset+EraseBlockSize]
	binary.LittleEndian.PutUint32(copyData[8:12], 1)
	primary := copyData[PageSize:]
	binary.LittleEndian.PutUint32(primary[8:12], 1)
	binary.LittleEndian.PutUint32(primary[12:16], 6)
	entryOffset := 16 + 5*mibibEntrySize
	copy(primary[entryOffset:entryOffset+16], "0:EFS2")
	putU32s(primary, entryOffset+16, 7, 0, 0)
	sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}

	wbin := readSyntheticSource(t, sources[RoleWBIN])
	footer := len(wbin) - 62
	putU32s(wbin, footer, 0x6, 0xc, 0x8, 0x12)
	sources[RoleWBIN] = firmwareset.Source{ReaderAt: bytes.NewReader(wbin), Size: int64(len(wbin))}

	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleWBT], sources[RoleWBIN], sources[RoleDAT], sources[RoleFont],
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if layout.MIBIBVersion != 1 || layout.MIBIBGeneration != 2 || layout.PackagedEnd != 0x120000 {
		t.Fatalf("version-one layout = %+v", layout)
	}
	last := layout.Partitions[len(layout.Partitions)-1]
	if last.Name != "0:EFS2" || last.Start != 0x0e0000 || last.Size != 0x040000 || last.BlockCount != 2 {
		t.Fatalf("resolved open-ended partition = %+v", last)
	}
}

func TestNormalizeRejectsOpenEndedPartitionOutsideVersionOneTail(t *testing.T) {
	tests := []struct {
		name       string
		version    uint32
		entryIndex int
	}{
		{name: "version-three", version: 3, entryIndex: 5},
		{name: "non-final", version: 1, entryIndex: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sources := syntheticDownloadSources(t)
			wbt := readSyntheticSource(t, sources[RoleWBT])
			copyOffset := WrapperSize + 2*EraseBlockSize
			copyData := wbt[copyOffset : copyOffset+EraseBlockSize]
			binary.LittleEndian.PutUint32(copyData[8:12], test.version)
			primary := copyData[PageSize:]
			binary.LittleEndian.PutUint32(primary[8:12], test.version)
			binary.LittleEndian.PutUint32(primary[12:16], 6)
			entryOffset := 16 + test.entryIndex*mibibEntrySize
			copy(primary[entryOffset:entryOffset+16], "0:EFS2")
			putU32s(primary, entryOffset+16, 7, 0, 0)
			sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}

			set, err := firmwareset.NewSet([]firmwareset.Source{
				sources[RoleWBT], sources[RoleWBIN], sources[RoleDAT], sources[RoleFont],
			})
			if err != nil {
				t.Fatal(err)
			}
			pkg, err := Inspect(set)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Normalize(set, pkg); err == nil {
				t.Fatal("Normalize accepted an invalid open-ended partition")
			}
		})
	}
}

func TestNormalizeAcceptsVersionOneFOTAAliasAtEndOfAMSS(t *testing.T) {
	sources := syntheticDownloadSources(t)
	wbt := readSyntheticSource(t, sources[RoleWBT])
	copyOffset := WrapperSize + 2*EraseBlockSize
	copyData := wbt[copyOffset : copyOffset+EraseBlockSize]
	binary.LittleEndian.PutUint32(copyData[8:12], 1)
	primary := copyData[PageSize:]
	putU32s(primary, 8, 1, 6)
	entries := []struct {
		name        string
		start, size uint32
	}{
		{"0:MIBIB", 0, 2},
		{"0:QCSBL", 2, 1},
		{"0:AMSS", 3, 3},
		{"0:FOTA", 5, 1},
		{"0:RSRC", 6, 2},
		{"0:FONT", 8, 1},
	}
	for index, entry := range entries {
		offset := 16 + index*mibibEntrySize
		clear(primary[offset : offset+mibibEntrySize])
		copy(primary[offset:offset+16], entry.name)
		putU32s(primary, offset+16, entry.start, entry.size, 0)
	}
	sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}

	wbin := readSyntheticSource(t, sources[RoleWBIN])
	footer := len(wbin) - 62
	putU32s(wbin, footer, 0x6, 0x10, 0xc, 0x12)
	sources[RoleWBIN] = firmwareset.Source{ReaderAt: bytes.NewReader(wbin), Size: int64(len(wbin))}

	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleWBT], sources[RoleWBIN], sources[RoleDAT], sources[RoleFont],
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if layout.MIBIBVersion != 1 || len(layout.Partitions) != len(entries) {
		t.Fatalf("version-one alias layout = %+v", layout)
	}
}

func TestNormalizeAcceptsVersionOneFOTAAliasWithinFollowingDMB(t *testing.T) {
	sources := syntheticDownloadSources(t)
	wbt := readSyntheticSource(t, sources[RoleWBT])
	copyOffset := WrapperSize + 2*EraseBlockSize
	copyData := wbt[copyOffset : copyOffset+EraseBlockSize]
	putU32s(copyData, 8, 1)
	primary := copyData[PageSize:]
	putU32s(primary, 8, 1, 7)
	entries := []struct {
		name        string
		start, size uint32
	}{
		{"0:MIBIB", 0, 2},
		{"0:QCSBL", 2, 1},
		{"0:AMSS", 3, 3},
		{"0:FOTA", 8, 1},
		{"0:DMB", 6, 3},
		{"0:RSRC", 9, 3},
		{"0:FONT", 12, 1},
	}
	for index, entry := range entries {
		offset := 16 + index*mibibEntrySize
		clear(primary[offset : offset+mibibEntrySize])
		copy(primary[offset:offset+16], entry.name)
		putU32s(primary, offset+16, entry.start, entry.size, 0)
	}
	sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}

	wbin := readSyntheticSource(t, sources[RoleWBIN])
	putU32s(wbin, len(wbin)-62, 0x6, 0x18, 0x12, 0x1a)
	sources[RoleWBIN] = firmwareset.Source{ReaderAt: bytes.NewReader(wbin), Size: int64(len(wbin))}

	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleWBT], sources[RoleWBIN], sources[RoleDAT], sources[RoleFont],
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := Normalize(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if layout.MIBIBVersion != 1 || len(layout.Partitions) != len(entries) {
		t.Fatalf("version-one DMB alias layout = %+v", layout)
	}
}

func TestVersionOneFOTAAliasAcceptsEvidencedContainers(t *testing.T) {
	fota := Partition{Name: "0:FOTA", Start: 0x28000, Size: 0x4000}
	for _, name := range []string{"0:AMSS", "0:DMB", "0:RSRC"} {
		container := Partition{Name: name, Start: 0x20000, Size: 0x10000}
		if !versionOneFOTAAlias(1, fota, container) ||
			!versionOneFOTAAlias(1, container, fota) {
			t.Fatalf("version-one FOTA alias rejected container %q", name)
		}
	}
	if versionOneFOTAAlias(3, fota, Partition{Name: "0:RSRC", Start: 0x20000, Size: 0x10000}) ||
		versionOneFOTAAlias(1, fota, Partition{Name: "0:FONT", Start: 0x20000, Size: 0x10000}) {
		t.Fatal("FOTA alias accepted an unsupported version or container")
	}
}

func TestNormalizeRejectsUnrelatedVersionOnePartitionOverlap(t *testing.T) {
	sources := syntheticDownloadSources(t)
	wbt := readSyntheticSource(t, sources[RoleWBT])
	copyOffset := WrapperSize + 2*EraseBlockSize
	copyData := wbt[copyOffset : copyOffset+EraseBlockSize]
	binary.LittleEndian.PutUint32(copyData[8:12], 1)
	primary := copyData[PageSize:]
	binary.LittleEndian.PutUint32(primary[8:12], 1)
	entryOffset := 16 + 4*mibibEntrySize
	putU32s(primary, entryOffset+16, 5, 2, 0)
	sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}

	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleWBT], sources[RoleWBIN], sources[RoleDAT], sources[RoleFont],
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(set, pkg); err == nil {
		t.Fatal("Normalize accepted an unrelated version-one overlap")
	}
}

func TestInspectReportsDuplicateRoleAndMissingPieces(t *testing.T) {
	sources := syntheticDownloadSources(t)
	set, err := firmwareset.NewSet([]firmwareset.Source{sources[RoleWBT], sources[RoleWBT]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(set); !errors.Is(err, ErrDuplicateRole) {
		t.Fatalf("Inspect duplicate error = %v", err)
	}

	set, err = firmwareset.NewSet([]firmwareset.Source{sources[RoleWBT]})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Complete() || len(pkg.MissingRoles()) != 3 {
		t.Fatalf("partial package missing roles = %v", pkg.MissingRoles())
	}
	if _, err := Normalize(set, pkg); !errors.Is(err, ErrIncompleteSet) {
		t.Fatalf("Normalize incomplete error = %v", err)
	}
}

func TestInspectWrapsFamilyRecognitionError(t *testing.T) {
	data := syntheticWrappedPiece(RoleWBT, "CG231001", 0x100)
	binary.LittleEndian.PutUint32(data[0:4], 0)
	set, err := firmwareset.NewSet([]firmwareset.Source{
		{ReaderAt: bytes.NewReader(data), Size: int64(len(data))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(set); !errors.Is(err, ErrNotSCHDownload) {
		t.Fatalf("Inspect recognition error = %v", err)
	}
}

func TestNormalizeCarriesMIBIBFormatOffset(t *testing.T) {
	sources := syntheticDownloadSources(t)
	wbt := readSyntheticSource(t, sources[RoleWBT])
	binary.LittleEndian.PutUint32(wbt[0x6080c:0x60810], 0xff)
	sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}
	set, err := firmwareset.NewSet([]firmwareset.Source{
		sources[RoleWBT], sources[RoleWBIN], sources[RoleDAT], sources[RoleFont],
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Normalize(set, pkg)
	var formatErr *FormatError
	if !errors.As(err, &formatErr) {
		t.Fatalf("Normalize error = %v, want FormatError", err)
	}
	if formatErr.Role != RoleWBT || formatErr.Offset != 0x6080c {
		t.Fatalf("FormatError = %+v", formatErr)
	}
}

func syntheticDownloadSources(t *testing.T) map[Role]firmwareset.Source {
	t.Helper()
	const (
		wbtPayload  = 0x60000
		wbinPayload = 0x20000
	)
	pieces := map[Role][]byte{
		RoleWBT:  syntheticWrappedPiece(RoleWBT, "CG231001", wbtPayload),
		RoleWBIN: syntheticWrappedPiece(RoleWBIN, "DL211024", wbinPayload),
		RoleDAT:  syntheticWrappedPiece(RoleDAT, "DL211024", 0x10000),
		RoleFont: syntheticWrappedPiece(RoleFont, "DD081821", 0x10000),
	}

	copyOffset := WrapperSize + 2*EraseBlockSize
	copyData := pieces[RoleWBT][copyOffset : copyOffset+EraseBlockSize]
	putU32s(copyData, 0, mibibHeaderMagic[0], mibibHeaderMagic[1], mibibHeaderMagic[2], 2)
	primary := copyData[PageSize:]
	putU32s(primary, 0, primaryTableMagic[0], primaryTableMagic[1], primaryTableMagic[2], 5)
	entries := []struct {
		name        string
		start, size uint32
	}{
		{"0:MIBIB", 0, 2},
		{"0:QCSBL", 2, 1},
		{"0:AMSS", 3, 1},
		{"0:RSRC", 4, 2},
		{"0:FONT", 6, 1},
	}
	for index, entry := range entries {
		offset := 16 + index*mibibEntrySize
		copy(primary[offset:offset+16], entry.name)
		putU32s(primary, offset+16, entry.start, entry.size, 0x00ffffff)
	}

	wbin := pieces[RoleWBIN]
	footer := len(wbin) - 62
	putU32s(wbin, footer, 0x6, 0xc, 0x8, 0xe)

	result := make(map[Role]firmwareset.Source, len(pieces))
	for role, data := range pieces {
		result[role] = firmwareset.Source{ReaderAt: bytes.NewReader(data), Size: int64(len(data))}
	}
	return result
}

func syntheticRawDownloadSources(t *testing.T) map[Role]firmwareset.Source {
	t.Helper()
	wrapped := syntheticDownloadSources(t)
	_, plaintext := syntheticEncodedWBIN(t)
	payloads := make(map[Role][]byte, len(requiredRoles))
	for _, role := range []Role{RoleWBT, RoleDAT, RoleFont} {
		piece := readSyntheticSource(t, wrapped[role])
		payloads[role] = append([]byte(nil), piece[WrapperSize:]...)
	}
	payloads[RoleWBIN] = append([]byte(nil), plaintext...)
	putU32s(payloads[RoleDAT], 0, 0x3167)
	putU32s(payloads[RoleFont], 0, 1)
	copy(payloads[RoleFont][4:12], "DC18brew")

	result := make(map[Role]firmwareset.Source, len(payloads))
	for role, data := range payloads {
		result[role] = firmwareset.Source{ReaderAt: bytes.NewReader(data), Size: int64(len(data))}
	}
	return result
}

func syntheticSmallPageRawDownloadSources(t *testing.T) map[Role]firmwareset.Source {
	t.Helper()
	sources := syntheticRawDownloadSources(t)
	wbt := make([]byte, 0x60000)
	entries := []struct {
		name        string
		start, size uint32
	}{
		{"0:MIBIB", 0, 16},
		{"0:QCSBL", 16, 8},
		{"0:AMSS", 24, 8},
		{"0:RSRC", 32, 8},
		{"0:FONT", 40, 8},
		{"0:EFS2", 48, 8},
	}
	for generation, copyOffset := range []int{0x0c000, 0x10000} {
		copyData := wbt[copyOffset : copyOffset+smallEraseBlockSize]
		putU32s(copyData, 0, mibibHeaderMagic[0], mibibHeaderMagic[1], 1, uint32(generation+1))
		primary := copyData[smallPageSize : 2*smallPageSize]
		putU32s(primary, 0, primaryTableMagic[0], primaryTableMagic[1], 1, uint32(len(entries)))
		for index, entry := range entries {
			offset := 16 + index*mibibEntrySize
			copy(primary[offset:offset+16], entry.name)
			putU32s(primary, offset+16, entry.start, entry.size, 0x00ffffff)
		}
	}
	sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}

	wbin := readSyntheticSource(t, sources[RoleWBIN])
	footer := len(wbin) - 62
	clear(wbin[footer : footer+16])
	sources[RoleWBIN] = firmwareset.Source{ReaderAt: bytes.NewReader(wbin), Size: int64(len(wbin))}
	return sources
}

func syntheticWrappedPiece(role Role, build string, payloadSize int) []byte {
	data := make([]byte, WrapperSize+payloadSize)
	putU32s(data, 0, WrapperMagic, roleTokens[role])
	copy(data[12:20], build)
	return data
}

func putU32s(data []byte, offset int, values ...uint32) {
	for index, value := range values {
		binary.LittleEndian.PutUint32(data[offset+index*4:], value)
	}
}

func readSyntheticSource(t *testing.T, source firmwareset.Source) []byte {
	t.Helper()
	data := make([]byte, source.Size)
	if _, err := source.ReaderAt.ReadAt(data, 0); err != nil {
		t.Fatal(err)
	}
	return data
}
