package samsung

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
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

func TestExactFlatProfileInspectsReconstructsAndMapsSyntheticBytes(t *testing.T) {
	firmware := bytes.Repeat([]byte{0x31}, 0x400)
	preload := bytes.Repeat([]byte{0x72}, 0x200)
	firmwareDigest := fmt.Sprintf("%x", sha256.Sum256(firmware))
	preloadDigest := fmt.Sprintf("%x", sha256.Sum256(preload))
	resetDigest := fmt.Sprintf("%x", sha256.Sum256(firmware[:0x200]))
	profile := BuildProfile{
		ID: "samsung.synthetic.flat", Family: FamilySamsungMonolithicFlash,
		Manufacturer: "Samsung", Model: "Synthetic", Build: "TEST",
		PieceHashes: map[Role]string{
			RoleFirmware: firmwareDigest,
			RolePreload:  preloadDigest,
		},
		BootImages: []BootImageSpec{{
			ID: "reset", Role: RoleFirmware, ContiguousSize: 0x200,
			LoadAddress: 0xffff0000, UsedSize: 0x100, LogicalSHA256: resetDigest,
			MirrorAddresses: []uint32{0},
		}},
		DirectResetImage: "reset",
		FlatFlash: &FlatFlashSpec{
			Size: 0x1000, PageSize: 0x200, EraseBlockSize: 0x400,
			Regions: []FlatFlashRegionSpec{
				{Role: RoleFirmware, Start: 0},
				{Role: RolePreload, Start: 0x800},
			},
		},
	}
	registry, err := NewRegistry(profile)
	if err != nil {
		t.Fatal(err)
	}
	set, err := firmwareset.NewSet([]firmwareset.Source{
		{ReaderAt: bytes.NewReader(preload), Size: int64(len(preload))},
		{ReaderAt: bytes.NewReader(firmware), Size: int64(len(firmware))},
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := inspectWithRegistry(set, registry)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Family != FamilySamsungMonolithicFlash ||
		pkg.Pieces[RoleFirmware].Index != 1 || pkg.Pieces[RolePreload].Index != 0 {
		t.Fatalf("exact flat package = %+v", pkg)
	}
	matched, err := registry.Match(pkg)
	if err != nil || matched.ID != profile.ID {
		t.Fatalf("flat profile match = %q error %v", matched.ID, err)
	}
	reset, err := ReconstructBootImage(set, pkg, profile.BootImages[0])
	if err != nil {
		t.Fatal(err)
	}
	if reset.LoadAddress != 0xffff0000 || reset.EntryAddress != 0xffff0000 ||
		reset.UsedSize != 0x100 || !bytes.Equal(reset.Bytes, firmware[:0x200]) {
		t.Fatalf("flat reset image = %+v", reset)
	}
	image, err := AssembleFlashForProfileWithOptions(set, pkg, profile, FlashAssemblyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertFlashBytes(t, image, 0, firmware[:16])
	assertFlashBytes(t, image, 0x800, preload[:16])
	assertFlashBytes(t, image, 0x600, bytes.Repeat([]byte{0xff}, 16))
}

func TestRegistryRestrictsOpaqueWBINToRawDownloads(t *testing.T) {
	sources := syntheticDownloadSources(t)
	_, pkg := inspectSyntheticSet(t, sources)
	profile := syntheticProfile(t, pkg)
	profile.WBINFormat = WBINFormatOpaque
	if _, err := NewRegistry(profile); err == nil {
		t.Fatal("NewRegistry accepted opaque WBIN for a wrapped download")
	}

	profile.Family = FamilySCHRawDownload
	registry, err := NewRegistry(profile)
	if err != nil {
		t.Fatal(err)
	}
	if registry.profiles[0].WBINFormat != WBINFormatOpaque {
		t.Fatalf("cloned WBIN format = %q", registry.profiles[0].WBINFormat)
	}

	profile.WBINFormat = "unsupported"
	if _, err := NewRegistry(profile); err == nil {
		t.Fatal("NewRegistry accepted an unknown WBIN format")
	}
}

func TestBuiltinRegistrySeparatesSCHW830BuildsAndAdjacentBoards(t *testing.T) {
	registry := BuiltinRegistry()
	if len(registry.profiles) != 21 {
		t.Fatalf("built-in Samsung profiles = %d, want 21", len(registry.profiles))
	}
	profiles := make(map[string]BuildProfile, len(registry.profiles))
	for _, profile := range registry.profiles {
		profiles[profile.ID] = profile
	}
	dl21, dl21OK := profiles[SCHW830DL21ProfileID]
	da18, da18OK := profiles[SCHW830DA18ProfileID]
	w770, w770OK := profiles[SCHW770DA05ProfileID]
	w860, w860OK := profiles[SCHW860DA06ProfileID]
	w4200, w4200OK := profiles[SPHW4200DC17ProfileID]
	w450, w450OK := profiles[SCHW450CK10ProfileID]
	w599, w599OK := profiles[SCHW599BE30ProfileID]
	if !dl21OK || !da18OK || !w770OK || !w860OK || !w4200OK || !w450OK || !w599OK {
		t.Fatalf("built-in Samsung profile IDs = %#v", profiles)
	}
	if dl21.Build != "DL21" || da18.Build != "DA18" ||
		dl21.PieceHashes[RoleWBT] != da18.PieceHashes[RoleWBT] ||
		dl21.PieceHashes[RoleWBIN] == da18.PieceHashes[RoleWBIN] {
		t.Fatalf("SCH-W830 build profiles do not preserve shared WBT and distinct AMSS: DL21=%+v DA18=%+v", dl21, da18)
	}
	if w860.Model != "SCH-W860" || w860.Build != "DA06" ||
		w860.PieceHashes[RoleWBT] == dl21.PieceHashes[RoleWBT] {
		t.Fatalf("SCH-W860 profile is not a distinct exact board build: %+v", w860)
	}
	if w770.Model != "SCH-W770" || w770.Build != "DA05" ||
		w770.PieceHashes[RoleWBT] == dl21.PieceHashes[RoleWBT] {
		t.Fatalf("SCH-W770 profile is not a distinct exact board build: %+v", w770)
	}
	if w4200.Model != "SPH-W4200" || w4200.Build != "DC17" ||
		w4200.Family != FamilySCHRawDownload {
		t.Fatalf("SPH-W4200 profile = %+v", w4200)
	}
	w450Reset, w450ResetOK := w450.BootImage("reset")
	if w450.Model != "SCH-W450" || w450.Build != "CK10" ||
		w450.Family != FamilySamsungLegacyFlatDownload ||
		w450.DirectResetImage != "reset" || !w450ResetOK ||
		w450Reset.LoadAddress != 0xffff0000 || w450Reset.ContiguousSize != 0xf000 ||
		!reflect.DeepEqual(w450Reset.MirrorAddresses, []uint32{0}) ||
		w450.FlatFlash == nil || w450.FlatFlash.Size != 0x04c50000 {
		t.Fatalf("SCH-W450 profile = %+v", w450)
	}
	w599Reset, w599ResetOK := w599.BootImage("reset")
	if w599.Model != "SCH-W599" || w599.Build != "BE30" ||
		w599.Family != FamilySamsungMonolithicFlash ||
		w599.DirectResetImage != "reset" || !w599ResetOK ||
		w599Reset.LoadAddress != 0xffff0000 || w599Reset.ContiguousSize != 0xf000 ||
		len(w599Reset.MirrorAddresses) != 0 ||
		w599.FlatFlash == nil || w599.FlatFlash.Size != 0x05d70000 {
		t.Fatalf("SCH-W599 profile = %+v", w599)
	}
	qcsbl, ok := w770.BootImage("qcsbl")
	if !ok || len(qcsbl.BlockOffsets) != 1 || qcsbl.BlockOffsets[0] != 0x0c0000 ||
		qcsbl.LoadAddress != 0x00080000 || qcsbl.EntryOffset != 0 {
		t.Fatalf("SCH-W770 QCSBL profile = %+v", qcsbl)
	}
}

func TestBuiltinRegistryKeepsAdditionalSmallPageRawTargetsExact(t *testing.T) {
	profiles := make(map[string]BuildProfile)
	for _, profile := range BuiltinRegistry().profiles {
		profiles[profile.ID] = profile
	}
	tests := []struct {
		id, model, build, wbt string
		oemsblEntry           uint32
	}{
		{SCHW210CK12ProfileID, "SCH-W210", "CK12", "873c0dc12a58148a922755773019fb2a51766bcaf60804dd0bac89234113fce0", 0x03d9ca26},
		{SCHW240CL28ProfileID, "SCH-W240", "CL28", "7c7a52555ff528ccfc596f0efef1eeeff4e3fd9ab994cc4e9fe3f992a6ccc9f3", 0x03d9c91e},
		{SCHW290CK10ProfileID, "SCH-W290", "CK10", "ef6a490e7d31edb04fda399ee0b606954a408a3dda3e2a2e8d5a9d6f662bb490", 0x00a50d20},
		{SCHW330CK06ProfileID, "SCH-W330", "CK06", "14236a18f79bd54f15fa22d8276e364524f3e220aaec9140606bed6eff330c33", 0x000a0664},
		{SCHW390CK11ProfileID, "SCH-W390", "CK11", "9d990da9e29450084f069359c60a4f09efeceddec93994522447b69003d6008d", 0x000a0640},
		{SCHW460CC26ProfileID, "SCH-W460", "CC26", "59388283d06af34c0e266457ff854ab3010b03d00158a7348569c8af6d51c7c6", 0x000a077c},
	}
	for _, test := range tests {
		profile, ok := profiles[test.id]
		if !ok || profile.Family != FamilySCHRawDownload ||
			profile.Model != test.model || profile.Build != test.build ||
			profile.WBINFormat != WBINFormatOpaque || profile.PieceHashes[RoleWBT] != test.wbt {
			t.Fatalf("small-page raw profile %q = %+v", test.id, profile)
		}
		oemsbl, ok := profile.BootImage("oemsbl")
		if !ok || oemsbl.LoadAddress+oemsbl.EntryOffset != test.oemsblEntry {
			t.Fatalf("small-page raw profile %q OEMSBL = %+v", test.id, oemsbl)
		}
	}

	w210 := profiles[SCHW210CK12ProfileID]
	w210QCSBL, _ := w210.BootImage("qcsbl")
	w210PBL, w210HasPBL := w210.MemoryImage("pbl-rom")
	if len(w210QCSBL.BlockOffsets) != 1 || w210QCSBL.BlockOffsets[0] != 0x40000 ||
		w210QCSBL.EntryOffset != 0x0fd8 || !w210QCSBL.PBLPreload ||
		len(w210QCSBL.PBLBytePatches) != 1 || !w210HasPBL ||
		w210PBL.LoadAddress != 0x03d4c000 || len(w210PBL.PBLBytePatches) != 14 {
		t.Fatalf("SCH-W210 boot profile = QCSBL %+v PBL %+v", w210QCSBL, w210PBL)
	}

	for _, id := range []string{SCHW240CL28ProfileID, SCHW290CK10ProfileID} {
		profile := profiles[id]
		qcsbl, _ := profile.BootImage("qcsbl")
		pbl, hasPBL := profile.MemoryImage("pbl-source")
		_, hasMappedPBL := profile.MemoryImage("pbl-rom")
		wantPBLSize := uint32(0x4388)
		if id == SCHW290CK10ProfileID {
			wantPBLSize = 0x43a0
		}
		if len(qcsbl.BlockOffsets) != 1 || qcsbl.BlockOffsets[0] != 0x40000 ||
			qcsbl.EntryOffset != 0x0f34 || qcsbl.UsedSize != 0x1498 ||
			qcsbl.PBLRelocationAddress != 0x78010000 || qcsbl.PBLPreload ||
			!hasPBL || hasMappedPBL || pbl.LoadAddress != 0xffff0000 || pbl.Size != wantPBLSize {
			t.Fatalf("%s boot profile = QCSBL %+v PBL %+v", id, qcsbl, pbl)
		}
	}

	for _, id := range []string{SCHW330CK06ProfileID, SCHW390CK11ProfileID, SCHW460CC26ProfileID} {
		profile := profiles[id]
		qcsbl, _ := profile.BootImage("qcsbl")
		if !reflect.DeepEqual(qcsbl.BlockOffsets, []int64{0x40000, 0x44000, 0x48000}) ||
			qcsbl.EntryOffset != 0x28 || qcsbl.HeaderSize != smallPageSize ||
			qcsbl.BlockSize != smallEraseBlockSize || len(profile.MemoryImages) != 0 {
			t.Fatalf("%s three-block QCSBL profile = %+v", id, qcsbl)
		}
	}
}

func TestBuiltinRegistryKeepsRawVersionOneTargetsExact(t *testing.T) {
	profiles := make(map[string]BuildProfile)
	for _, profile := range BuiltinRegistry().profiles {
		profiles[profile.ID] = profile
	}
	expected := map[string]struct {
		model, build, wbt string
	}{
		SCHW320DC18ProfileID: {"SCH-W320", "DC18", "a6f44d3e3cb0dca7e3e2aeefd1ce568386f1c0afcdae7e0ac9879b629aed3194"},
		SCHW340DC18ProfileID: {"SCH-W340", "DC18", "d07018c4ddb90fa14042515a7363e5895ec9fbe8644a336d158c533f8e37288a"},
		SCHW350CK06ProfileID: {"SCH-W350", "CK06", "b7eb96136c3621e4fcad5124cb7a85486b135fbbac133c4fcd684a73335d2c9f"},
		SCHW410CL10ProfileID: {"SCH-W410", "CL10", "8655518bde12f4e51b88da2653523ddddec4d2b77b2f6bd0097010385754f506"},
		SCHW300DA04ProfileID: {"SCH-W300", "DA04", "6940ee63e557b0ef81291f0f59371019f7617ee021b42b93cf6e809fd81a3a36"},
		SCHW420CD16ProfileID: {"SCH-W420", "CD16", "6550560f94d996ec9c39cb703841904033ff2cb39eff838afd37cf3dda8c609c"},
	}
	for id, want := range expected {
		profile, ok := profiles[id]
		if !ok || profile.Family != FamilySCHRawDownload || profile.Model != want.model ||
			profile.Build != want.build || profile.PieceHashes[RoleWBT] != want.wbt {
			t.Fatalf("raw profile %q = %+v", id, profile)
		}
		qcsbl, ok := profile.BootImage("qcsbl")
		if !ok || len(qcsbl.BlockOffsets) != 1 || qcsbl.BlockOffsets[0] != 0x0a0000 ||
			qcsbl.LoadAddress != 0x00080000 || qcsbl.EntryOffset != 0 {
			t.Fatalf("raw profile %q QCSBL = %+v", id, qcsbl)
		}
		pbl, ok := profile.MemoryImage("pbl-rom")
		if !ok || pbl.SourceOffset != 0 || pbl.Size != 0x3948 ||
			pbl.LoadAddress != 0x00101000 {
			t.Fatalf("raw profile %q PBL ROM = %+v", id, pbl)
		}
	}
	for id, entry := range map[string]uint32{
		SCHW300DA04ProfileID: 0x000a05e4,
		SCHW420CD16ProfileID: 0x000a09a0,
	} {
		oemsbl, ok := profiles[id].BootImage("oemsbl")
		if !ok || oemsbl.LoadAddress+oemsbl.EntryOffset != entry {
			t.Fatalf("raw profile %q OEMSBL = %+v, want entry %#x", id, oemsbl, entry)
		}
	}
	w270, ok := profiles[SCHW270CL28ProfileID]
	if !ok || w270.Family != FamilySCHRawDownload || w270.Model != "SCH-W270" ||
		w270.Build != "CL28" || w270.WBINFormat != WBINFormatOpaque ||
		w270.PieceHashes[RoleWBT] != "7899654ba29cf597df88279d96b26c2867e2b342da96169ddad5daeae78c37a4" {
		t.Fatalf("raw profile %q = %+v", SCHW270CL28ProfileID, w270)
	}
	qcsbl, ok := w270.BootImage("qcsbl")
	if !ok || len(qcsbl.BlockOffsets) != 1 || qcsbl.BlockOffsets[0] != 0x40000 ||
		qcsbl.HeaderSize != smallPageSize || qcsbl.BlockSize != smallEraseBlockSize ||
		qcsbl.LoadAddress != 0x00080000 || qcsbl.EntryOffset != 0x0fd8 ||
		qcsbl.UsedSize != 0x1504 || !qcsbl.PBLPreload ||
		!reflect.DeepEqual(qcsbl.PBLBytePatches, []BootImageBytePatch{{
			Offset: 0x240, Expected: 0xd8, Value: 0xa0,
		}}) {
		t.Fatalf("SCH-W270 QCSBL = %+v", qcsbl)
	}
	oemsbl, ok := w270.BootImage("oemsbl")
	if !ok || len(oemsbl.BlockOffsets) != 23 || oemsbl.BlockOffsets[0] != 0x80000 ||
		oemsbl.BlockOffsets[len(oemsbl.BlockOffsets)-1] != 0xd8000 ||
		oemsbl.LoadAddress != 0x03d9c000 || oemsbl.EntryOffset != 0x0a1e {
		t.Fatalf("SCH-W270 OEMSBL = %+v", oemsbl)
	}
	pbl, ok := w270.MemoryImage("pbl-rom")
	if !ok || pbl.Size != 0x42f0 || pbl.LoadAddress != 0x03d4c000 ||
		len(pbl.PBLBytePatches) != 14 {
		t.Fatalf("SCH-W270 PBL ROM = %+v", pbl)
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
	relocatedSpec := spec
	relocatedSpec.PBLRelocationAddress = 0x00100000
	if err := relocatedSpec.validate(); err != nil {
		t.Fatalf("valid PBL relocation rejected: %v", err)
	}
	relocatedSpec.PBLRelocationAddress = relocatedSpec.LoadAddress
	if err := relocatedSpec.validate(); err == nil {
		t.Fatal("boot image accepted a PBL relocation at its load address")
	}
	relocatedSpec.PBLRelocationAddress = 0xffff0000
	if err := relocatedSpec.validate(); err == nil {
		t.Fatal("boot image accepted an overflowing PBL relocation")
	}

	patchedSpec := spec
	patchedSpec.PBLPreload = true
	patchedSpec.PBLBytePatches = []BootImageBytePatch{{
		Offset: 0, Expected: logical[0], Value: logical[0] ^ 0xff,
	}}
	patched, err := ReconstructBootImage(set, pkg, patchedSpec)
	if err != nil {
		t.Fatal(err)
	}
	if patched.Bytes[0] != logical[0]^0xff {
		t.Fatalf("PBL-patched boot byte = 0x%02x", patched.Bytes[0])
	}
	patchedSpec.PBLBytePatches[0].Expected ^= 0xff
	if _, err := ReconstructBootImage(set, pkg, patchedSpec); err == nil {
		t.Fatal("ReconstructBootImage accepted the wrong PBL patch preimage")
	}

	spec.BlockMarker[0] ^= 0xff
	if _, err := ReconstructBootImage(set, pkg, spec); err == nil {
		t.Fatal("ReconstructBootImage accepted the wrong block marker")
	}
}

func TestReconstructMemoryImageReadsExactVerifiedRange(t *testing.T) {
	sources := syntheticRawDownloadSources(t)
	set, pkg := inspectSyntheticSet(t, sources)
	wbt := readSyntheticSource(t, sources[RoleWBT])
	digest := sha256.Sum256(wbt[:32])
	spec := MemoryImageSpec{
		ID: "synthetic-rom", Role: RoleWBT, SourceOffset: 0, Size: 32,
		LoadAddress: 0x00101000, LogicalSHA256: fmt.Sprintf("%x", digest),
	}
	image, err := ReconstructMemoryImage(set, pkg, spec)
	if err != nil {
		t.Fatal(err)
	}
	if image.LoadAddress != spec.LoadAddress || !bytes.Equal(image.Bytes, wbt[:32]) {
		t.Fatalf("reconstructed memory image = %+v", image)
	}
	patchedSpec := spec
	patchedSpec.PBLBytePatches = []BootImageBytePatch{{
		Offset: 3, Expected: wbt[3], Value: wbt[3] ^ 0xff,
	}}
	patched, err := ReconstructMemoryImage(set, pkg, patchedSpec)
	if err != nil {
		t.Fatal(err)
	}
	if patched.Bytes[3] != wbt[3]^0xff {
		t.Fatalf("PBL-patched memory byte = 0x%02x", patched.Bytes[3])
	}
	patchedSpec.PBLBytePatches[0].Expected ^= 0xff
	if _, err := ReconstructMemoryImage(set, pkg, patchedSpec); err == nil {
		t.Fatal("ReconstructMemoryImage accepted the wrong PBL patch preimage")
	}
	spec.LogicalSHA256 = fmt.Sprintf("%064d", 1)
	if _, err := ReconstructMemoryImage(set, pkg, spec); err == nil {
		t.Fatal("ReconstructMemoryImage accepted the wrong hash")
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
