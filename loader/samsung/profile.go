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
	SCHW320DC18ProfileID = "samsung.sch-w320.dc18"
	SCHW340DC18ProfileID = "samsung.sch-w340.dc18"
	SCHW350CK06ProfileID = "samsung.sch-w350.ck06"
	SCHW410CL10ProfileID = "samsung.sch-w410.cl10"
	SCHW850CF11ProfileID = "samsung.sch-w850.cf11"
)

var (
	ErrUnknownBuild   = errors.New("unknown Samsung firmware build")
	ErrAmbiguousBuild = errors.New("ambiguous Samsung firmware build")
)

type BootImageSpec struct {
	ID             string
	Role           Role
	PBLPreload     bool
	PBLBytePatches []BootImageBytePatch
	BlockOffsets   []int64
	BlockMarker    [8]byte
	MarkerOffset   uint32
	HeaderSize     uint32
	BlockSize      uint32
	DataOffset     uint32
	DataSize       uint32
	ChunkSize      uint32
	ChunkStride    uint32
	LoadAddress    uint32
	EntryOffset    uint32
	UsedSize       uint32
	LogicalSHA256  string
}

// BootImageBytePatch records a target-selection byte applied by the missing
// mask-ROM PBL after the exact source image has passed its profile hash check.
// Expected prevents a profile from silently patching a different image layout.
type BootImageBytePatch struct {
	Offset   uint32
	Expected byte
	Value    byte
}

// MemoryImageSpec identifies a bounded, directly mapped image retained in a
// firmware piece. Unlike BootImageSpec it has no per-erase-block framing.
type MemoryImageSpec struct {
	ID            string
	Role          Role
	SourceOffset  uint64
	Size          uint32
	LoadAddress   uint32
	LogicalSHA256 string
}

func (s MemoryImageSpec) validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("memory image ID is empty")
	}
	if _, ok := roleTokens[s.Role]; !ok {
		return fmt.Errorf("memory image %q has invalid role %q", s.ID, s.Role)
	}
	if s.Size == 0 || uint64(s.LoadAddress)+uint64(s.Size) > 1<<32 ||
		s.SourceOffset > ^uint64(0)-uint64(s.Size) {
		return fmt.Errorf("memory image %q has invalid geometry", s.ID)
	}
	if err := validateSHA256(s.LogicalSHA256); err != nil {
		return fmt.Errorf("memory image %q: %w", s.ID, err)
	}
	return nil
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
		if offset < 0 || offset < WrapperSize && s.MarkerOffset == 0 ||
			offset%int64(s.BlockSize) != 0 {
			return fmt.Errorf("boot image %q block %d has invalid offset 0x%x", s.ID, index, offset)
		}
	}
	dataOffset, dataSize, chunkSize, chunkStride, ok := s.geometry()
	if !ok || uint64(s.MarkerOffset)+uint64(len(s.BlockMarker)) > uint64(s.BlockSize) ||
		dataOffset < s.HeaderSize {
		return fmt.Errorf("boot image %q has invalid extraction geometry", s.ID)
	}
	logicalSize := uint64(len(s.BlockOffsets)) * uint64(dataSize/chunkStride) * uint64(chunkSize)
	if s.UsedSize == 0 || uint64(s.UsedSize) > logicalSize {
		return fmt.Errorf("boot image %q used size exceeds its logical image", s.ID)
	}
	if len(s.PBLBytePatches) != 0 && !s.PBLPreload {
		return fmt.Errorf("boot image %q patches an image not preloaded by PBL", s.ID)
	}
	seenPatchOffsets := make(map[uint32]struct{}, len(s.PBLBytePatches))
	for _, patch := range s.PBLBytePatches {
		if uint64(patch.Offset) >= logicalSize || patch.Expected == patch.Value {
			return fmt.Errorf("boot image %q has invalid PBL byte patch at 0x%x", s.ID, patch.Offset)
		}
		if _, duplicate := seenPatchOffsets[patch.Offset]; duplicate {
			return fmt.Errorf("boot image %q repeats PBL byte patch at 0x%x", s.ID, patch.Offset)
		}
		seenPatchOffsets[patch.Offset] = struct{}{}
	}
	if uint64(s.LoadAddress)+uint64(s.EntryOffset) >= 1<<32 {
		return fmt.Errorf("boot image %q entry address overflows", s.ID)
	}
	if err := validateSHA256(s.LogicalSHA256); err != nil {
		return fmt.Errorf("boot image %q: %w", s.ID, err)
	}
	return nil
}

