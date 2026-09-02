package samsung

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/mirusu400/aram-core/firmwareset"
)

const (
	flexOneNANDPageSize       = uint64(0x1000)
	flexOneNANDSLCBlockSize   = uint64(0x080000)
	flexOneNANDMLCBlockSize   = uint64(0x100000)
	flexOneNANDSLCBlockCount  = uint32(8)
	flexOneNANDPhysicalSize   = uint64(0x20000000)
	flexOneNANDMIBIBBlockSize = int64(0x080000)

	flexOneNANDRawSLCBlockCount = uint32(0x0010)
	flexOneNANDRawBlockCount    = uint32(0x0400)
	flexOneNANDRawSLCBlockSize  = uint64(0x040000)
	flexOneNANDRawMLCBlockSize  = uint64(0x080000)
)

// NormalizeFlexOneNAND resolves the hybrid SLC/MLC partition geometry used by
// the supported MSM7600 dual-processor package. The first eight partition
// blocks are 512 KiB SLC blocks; later entries name 1 MiB MLC blocks.
func NormalizeFlexOneNAND(set firmwareset.Set, pkg Package) (Layout, error) {
	if pkg.Family != FamilySCHFlexOneNANDDownload {
		return Layout{}, fmt.Errorf("unsupported Samsung package family %q", pkg.Family)
	}
	if missing := pkg.MissingRoles(); len(missing) != 0 {
		return Layout{}, fmt.Errorf("%w: missing %s", ErrIncompleteSet, joinRoles(missing))
	}
	pieces := make(map[Role]firmwareset.Piece, len(flexOneNANDRequiredRoles))
	for _, role := range flexOneNANDRequiredRoles {
		metadata := pkg.Pieces[role]
		piece, err := set.Piece(metadata.Index)
		if err != nil {
			return Layout{}, err
		}
		if piece.SHA256() != metadata.SHA256 {
			return Layout{}, fmt.Errorf("Samsung %s metadata does not match firmware set", role)
		}
		header, err := inspectFlexOneNANDPiece(piece)
		if err != nil {
			return Layout{}, err
		}
		if header != metadata.Header {
			return Layout{}, fmt.Errorf("Samsung %s header metadata does not match firmware set", role)
		}
		pieces[role] = piece
	}

	copies, err := parseFlexOneNANDMIBIBCopies(pieces[RoleWBT])
	if err != nil {
		return Layout{}, err
	}
	selected := copies[0]
	for _, candidate := range copies[1:] {
		if candidate.Generation > selected.Generation {
			selected = candidate
		}
	}
	byName := make(map[string]Partition, len(selected.Partitions))
	for _, partition := range selected.Partitions {
		byName[partition.Name] = partition
	}
	rolePartitions := []struct {
		role Role
		name string
	}{
		{RoleWBIN, "0:AMSS"},
		{RoleABIN, "0:APPS"},
		{RoleDAT, "0:DATA"},
		{RoleFont, "0:FONT"},
	}
	regions := []DownloadRegion{{
		Role: RoleWBT, Start: 0, Size: uint64(pieces[RoleWBT].Size()), Transform: TransformIdentity,
	}}
	packagedEnd := regions[0].Size
	for _, mapping := range rolePartitions {
		partition, ok := byName[mapping.name]
		if !ok {
			return Layout{}, &FormatError{
				Role: RoleWBT, Piece: pieces[RoleWBT].Index(),
				Reason: fmt.Sprintf("Flex-OneNAND MIBIB has no %s partition", mapping.name),
			}
		}
		size := uint64(pieces[mapping.role].Size())
		if size == 0 || size > partition.Size {
			return Layout{}, &FormatError{
				Role: mapping.role, Piece: pieces[mapping.role].Index(),
				Reason: fmt.Sprintf("payload size 0x%x exceeds %s partition", size, mapping.name),
			}
		}
		regions = append(regions, DownloadRegion{
			Role: mapping.role, Start: partition.Start, Size: size, Transform: TransformIdentity,
		})
		packagedEnd = max(packagedEnd, partition.Start+size)
	}
	sort.Slice(regions, func(left, right int) bool { return regions[left].Start < regions[right].Start })
	for index, region := range regions {
		if region.Start > flexOneNANDPhysicalSize || region.Size > flexOneNANDPhysicalSize-region.Start {
			return Layout{}, fmt.Errorf("Samsung %s region has invalid Flex-OneNAND geometry", region.Role)
		}
		if index != 0 && regions[index-1].Start+regions[index-1].Size > region.Start {
			return Layout{}, fmt.Errorf("Samsung %s region overlaps %s", region.Role, regions[index-1].Role)
		}
	}
	return Layout{
		Family: pkg.Family, MIBIBVersion: selected.Version,
		MIBIBGeneration: selected.Generation, PackagedEnd: packagedEnd,
		Partitions: append([]Partition(nil), selected.Partitions...), Regions: regions,
	}, nil
}

