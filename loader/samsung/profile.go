package samsung

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/mirusu400/aram-core/firmwareset"
)

const (
	SCHW830DL21ProfileID = "samsung.sch-w830.dl21"
	SCHW830DA18ProfileID = "samsung.sch-w830.da18"
	SCHW770DA05ProfileID = "samsung.sch-w770.da05"
	SCHW860DA06ProfileID = "samsung.sch-w860.da06"
)

var (
	ErrUnknownBuild   = errors.New("unknown Samsung firmware build")
	ErrAmbiguousBuild = errors.New("ambiguous Samsung firmware build")
)

type BootImageSpec struct {
	ID            string
	Role          Role
	BlockOffsets  []int64
	BlockMarker   [8]byte
	HeaderSize    uint32
	BlockSize     uint32
	LoadAddress   uint32
	EntryOffset   uint32
	UsedSize      uint32
	LogicalSHA256 string
}

func (s BootImageSpec) validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("boot image ID is empty")
	}
	if _, ok := roleTokens[s.Role]; !ok {
		return fmt.Errorf("boot image %q has invalid role %q", s.ID, s.Role)
	}
	if len(s.BlockOffsets) == 0 || len(s.BlockOffsets) > 64 {
		return fmt.Errorf("boot image %q has invalid block count %d", s.ID, len(s.BlockOffsets))
	}
	if s.HeaderSize == 0 || s.BlockSize <= s.HeaderSize {
		return fmt.Errorf("boot image %q has invalid block geometry", s.ID)
	}
	for index, offset := range s.BlockOffsets {
		if offset < WrapperSize || offset%int64(s.BlockSize) != 0 {
			return fmt.Errorf("boot image %q block %d has invalid offset 0x%x", s.ID, index, offset)
		}
	}
	logicalSize := uint64(len(s.BlockOffsets)) * uint64(s.BlockSize-s.HeaderSize)
	if s.UsedSize == 0 || uint64(s.UsedSize) > logicalSize {
		return fmt.Errorf("boot image %q used size exceeds its logical image", s.ID)
	}
	if uint64(s.LoadAddress)+uint64(s.EntryOffset) >= 1<<32 {
		return fmt.Errorf("boot image %q entry address overflows", s.ID)
	}
	if err := validateSHA256(s.LogicalSHA256); err != nil {
		return fmt.Errorf("boot image %q: %w", s.ID, err)
	}
	return nil
}

type BuildProfile struct {
	ID           string
	Family       string
	Manufacturer string
	Model        string
	Build        string
	PieceHashes  map[Role]string
	BootImages   []BootImageSpec
}

func (p BuildProfile) validate() error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Manufacturer) == "" ||
		strings.TrimSpace(p.Model) == "" || strings.TrimSpace(p.Build) == "" {
		return fmt.Errorf("firmware profile identity is incomplete")
	}
	if p.Family != FamilySCHDownload {
		return fmt.Errorf("firmware profile %q has unsupported family %q", p.ID, p.Family)
	}
	if len(p.PieceHashes) != len(requiredRoles) {
		return fmt.Errorf("firmware profile %q does not identify exactly four pieces", p.ID)
	}
	for _, role := range requiredRoles {
		digest, ok := p.PieceHashes[role]
		if !ok {
			return fmt.Errorf("firmware profile %q has no %s hash", p.ID, role)
		}
		if err := validateSHA256(digest); err != nil {
			return fmt.Errorf("firmware profile %q %s: %w", p.ID, role, err)
		}
	}
	seenImages := make(map[string]struct{}, len(p.BootImages))
	for _, image := range p.BootImages {
		if err := image.validate(); err != nil {
			return fmt.Errorf("firmware profile %q: %w", p.ID, err)
		}
		if _, duplicate := seenImages[image.ID]; duplicate {
			return fmt.Errorf("firmware profile %q repeats boot image %q", p.ID, image.ID)
		}
		seenImages[image.ID] = struct{}{}
	}
	return nil
}

