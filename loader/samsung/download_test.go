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
