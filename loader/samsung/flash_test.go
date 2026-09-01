package samsung

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/mirusu400/aram-core/firmwareset"
)

func TestAssembleFlashMapsDecodedAndIdentityRegions(t *testing.T) {
	set, pkg := syntheticFlashSet(t, false)
	image, err := AssembleFlash(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if image.Size() != 0xe0000 || image.ErasedValue() != 0xff {
		t.Fatalf("flash geometry = size %#x erased %#x", image.Size(), image.ErasedValue())
	}
	if len(image.Identity()) != len("samsung-flash-v1:")+64 {
		t.Fatalf("flash identity = %q", image.Identity())
	}
	regions := image.Regions()
	if len(regions) != 4 {
		t.Fatalf("flash region count = %d", len(regions))
	}
	wantStarts := []uint64{0, 0x60000, 0x80000, 0xc0000}
	wantRoles := []Role{RoleWBT, RoleWBIN, RoleDAT, RoleFont}
	for index := range regions {
		if regions[index].Role != wantRoles[index] || regions[index].Start != wantStarts[index] ||
			len(regions[index].SourceSHA256) != 64 || len(regions[index].OutputSHA256) != 64 {
			t.Fatalf("flash region %d = %+v", index, regions[index])
		}
	}
	if regions[0].Transform != TransformBootBlocks ||
		regions[1].Transform != TransformSEEDFeedback || regions[1].SourceOffset != WrapperSize {
		t.Fatalf("WBIN attribution = %+v", regions[1])
	}

	assertFlashBytes(t, image, 0, []byte{0x11})
	assertFlashBytes(t, image, EraseBlockSize, []byte{0xac, 0x9f, 0x56, 0xfe})
	assertFlashBytes(t, image, 0x60000, []byte{0x7f, 'E', 'L', 'F'})
	assertFlashBytes(t, image, 0x70000, bytes.Repeat([]byte{0xff}, 16))
	assertFlashBytes(t, image, 0x80000, []byte{0xd1})
	assertFlashBytes(t, image, 0xc0000, []byte{0xf1})

	if len(image.ProgressiveELF().ProgramHeaders) != 1 {
		t.Fatalf("progressive ELF = %+v", image.ProgressiveELF())
	}
	partitions := image.Partitions()
	partitions[0].Name = "mutated"
	if image.Partitions()[0].Name == "mutated" {
		t.Fatal("Partitions returned mutable image metadata")
	}

	buffer := make([]byte, 2)
	count, err := image.ReadAt(buffer, image.Size()-1)
	if count != 1 || !errors.Is(err, io.EOF) || buffer[0] != 0xff {
		t.Fatalf("partial flash read = count %d bytes %x error %v", count, buffer, err)
	}
}

func TestAssembleFlashMapsRawDownloadRegionsFromPieceOrigin(t *testing.T) {
	sources := syntheticRawDownloadSources(t)
	wbt := readSyntheticSource(t, sources[RoleWBT])
	wbt[0] = 0x11
	sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}
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
	image, err := AssembleFlash(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	regions := image.Regions()
	if len(regions) != 4 {
		t.Fatalf("raw flash region count = %d", len(regions))
	}
	for _, region := range regions {
		if region.SourceOffset != 0 {
			t.Fatalf("raw %s source offset = %#x", region.Role, region.SourceOffset)
		}
	}
	if regions[1].Transform != TransformIdentity {
		t.Fatalf("raw WBIN transform = %q", regions[1].Transform)
	}
	assertFlashBytes(t, image, 0, []byte{0x11})
	assertFlashBytes(t, image, 0x60000, []byte{0x7f, 'E', 'L', 'F'})
	assertFlashBytes(t, image, 0x80000, []byte{0x67, 0x31})
	assertFlashBytes(t, image, 0xc0000, []byte{1, 0, 0, 0})
}

func TestAssembleFlashMapsLogicalRegionsAroundFactoryBadBlocks(t *testing.T) {
	set, pkg := syntheticFlashSet(t, false)
	image, err := AssembleFlashWithOptions(set, pkg, FlashAssemblyOptions{
		FactoryBadBlocks: []uint32{5, 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if image.Size() != 0x120000 {
		t.Fatalf("physical flash size = %#x", image.Size())
	}
	regions := image.Regions()
	if len(regions) != 4 {
		t.Fatalf("physical flash region count = %d: %+v", len(regions), regions)
	}
	wantStarts := []uint64{0, 0x80000, 0xc0000, 0x100000}
	for index, start := range wantStarts {
		if regions[index].Start != start {
			t.Fatalf("physical flash region %d start = %#x, want %#x", index, regions[index].Start, start)
		}
	}
	assertFlashBytes(t, image, 0x60000, bytes.Repeat([]byte{0xff}, 16))
	assertFlashBytes(t, image, 0x80000, []byte{0x7f, 'E', 'L', 'F'})
	assertFlashBytes(t, image, 0xa0000, bytes.Repeat([]byte{0xff}, 16))
	assertFlashBytes(t, image, 0xc0000, []byte{0xd1})
	assertFlashBytes(t, image, 0x100000, []byte{0xf1})
}

func TestAssembleFlashRejectsOverlappingNormalizedRegions(t *testing.T) {
	set, pkg := syntheticFlashSet(t, true)
	_, err := AssembleFlash(set, pkg)
	if !errors.Is(err, ErrInvalidFlashLayout) {
		t.Fatalf("AssembleFlash error = %v", err)
	}
}

func syntheticFlashSet(t *testing.T, overlap bool) (firmwareset.Set, Package) {
	t.Helper()
	sources := syntheticDownloadSources(t)
	wbin, _ := syntheticEncodedWBIN(t)
	if overlap {
		putU32s(wbin, len(wbin)-62, 0x7, 0xc, 0x8, 0xe)
	}
	sources[RoleWBIN] = firmwareset.Source{ReaderAt: bytes.NewReader(wbin), Size: int64(len(wbin))}

	wbt := readSyntheticSource(t, sources[RoleWBT])
	wbt[WrapperSize] = 0x11
	if overlap {
		wbt = append(wbt, make([]byte, 0x10000)...)
	}
	sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}
	dat := readSyntheticSource(t, sources[RoleDAT])
	dat[WrapperSize] = 0xd1
	sources[RoleDAT] = firmwareset.Source{ReaderAt: bytes.NewReader(dat), Size: int64(len(dat))}
	font := readSyntheticSource(t, sources[RoleFont])
	font[WrapperSize] = 0xf1
	sources[RoleFont] = firmwareset.Source{ReaderAt: bytes.NewReader(font), Size: int64(len(font))}

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
	return set, pkg
}

func assertFlashBytes(t *testing.T, image FlashImage, offset int64, want []byte) {
	t.Helper()
	got := make([]byte, len(want))
	if _, err := image.ReadAt(got, offset); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("flash bytes at %#x = %x, want %x", offset, got, want)
	}
}
