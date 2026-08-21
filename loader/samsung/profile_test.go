package samsung

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/firmwareset"
)

func TestRegistryMatchesExactPieceHashes(t *testing.T) {
	sources := syntheticDownloadSources(t)
	_, pkg := inspectSyntheticSet(t, sources)
	profile := syntheticProfile(t, pkg)
	registry, err := NewRegistry(profile)
	if err != nil {
		t.Fatal(err)
	}
	matched, err := registry.Match(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if matched.ID != profile.ID {
		t.Fatalf("matched profile = %q, want %q", matched.ID, profile.ID)
	}

	mutated := pkg
	mutated.Pieces = clonePieces(pkg.Pieces)
	wbin := mutated.Pieces[RoleWBIN]
	wbin.SHA256 = fmt.Sprintf("%064d", 1)
	mutated.Pieces[RoleWBIN] = wbin
	if _, err := registry.Match(mutated); !errors.Is(err, ErrUnknownBuild) {
		t.Fatalf("Match changed hash error = %v", err)
	}
}

func TestReconstructBootImageStripsPerBlockHeaders(t *testing.T) {
	sources := syntheticDownloadSources(t)
	wbt := readSyntheticSource(t, sources[RoleWBT])
	const firstBlock = WrapperSize
	marker := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	copy(wbt[firstBlock:firstBlock+8], marker[:])
	for index := 0; index < 32; index++ {
		wbt[firstBlock+PageSize+index] = byte(index + 1)
	}
	sources[RoleWBT] = firmwareset.Source{ReaderAt: bytes.NewReader(wbt), Size: int64(len(wbt))}
	set, pkg := inspectSyntheticSet(t, sources)

	logical := wbt[firstBlock+PageSize : firstBlock+EraseBlockSize]
	digest := sha256.Sum256(logical)
	spec := BootImageSpec{
		ID:            "synthetic-sbl",
		Role:          RoleWBT,
		BlockOffsets:  []int64{firstBlock},
		BlockMarker:   marker,
		HeaderSize:    PageSize,
		BlockSize:     EraseBlockSize,
		LoadAddress:   0x80000,
		EntryOffset:   0x28,
		UsedSize:      0x100,
		LogicalSHA256: fmt.Sprintf("%x", digest),
	}
	image, err := ReconstructBootImage(set, pkg, spec)
	if err != nil {
		t.Fatal(err)
	}
	if image.EntryAddress != 0x80028 || len(image.Bytes) != EraseBlockSize-PageSize {
		t.Fatalf("boot image = entry %#x, size %#x", image.EntryAddress, len(image.Bytes))
	}
	if !bytes.Equal(image.Bytes[:32], wbt[firstBlock+PageSize:firstBlock+PageSize+32]) {
		t.Fatal("reconstructed payload does not follow the block header")
	}

	spec.BlockMarker[0] ^= 0xff
	if _, err := ReconstructBootImage(set, pkg, spec); err == nil {
		t.Fatal("ReconstructBootImage accepted the wrong block marker")
	}
}

func inspectSyntheticSet(t *testing.T, sources map[Role]firmwareset.Source) (firmwareset.Set, Package) {
	t.Helper()
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
	return set, pkg
}

func syntheticProfile(t *testing.T, pkg Package) BuildProfile {
	t.Helper()
	hashes := make(map[Role]string, len(requiredRoles))
	for _, role := range requiredRoles {
		hashes[role] = pkg.Pieces[role].SHA256
	}
	return BuildProfile{
		ID:           "samsung.synthetic.build",
		Family:       FamilySCHDownload,
		Manufacturer: "Samsung",
		Model:        "Synthetic",
		Build:        "TEST",
		PieceHashes:  hashes,
	}
}

func clonePieces(input map[Role]Piece) map[Role]Piece {
	output := make(map[Role]Piece, len(input))
	for role, piece := range input {
		output[role] = piece
	}
	return output
}