func (p BuildProfile) BootImage(id string) (BootImageSpec, bool) {
	for _, image := range p.BootImages {
		if image.ID == id {
			image.BlockOffsets = append([]int64(nil), image.BlockOffsets...)
			return image, true
		}
	}
	return BootImageSpec{}, false
}

type Registry struct {
	profiles []BuildProfile
}

func NewRegistry(profiles ...BuildProfile) (Registry, error) {
	registry := Registry{profiles: make([]BuildProfile, len(profiles))}
	seen := make(map[string]struct{}, len(profiles))
	for index, profile := range profiles {
		if err := profile.validate(); err != nil {
			return Registry{}, err
		}
		if _, duplicate := seen[profile.ID]; duplicate {
			return Registry{}, fmt.Errorf("duplicate firmware profile %q", profile.ID)
		}
		seen[profile.ID] = struct{}{}
		registry.profiles[index] = cloneProfile(profile)
	}
	return registry, nil
}

func (r Registry) Match(pkg Package) (BuildProfile, error) {
	var matches []BuildProfile
	for _, profile := range r.profiles {
		if profile.Family != pkg.Family || len(pkg.Pieces) != len(profile.PieceHashes) {
			continue
		}
		matched := true
		for role, digest := range profile.PieceHashes {
			piece, ok := pkg.Pieces[role]
			if !ok || !strings.EqualFold(piece.SHA256, digest) {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, profile)
		}
	}
	switch len(matches) {
	case 0:
		return BuildProfile{}, ErrUnknownBuild
	case 1:
		return cloneProfile(matches[0]), nil
	default:
		return BuildProfile{}, fmt.Errorf("%w: %d profiles", ErrAmbiguousBuild, len(matches))
	}
}

func BuiltinRegistry() Registry {
	registry, err := NewRegistry(
		schW830DL21Profile(),
		schW830DA18Profile(),
		schW770DA05Profile(),
		schW860DA06Profile(),
	)
	if err != nil {
		panic(err)
	}
	return registry
}

func schW770DA05Profile() BuildProfile {
	// DA05 uses the earlier version-one MIBIB layout, but its WBT keeps the
	// same 2 KiB boot-page framing and Qualcomm QCSBL/OEMSBL markers as the
	// later SCH-W830 family. The exact four-piece identity prevents those
	// shared structural facts from becoming a cross-model match.
	profile := schW830DL21Profile()
	profile.ID = SCHW770DA05ProfileID
	profile.Model = "SCH-W770"
	profile.Build = "DA05"
	profile.PieceHashes = map[Role]string{
		RoleWBT:  "e88b5d1f5fb8249e63467f27f8a279712ded1d2c93d90cc304247a6c57fc5afc",
		RoleWBIN: "1f0626d93084b6bf657fa7abe4d377ef341d0621c43ad9c650a408db1d54099d",
		RoleDAT:  "b837559aa0c9addb01c8d282fa17c8c250813c39c5ee2688436bd482a641dfab",
		RoleFont: "61dd894f063a061b7abdabb8d0d0620a60244bce9386a060f1698db210cd99e9",
	}
	for index := range profile.BootImages {
		switch profile.BootImages[index].ID {
		case "oemsbl":
			profile.BootImages[index].BlockOffsets = []int64{0x0e0000, 0x100000, 0x120000}
			profile.BootImages[index].UsedSize = 0x0004fd1c
			profile.BootImages[index].LogicalSHA256 = "2a8bb93dd6daf25f208eec98b65d278dfff7860914e400012578afa0abfcf31b"
		case "qcsbl":
			profile.BootImages[index].BlockOffsets = []int64{0x0c0000}
			profile.BootImages[index].EntryOffset = 0
			profile.BootImages[index].UsedSize = 0x000081bf
			profile.BootImages[index].LogicalSHA256 = "e501bbe2fb888d46271e6e83b3fad03dc6750a520c02358fa816abef3b02a4c4"
		}
	}
	return profile
}