func parseFlexOneNANDMIBIBCopies(piece firmwareset.Piece) ([]mibibCopy, error) {
	var copies []mibibCopy
	for offset := int64(0); offset+int64(flexOneNANDPageSize*2) <= piece.Size(); offset += flexOneNANDMIBIBBlockSize {
		var header [16]byte
		if _, err := piece.ReadAt(header[:], offset); err != nil {
			return nil, err
		}
		if binary.LittleEndian.Uint32(header[0:4]) != mibibHeaderMagic[0] ||
			binary.LittleEndian.Uint32(header[4:8]) != mibibHeaderMagic[1] {
			continue
		}
		version := binary.LittleEndian.Uint32(header[8:12])
		if version != mibibHeaderMagic[2] {
			return nil, &FormatError{
				Role: RoleWBT, Piece: piece.Index(), Offset: offset + 8,
				Reason: fmt.Sprintf("unsupported Flex-OneNAND MIBIB version %d", version),
			}
		}
		primary := make([]byte, flexOneNANDPageSize)
		if _, err := piece.ReadAt(primary, offset+int64(flexOneNANDPageSize)); err != nil {
			return nil, err
		}
		copy, err := parseFlexOneNANDMIBIBCopy(piece.Index(), offset, header[:], primary)
		if err != nil {
			return nil, err
		}
		copies = append(copies, copy)
	}
	if len(copies) == 0 {
		return nil, &FormatError{
			Role: RoleWBT, Piece: piece.Index(), Reason: ErrMIBIBNotFound.Error(), Err: ErrMIBIBNotFound,
		}
	}
	return copies, nil
}

func parseFlexOneNANDMIBIBCopy(piece int, base int64, header, primary []byte) (mibibCopy, error) {
	fail := func(relative int64, reason string) (mibibCopy, error) {
		return mibibCopy{}, &FormatError{Role: RoleWBT, Piece: piece, Offset: base + relative, Reason: reason}
	}
	if len(header) < 16 || len(primary) != int(flexOneNANDPageSize) {
		return fail(0, "invalid Flex-OneNAND MIBIB geometry")
	}
	version := binary.LittleEndian.Uint32(header[8:12])
	for index, magic := range primaryTableMagic[:2] {
		if binary.LittleEndian.Uint32(primary[index*4:]) != magic {
			return fail(int64(flexOneNANDPageSize)+int64(index*4), "primary partition-table magic mismatch")
		}
	}
	if primaryVersion := binary.LittleEndian.Uint32(primary[8:12]); primaryVersion != version {
		return fail(int64(flexOneNANDPageSize)+8, "primary partition-table version mismatch")
	}
	count := binary.LittleEndian.Uint32(primary[12:16])
	maxEntries := uint32((len(primary) - 16) / mibibEntrySize)
	if count == 0 || count > maxEntries {
		return fail(int64(flexOneNANDPageSize)+12, fmt.Sprintf("invalid primary partition count %d", count))
	}
	partitions := make([]Partition, 0, count)
	seen := make(map[string]struct{}, count)
	for index := uint32(0); index < count; index++ {
		relative := 16 + int(index)*mibibEntrySize
		entry := primary[relative : relative+mibibEntrySize]
		nameEnd := 0
		for nameEnd < 16 && entry[nameEnd] != 0 {
			if entry[nameEnd] < 0x21 || entry[nameEnd] > 0x7e {
				return fail(int64(flexOneNANDPageSize)+int64(relative+nameEnd), "partition name is not printable ASCII")
			}
			nameEnd++
		}
		if nameEnd == 0 || nameEnd == 16 {
			return fail(int64(flexOneNANDPageSize)+int64(relative), "partition name is empty or unterminated")
		}
		name := string(entry[:nameEnd])
		if _, duplicate := seen[name]; duplicate {
			return fail(int64(flexOneNANDPageSize)+int64(relative), fmt.Sprintf("duplicate partition %q", name))
		}
		seen[name] = struct{}{}
		startBlock := binary.LittleEndian.Uint32(entry[16:20])
		blockCount := binary.LittleEndian.Uint32(entry[20:24])
		start, startOK := flexOneNANDOffset(startBlock)
		if !startOK {
			return fail(int64(flexOneNANDPageSize)+int64(relative+16), fmt.Sprintf("partition %q start exceeds media", name))
		}
		end := flexOneNANDPhysicalSize
		if blockCount != ^uint32(0) {
			if blockCount == 0 || startBlock > ^uint32(0)-blockCount {
				return fail(int64(flexOneNANDPageSize)+int64(relative+20), fmt.Sprintf("partition %q has invalid block count", name))
			}
			var endOK bool
			end, endOK = flexOneNANDOffset(startBlock + blockCount)
			if !endOK || end <= start {
				return fail(int64(flexOneNANDPageSize)+int64(relative+20), fmt.Sprintf("partition %q exceeds media", name))
			}
		} else if index != count-1 {
			return fail(int64(flexOneNANDPageSize)+int64(relative+20), fmt.Sprintf("partition %q is unexpectedly open-ended", name))
		}
		partition := Partition{
			Name: name, StartBlock: startBlock, BlockCount: blockCount,
			Attributes: binary.LittleEndian.Uint32(entry[24:28]), Start: start, Size: end - start,
		}
		if len(partitions) != 0 && partitions[len(partitions)-1].End() > partition.Start {
			return fail(int64(flexOneNANDPageSize)+int64(relative+16), fmt.Sprintf("partition %q overlaps its predecessor", name))
		}
		partitions = append(partitions, partition)
	}
	return mibibCopy{
		Offset: base, Version: version, Generation: binary.LittleEndian.Uint32(header[12:16]),
		Partitions: partitions,
	}, nil
}