func (s BootImageSpec) geometry() (dataOffset, dataSize, chunkSize, chunkStride uint32, ok bool) {
	dataOffset = s.DataOffset
	if dataOffset == 0 {
		dataOffset = s.HeaderSize
	}
	if dataOffset >= s.BlockSize {
		return 0, 0, 0, 0, false
	}
	dataSize = s.DataSize
	if dataSize == 0 {
		dataSize = s.BlockSize - dataOffset
	}
	chunkSize = s.ChunkSize
	if chunkSize == 0 {
		chunkSize = dataSize
	}
	chunkStride = s.ChunkStride
	if chunkStride == 0 {
		chunkStride = chunkSize
	}
	if dataSize == 0 || uint64(dataOffset)+uint64(dataSize) > uint64(s.BlockSize) ||
		chunkSize == 0 || chunkSize > chunkStride || dataSize%chunkStride != 0 {
		return 0, 0, 0, 0, false
	}
	return dataOffset, dataSize, chunkSize, chunkStride, true
}

type BuildProfile struct {
	ID           string
	Family       string
	Manufacturer string
	Model        string
	Build        string
	PieceHashes  map[Role]string
	BootImages   []BootImageSpec
	MemoryImages []MemoryImageSpec
}

func (p BuildProfile) validate() error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Manufacturer) == "" ||
		strings.TrimSpace(p.Model) == "" || strings.TrimSpace(p.Build) == "" {
		return fmt.Errorf("firmware profile identity is incomplete")
	}
	if p.Family != FamilySCHDownload && p.Family != FamilySCHRawDownload &&
		p.Family != FamilySCHFlexOneNANDDownload {
		return fmt.Errorf("firmware profile %q has unsupported family %q", p.ID, p.Family)
	}
	required := requiredRolesForFamily(p.Family)
	if len(p.PieceHashes) != len(required) {
		return fmt.Errorf("firmware profile %q does not identify exactly %d pieces", p.ID, len(required))
	}
	for _, role := range required {
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
	for _, image := range p.MemoryImages {
		if err := image.validate(); err != nil {
			return fmt.Errorf("firmware profile %q: %w", p.ID, err)
		}
		if _, duplicate := seenImages[image.ID]; duplicate {
			return fmt.Errorf("firmware profile %q repeats image %q", p.ID, image.ID)
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

func (p BuildProfile) MemoryImage(id string) (MemoryImageSpec, bool) {
	for _, image := range p.MemoryImages {
		if image.ID == id {
			return image, true
		}
	}
	return MemoryImageSpec{}, false
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
		schW320DC18Profile(),
		schW340DC18Profile(),
		schW350CK06Profile(),
		schW410CL10Profile(),
		schW850CF11Profile(),
	)
	if err != nil {
		panic(err)
	}
	return registry
}

func schW850CF11Profile() BuildProfile {
	return BuildProfile{
		ID: SCHW850CF11ProfileID, Family: FamilySCHFlexOneNANDDownload,
		Manufacturer: "Samsung", Model: "SCH-W850", Build: "CF11",
		PieceHashes: map[Role]string{
			RoleWBT:  "aa6e58dfff046d0f3a29a5b194fca5a4db5c6298dc7d3c430414f2dd45fe1e51",
			RoleWBIN: "a06577f94b08692ff06063c981519947b6403e1882afe9b2f6b850e512a47274",
			RoleABIN: "cec122bee31a1d00f7e030407b85d144309530e2480832fd320761e68006cfe3",
			RoleDAT:  "c4b20a0cc8696e6fda69c9b94269a8446475b74466e74d13efda42f3f3474aef",
			RoleFont: "0b26e9e03cc28c027c3a04edc6b761016e533ac91f95f5e4a3324753aef31681",
		},
		BootImages: []BootImageSpec{
			{
				ID: "qcsbl", Role: RoleWBT, BlockOffsets: []int64{0},
				BlockMarker:  [8]byte{0xdf, 0x5d, 0xe8, 0x5f, 0xbc, 0xce, 0x64, 0x52},
				MarkerOffset: 0x00006000, HeaderSize: 0x00008000, BlockSize: 0x00080000,
				DataSize: 0x00078000, ChunkSize: 0x00000800, ChunkStride: 0x00002000,
				LoadAddress: 0x00800000, UsedSize: 0x000191dc,
				LogicalSHA256: "a81b775f74e101c172d158f0f640ed0b20d1a181a6792d7568d12c0cc20bbb04",
			},
			{
				ID: "oemsbl", Role: RoleWBT,
				BlockOffsets: []int64{0x00200000},
				BlockMarker:  [8]byte{0x9c, 0x12, 0x0f, 0xfa, 0xc9, 0xb6, 0x8f, 0x5a},
				HeaderSize:   0x00001000, BlockSize: 0x00080000,
				// The downloader container retains OEMSBL in partition block four.
				// Flash assembly maps its used image into the corresponding packed
				// raw FBA so QCSBL can copy it through the SFlash controller.
				LoadAddress: 0x00900000, UsedSize: 0x0003e168,
				LogicalSHA256: "39f41501f6917b1d5206abc073bc4ad2cd2ebe599a54d55aaf5d83c2c787d072",
			},
		},
	}
}

func schW320DC18Profile() BuildProfile {
	return schRawDownloadProfile(
		SCHW320DC18ProfileID,
		"SCH-W320",
		"DC18",
		map[Role]string{
			RoleWBT:  "a6f44d3e3cb0dca7e3e2aeefd1ce568386f1c0afcdae7e0ac9879b629aed3194",
			RoleWBIN: "90f2b3d3d435a22c9081960442c80962e0e867e54835b247fa31c0d43f5e7ab2",
			RoleDAT:  "1d87efeb9c0f6cbd43b8e0c6b5e895ff8789107e2f3c897b31f8507ae204605f",
			RoleFont: "75231a46cce706d5fb32222bcba71747967be38308789d43045b0bab8ccc621e",
		},
		0x00045c7e,
		"66da2ca862fd8704b4136b8476c3957fe22d702fd1c69bb120db1cdb4181d364",
	)
}

func schW340DC18Profile() BuildProfile {
	return schRawDownloadProfile(
		SCHW340DC18ProfileID,
		"SCH-W340",
		"DC18",
		map[Role]string{
			RoleWBT:  "d07018c4ddb90fa14042515a7363e5895ec9fbe8644a336d158c533f8e37288a",
			RoleWBIN: "bf7938049935bd378c95baa1b82ec03352b999425cb7b5e037f83c125fd3e781",
			RoleDAT:  "28d15d06d45116111955b67920dc65dbddd934ad8bc7ddd25cd33b624303fdb0",
			RoleFont: "360818f96042d2a844e1d158a46537743cd5d4573dc6554137976b726dd07a95",
		},
		0x00044757,
		"95b53a49c8b2365c67a9fbbb05644ccfeed69225062df503a68b5044f44d8dde",
	)
}

func schW350CK06Profile() BuildProfile {
	return schRawDownloadProfile(
		SCHW350CK06ProfileID,
		"SCH-W350",
		"CK06",
		map[Role]string{
			RoleWBT:  "b7eb96136c3621e4fcad5124cb7a85486b135fbbac133c4fcd684a73335d2c9f",
			RoleWBIN: "74a538b76b7c648ade5cba68f87a297255ac26a9d889e357c6d9b2da32c4ec57",
			RoleDAT:  "520ab99a1ee7169b4d057abd602338362e6b312340f0ed550bf085c364d7ad43",
			RoleFont: "165b468bb26ee3687a74a48659229cf25d34874a0ae72ec9ba9088c26b740782",
		},
		0x00053968,
		"3f04ed1ff6a122a84d7f6009cb87560597edfcd94a95384a6646514b3e5d995b",
	)
}

func schW410CL10Profile() BuildProfile {
	return schRawDownloadProfile(
		SCHW410CL10ProfileID,
		"SCH-W410",
		"CL10",
		map[Role]string{
			RoleWBT:  "8655518bde12f4e51b88da2653523ddddec4d2b77b2f6bd0097010385754f506",
			RoleWBIN: "895de85ad3071b5a86a1b0c799d57d045c5e20eef304fbd4c78aec41a3d878ff",
			RoleDAT:  "67b8b03dfdb50ed4af513af119e7eee0f0758f5dddea0c4ca5269c8344fd8831",
			RoleFont: "5c191716f3fd1224dbb8eeba156aba8a529469f5c7d1fba2998562c1dfb286ba",
		},
		0x00043ba0,
		"0fc474689a06d8299bb42519314bee3ec4cf0c33ddd3e8b9a15fb1cb43af4d79",
	)
}

func schRawDownloadProfile(
	id, model, build string,
	pieceHashes map[Role]string,
	oemsblUsedSize uint32,
	oemsblSHA256 string,
) BuildProfile {
	// These older version-one packages store the four downloader payloads
	// without the later 128 KiB signed wrapper. Their SBL blocks retain the
	// same 2 KiB page framing, but unused logical bytes are zero-filled.
	return BuildProfile{
		ID: id, Family: FamilySCHRawDownload, Manufacturer: "Samsung",
		Model: model, Build: build, PieceHashes: pieceHashes,
		BootImages: []BootImageSpec{
			{
				ID: "oemsbl", Role: RoleWBT,
				BlockOffsets: []int64{0x0c0000, 0x0e0000, 0x100000},
				BlockMarker:  [8]byte{0x9c, 0x12, 0x0f, 0xfa, 0xc9, 0xb6, 0x8f, 0x5a},
				HeaderSize:   PageSize, BlockSize: EraseBlockSize,
				LoadAddress: 0x000a0000, EntryOffset: 0,
				UsedSize: oemsblUsedSize, LogicalSHA256: oemsblSHA256,
			},
			{
				ID: "qcsbl", Role: RoleWBT,
				BlockOffsets: []int64{0x0a0000},
				BlockMarker:  [8]byte{0xdf, 0x5d, 0xe8, 0x5f, 0xbc, 0xce, 0x64, 0x52},
				HeaderSize:   PageSize, BlockSize: EraseBlockSize,
				LoadAddress: 0x00080000, EntryOffset: 0,
				UsedSize:      0x0000484f,
				LogicalSHA256: "551543bdca41fe33b889c81376effa276f01672310c8bda9c4c076ac4d8c1c89",
			},
		},
		MemoryImages: []MemoryImageSpec{{
			ID: "pbl-rom", Role: RoleWBT, SourceOffset: 0, Size: 0x00003948,
			LoadAddress:   0x00101000,
			LogicalSHA256: "ea0bd3a8dec21657a7da0b20485e44793062f15de6ee453774cd31bc1a78d920",
		}},
	}
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
		clone.BootImages[index].PBLBytePatches = append(
			[]BootImageBytePatch(nil), profile.BootImages[index].PBLBytePatches...,
		)
	}
	clone.MemoryImages = append([]MemoryImageSpec(nil), profile.MemoryImages...)
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

type MemoryImage struct {
	ID          string
	LoadAddress uint32
	SHA256      string
	Bytes       []byte
}

func ReconstructMemoryImage(
	set firmwareset.Set,
	pkg Package,
	spec MemoryImageSpec,
) (MemoryImage, error) {
	if err := spec.validate(); err != nil {
		return MemoryImage{}, err
	}
	metadata, ok := pkg.Pieces[spec.Role]
	if !ok {
		return MemoryImage{}, fmt.Errorf("%w: missing %s", ErrIncompleteSet, spec.Role)
	}
	piece, err := set.Piece(metadata.Index)
	if err != nil {
		return MemoryImage{}, err
	}
	if piece.SHA256() != metadata.SHA256 {
		return MemoryImage{}, fmt.Errorf("Samsung %s metadata does not match firmware set", spec.Role)
	}
	if spec.SourceOffset > uint64(piece.Size()) ||
		uint64(spec.Size) > uint64(piece.Size())-spec.SourceOffset {
		return MemoryImage{}, &FormatError{
			Role: spec.Role, Piece: piece.Index(), Offset: int64(spec.SourceOffset),
			Reason: fmt.Sprintf("memory image %q exceeds input", spec.ID),
		}
	}
	data := make([]byte, int(spec.Size))
	if _, err := piece.ReadAt(data, int64(spec.SourceOffset)); err != nil {
		return MemoryImage{}, err
	}
	digest := sha256.Sum256(data)
	digestText := hex.EncodeToString(digest[:])
	if !strings.EqualFold(digestText, spec.LogicalSHA256) {
		return MemoryImage{}, fmt.Errorf(
			"memory image %q SHA-256 %s does not match profile %s",
			spec.ID, digestText, spec.LogicalSHA256,
		)
	}
	return MemoryImage{
		ID: spec.ID, LoadAddress: spec.LoadAddress,
		SHA256: digestText, Bytes: data,
	}, nil
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
	dataOffset, dataSize, chunkSize, chunkStride, ok := spec.geometry()
	if !ok {
		return BootImage{}, fmt.Errorf("boot image %q has invalid extraction geometry", spec.ID)
	}
	logicalSize := len(spec.BlockOffsets) * int(dataSize/chunkStride) * int(chunkSize)
	logical := make([]byte, 0, logicalSize)
	for _, offset := range spec.BlockOffsets {
		if offset > piece.Size()-int64(spec.BlockSize) {
			return BootImage{}, &FormatError{
				Role: spec.Role, Piece: piece.Index(), Offset: offset,
				Reason: fmt.Sprintf("boot image %q block exceeds input", spec.ID),
			}
		}
		var marker [8]byte
		if _, err := piece.ReadAt(marker[:], offset+int64(spec.MarkerOffset)); err != nil {
			return BootImage{}, err
		}
		if marker != spec.BlockMarker {
			return BootImage{}, &FormatError{
				Role: spec.Role, Piece: piece.Index(), Offset: offset,
				Reason: fmt.Sprintf("boot image %q block marker mismatch", spec.ID),
			}
		}
		for relative := uint32(0); relative < dataSize; relative += chunkStride {
			chunk := make([]byte, int(chunkSize))
			if _, err := piece.ReadAt(chunk, offset+int64(dataOffset+relative)); err != nil {
				return BootImage{}, err
			}
			logical = append(logical, chunk...)
		}
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
	for _, patch := range spec.PBLBytePatches {
		if logical[patch.Offset] != patch.Expected {
			return BootImage{}, fmt.Errorf(
				"boot image %q PBL byte at 0x%x is 0x%02x, want 0x%02x",
				spec.ID, patch.Offset, logical[patch.Offset], patch.Expected,
			)
		}
		logical[patch.Offset] = patch.Value
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