func schW860DA06Profile() BuildProfile {
	profile := schW830DL21Profile()
	profile.ID = SCHW860DA06ProfileID
	profile.Model = "SCH-W860"
	profile.Build = "DA06"
	profile.PieceHashes = map[Role]string{
		RoleWBT:  "860cd9467842da3ad7c2722523c02d292ef5f6b4c0eba41e227114cc9e9029e6",
		RoleWBIN: "3259c3c6e96896eec7e7e7c6d1a6eca25486f54b0795e3b7e370f1aca823f6d8",
		RoleDAT:  "006fc80de446d7b554e387e837d259e3372c00b35e3219c3dd7d242a77044b09",
		RoleFont: "f3ce83be78dcf6615aa19e496595743c350c5f4cf372e2767968aee5c8e971a5",
	}
	for index := range profile.BootImages {
		switch profile.BootImages[index].ID {
		case "oemsbl":
			profile.BootImages[index].UsedSize = 0x00052986
			profile.BootImages[index].LogicalSHA256 = "cd1d66be2cbad443b7af8dd23249cc15c90fe2d77bd765f8d8f0ef0b78d9523a"
		case "qcsbl":
			profile.BootImages[index].LogicalSHA256 = "c46ec99bd66b005d258df04b17dee4b2bd4ae5cdcb8a9d815e88a489110cce27"
		}
	}
	return profile
}

func schW830DA18Profile() BuildProfile {
	// DA18 uses the same CG23 WBT and therefore the same original QCSBL and
	// OEMSBL block layout as DL21. The remaining exact hashes keep build
	// selection strict even though the handset board is shared.
	profile := schW830DL21Profile()
	profile.ID = SCHW830DA18ProfileID
	profile.Build = "DA18"
	profile.PieceHashes = map[Role]string{
		RoleWBT:  "b9b3e5a8175cff0813b074edec0ab852d6bd2f845900656a1a6521a32f0e8d74",
		RoleWBIN: "a0fc5f210623ede76f198dd26ef40f0f54ed3aadabc4c1fa2526ead4b5c1a159",
		RoleDAT:  "ac9383d8ef99facfa6f425057c1bf9349e0870572c3b79867a03ca63a7495ed8",
		RoleFont: "5eee858d40f1402031ee30aa8c0f724ae2bf36a91eb4c7c0669335967efcbb1c",
	}
	return profile
}

func schW830DL21Profile() BuildProfile {
	return BuildProfile{
		ID:           SCHW830DL21ProfileID,
		Family:       FamilySCHDownload,
		Manufacturer: "Samsung",
		Model:        "SCH-W830",
		Build:        "DL21",
		PieceHashes: map[Role]string{
			RoleWBT:  "b9b3e5a8175cff0813b074edec0ab852d6bd2f845900656a1a6521a32f0e8d74",
			RoleWBIN: "2f4b1dc521cfae74efbf2ec6135409eaa5e736a8a504ef1123214895983472f2",
			RoleDAT:  "955a39b3c09d6228224234dab18b3b38fe89da518c0b614a7cba47e6f9f96900",
			RoleFont: "cd32ae08892946cac4908cab32eb3a929790fa6b287afb0b8f373683e0d581d9",
		},
		BootImages: []BootImageSpec{
			{
				ID:            "oemsbl",
				Role:          RoleWBT,
				BlockOffsets:  []int64{0x160000, 0x180000, 0x1a0000},
				BlockMarker:   [8]byte{0x9c, 0x12, 0x0f, 0xfa, 0xc9, 0xb6, 0x8f, 0x5a},
				HeaderSize:    PageSize,
				BlockSize:     EraseBlockSize,
				LoadAddress:   0x000a0000,
				EntryOffset:   0,
				UsedSize:      0x000495e6,
				LogicalSHA256: "eb02b25d9836b82fc816d84d33bf7148ad37c5f36ea15a2b87cdbf21b585cda8",
			},
			{
				ID:            "qcsbl",
				Role:          RoleWBT,
				BlockOffsets:  []int64{0x220000},
				BlockMarker:   [8]byte{0xdf, 0x5d, 0xe8, 0x5f, 0xbc, 0xce, 0x64, 0x52},
				HeaderSize:    PageSize,
				BlockSize:     EraseBlockSize,
				LoadAddress:   0x00080000,
				EntryOffset:   0x28,
				UsedSize:      0x0000ad11,
				LogicalSHA256: "6e3580efedef3d88e30e407e92e101b0b2bcf15e5a327f3c53967314bedb0169",
			},
		},
	}
}