func flexOneNANDOffset(block uint32) (uint64, bool) {
	if block <= flexOneNANDSLCBlockCount {
		return uint64(block) * flexOneNANDSLCBlockSize, true
	}
	offset := uint64(flexOneNANDSLCBlockCount)*flexOneNANDSLCBlockSize +
		uint64(block-flexOneNANDSLCBlockCount)*flexOneNANDMLCBlockSize
	return offset, offset <= flexOneNANDPhysicalSize
}

// flexOneNANDRawFBAOffset maps the MSM7600 controller's packed raw FBA into
// the byte-addressed flash view. FBAs 0..15 are 256 KiB SLC blocks; the
// remaining FBAs are 512 KiB MLC blocks.
func flexOneNANDRawFBAOffset(block uint32) (uint64, bool) {
	if block >= flexOneNANDRawBlockCount {
		return 0, false
	}
	if block < flexOneNANDRawSLCBlockCount {
		return uint64(block) * flexOneNANDRawSLCBlockSize, true
	}
	return uint64(flexOneNANDRawSLCBlockCount)*flexOneNANDRawSLCBlockSize +
		uint64(block-flexOneNANDRawSLCBlockCount)*flexOneNANDRawMLCBlockSize, true
}

func flexOneNANDRawFBASize(block uint32) (uint64, bool) {
	if block >= flexOneNANDRawBlockCount {
		return 0, false
	}
	if block < flexOneNANDRawSLCBlockCount {
		return flexOneNANDRawSLCBlockSize, true
	}
	return flexOneNANDRawMLCBlockSize, true
}

func flexOneNANDPartitionContainsBlock(partitions []Partition, block uint32) bool {
	for _, partition := range partitions {
		if block < partition.StartBlock {
			continue
		}
		if partition.BlockCount == ^uint32(0) || block-partition.StartBlock < partition.BlockCount {
			return true
		}
	}
	return false
}

func flexOneNANDBootTarget(spec BootImageSpec, partitions []Partition) (uint64, error) {
	if len(spec.BlockOffsets) != 1 || int64(spec.BlockSize) != flexOneNANDMIBIBBlockSize ||
		spec.BlockOffsets[0] < 0 || spec.BlockOffsets[0]%flexOneNANDMIBIBBlockSize != 0 {
		return 0, fmt.Errorf("boot image %q has invalid Flex-OneNAND container geometry", spec.ID)
	}
	block64 := uint64(spec.BlockOffsets[0] / flexOneNANDMIBIBBlockSize)
	if block64 > uint64(^uint32(0)) {
		return 0, fmt.Errorf("boot image %q Flex-OneNAND block overflows", spec.ID)
	}
	block := uint32(block64)
	if !flexOneNANDPartitionContainsBlock(partitions, block) {
		return 0, fmt.Errorf("boot image %q block %d is absent from Flex-OneNAND MIBIB", spec.ID, block)
	}
	offset, ok := flexOneNANDRawFBAOffset(block)
	if !ok {
		return 0, fmt.Errorf("boot image %q raw FBA %d exceeds Flex-OneNAND media", spec.ID, block)
	}
	return offset, nil
}