func cloneProfile(profile BuildProfile) BuildProfile {
	clone := profile
	clone.PieceHashes = make(map[Role]string, len(profile.PieceHashes))
	for role, digest := range profile.PieceHashes {
		clone.PieceHashes[role] = digest
	}
	clone.BootImages = append([]BootImageSpec(nil), profile.BootImages...)
	for index := range clone.BootImages {
		clone.BootImages[index].BlockOffsets = append([]int64(nil), profile.BootImages[index].BlockOffsets...)
	}
	return clone
}

type BootImage struct {
	ID           string
	LoadAddress  uint32
	EntryAddress uint32
	UsedSize     uint32
	SHA256       string
	Bytes        []byte
}

func ReconstructBootImage(
	set firmwareset.Set,
	pkg Package,
	spec BootImageSpec,
) (BootImage, error) {
	if err := spec.validate(); err != nil {
		return BootImage{}, err
	}
	metadata, ok := pkg.Pieces[spec.Role]
	if !ok {
		return BootImage{}, fmt.Errorf("%w: missing %s", ErrIncompleteSet, spec.Role)
	}
	piece, err := set.Piece(metadata.Index)
	if err != nil {
		return BootImage{}, err
	}
	if piece.SHA256() != metadata.SHA256 {
		return BootImage{}, fmt.Errorf("Samsung %s metadata does not match firmware set", spec.Role)
	}
	payloadSize := int(spec.BlockSize - spec.HeaderSize)
	logical := make([]byte, 0, len(spec.BlockOffsets)*payloadSize)
	for _, offset := range spec.BlockOffsets {
		if offset > piece.Size()-int64(spec.BlockSize) {
			return BootImage{}, &FormatError{
				Role: spec.Role, Piece: piece.Index(), Offset: offset,
				Reason: fmt.Sprintf("boot image %q block exceeds input", spec.ID),
			}
		}
		var marker [8]byte
		if _, err := piece.ReadAt(marker[:], offset); err != nil {
			return BootImage{}, err
		}
		if marker != spec.BlockMarker {
			return BootImage{}, &FormatError{
				Role: spec.Role, Piece: piece.Index(), Offset: offset,
				Reason: fmt.Sprintf("boot image %q block marker mismatch", spec.ID),
			}
		}
		block := make([]byte, payloadSize)
		if _, err := piece.ReadAt(block, offset+int64(spec.HeaderSize)); err != nil {
			return BootImage{}, err
		}
		logical = append(logical, block...)
	}
	digest := sha256.Sum256(logical)
	digestText := hex.EncodeToString(digest[:])
	if !strings.EqualFold(digestText, spec.LogicalSHA256) {
		return BootImage{}, fmt.Errorf(
			"boot image %q SHA-256 %s does not match profile %s",
			spec.ID,
			digestText,
			spec.LogicalSHA256,
		)
	}
	return BootImage{
		ID:           spec.ID,
		LoadAddress:  spec.LoadAddress,
		EntryAddress: spec.LoadAddress + spec.EntryOffset,
		UsedSize:     spec.UsedSize,
		SHA256:       digestText,
		Bytes:        logical,
	}, nil
}

func validateSHA256(value string) error {
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("SHA-256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("invalid SHA-256: %w", err)
	}
	return nil
}