func transformFlexOneNANDBoot(
	set firmwareset.Set,
	pkg Package,
	layout Layout,
	profile BuildProfile,
) ([]byte, string, error) {
	spec, ok := profile.BootImage("oemsbl")
	if !ok {
		return nil, "", fmt.Errorf("firmware profile %q has no OEMSBL image", profile.ID)
	}
	target, err := flexOneNANDBootTarget(spec, layout.Partitions)
	if err != nil {
		return nil, "", err
	}
	image, err := ReconstructBootImage(set, pkg, spec)
	if err != nil {
		return nil, "", err
	}
	metadata := pkg.Pieces[RoleWBT]
	piece, err := set.Piece(metadata.Index)
	if err != nil {
		return nil, "", err
	}
	decoded := make([]byte, int(piece.Size()))
	if _, err := piece.ReadAt(decoded, 0); err != nil {
		return nil, "", err
	}
	block := uint32(uint64(spec.BlockOffsets[0]) / uint64(flexOneNANDMIBIBBlockSize))
	rawBlockSize, ok := flexOneNANDRawFBASize(block)
	if !ok {
		return nil, "", fmt.Errorf("boot image %q raw FBA %d exceeds Flex-OneNAND media", spec.ID, block)
	}
	source := uint64(spec.BlockOffsets[0])
	if err := placeFlexOneNANDBoot(
		decoded,
		source,
		target,
		uint64(spec.HeaderSize),
		rawBlockSize,
		image,
	); err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(decoded)
	return decoded, hex.EncodeToString(digest[:]), nil
}

func placeFlexOneNANDBoot(
	flash []byte,
	source uint64,
	target uint64,
	headerSize uint64,
	rawBlockSize uint64,
	image BootImage,
) error {
	usedSize := uint64(image.UsedSize)
	if headerSize > rawBlockSize || usedSize > rawBlockSize-headerSize ||
		usedSize > uint64(len(image.Bytes)) ||
		source > uint64(len(flash)) || headerSize > uint64(len(flash))-source ||
		target > uint64(len(flash)) || headerSize+usedSize > uint64(len(flash))-target {
		return fmt.Errorf("boot image %q does not fit raw Flex-OneNAND FBA", image.ID)
	}
	// QCSBL validates and skips the physical block-header page, then copies the
	// internal OEMSBL header and code beginning at page one into load RAM.
	copy(flash[int(target):int(target+headerSize)], flash[int(source):int(source+headerSize)])
	payloadTarget := target + headerSize
	copy(flash[int(payloadTarget):int(payloadTarget+usedSize)], image.Bytes[:int(usedSize)])
	return nil
}

func assembleFlexOneNANDFlash(set firmwareset.Set, pkg Package, options FlashAssemblyOptions) (FlashImage, error) {
	if len(options.FactoryBadBlocks) != 0 {
		return FlashImage{}, fmt.Errorf("%w: Flex-OneNAND factory-bad remapping is not encoded by this package", ErrInvalidFlashLayout)
	}
	layout, err := NormalizeFlexOneNAND(set, pkg)
	if err != nil {
		return FlashImage{}, err
	}
	progressive, err := DecodeWBIN(set, pkg)
	if err != nil {
		return FlashImage{}, err
	}
	var transformedBoot []byte
	var transformedBootHash string
	profile, matchErr := BuiltinRegistry().Match(pkg)
	switch {
	case matchErr == nil && profile.ID == SCHW850CF11ProfileID:
		transformedBoot, transformedBootHash, err = transformFlexOneNANDBoot(set, pkg, layout, profile)
		if err != nil {
			return FlashImage{}, err
		}
	case matchErr != nil && !errors.Is(matchErr, ErrUnknownBuild):
		return FlashImage{}, matchErr
	}
	regions := make([]flashRegion, 0, len(layout.Regions))
	for _, spec := range layout.Regions {
		metadata := pkg.Pieces[spec.Role]
		piece, pieceErr := set.Piece(metadata.Index)
		if pieceErr != nil {
			return FlashImage{}, pieceErr
		}
		region := flashRegion{
			metadata: FlashRegion{
				Role: spec.Role, Start: spec.Start, Size: spec.Size,
				SourcePiece: metadata.Index, SourceOffset: spec.SourceOffset,
				Transform: spec.Transform, SourceSHA256: metadata.SHA256,
				OutputSHA256: metadata.SHA256,
			},
			piece: piece,
		}
		if spec.Role == RoleWBT && transformedBoot != nil {
			region.metadata.Transform = TransformFlexOneNANDBoot
			region.metadata.OutputSHA256 = transformedBootHash
			region.decoded = transformedBoot
		}
		regions = append(regions, region)
	}
	identity := identifyFlash(flexOneNANDPhysicalSize, layout.Partitions, regions)
	return FlashImage{
		size: flexOneNANDPhysicalSize, erased: 0xff,
		pageSize: PageSize, eraseBlock: EraseBlockSize,
		partitions: append([]Partition(nil), layout.Partitions...), regions: regions,
		progressive: progressive.ELF, identity: identity,
	}, nil
}
