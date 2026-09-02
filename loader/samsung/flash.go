package samsung

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/mirusu400/aram-core/firmwareset"
)

const MaxFlashImageBytes = 512 << 20

var ErrInvalidFlashLayout = errors.New("invalid Samsung flash layout")

// FlashRegion attributes a normalized flash byte range to one input piece and
// transform. SourceOffset is measured in the original input piece.
type FlashRegion struct {
	Role         Role
	Start        uint64
	Size         uint64
	SourcePiece  int
	SourceOffset uint64
	Transform    Transform
	SourceSHA256 string
	OutputSHA256 string
}

func (r FlashRegion) End() uint64 {
	return r.Start + r.Size
}

type flashRegion struct {
	metadata FlashRegion
	piece    firmwareset.Piece
	decoded  []byte
	// dataOffset identifies the first logical byte represented by this
	// physical span. A downloader region may be split when factory-bad NAND
	// blocks have to be skipped.
	dataOffset uint64
}

// FlashAssemblyOptions describes physical NAND facts which are not retained
// by a logical Samsung downloader package. WBT boot blocks are already laid
// out physically and are never remapped. Later downloader regions are logical
// NAND streams and skip the listed factory-bad erase blocks.
type FlashAssemblyOptions struct {
	FactoryBadBlocks []uint32
}

// FlashImage is a bounded, read-only view of the reconstructed NAND contents.
// Unpopulated ranges read as erased bytes. Writable state belongs in a
// separate copy-on-write overlay and never mutates the user-supplied pieces.
type FlashImage struct {
	size        uint64
	erased      byte
	pageSize    uint32
	eraseBlock  uint32
	partitions  []Partition
	regions     []flashRegion
	progressive ProgressiveELF
	identity    string
}

func (i FlashImage) Size() int64 {
	return int64(i.size)
}

func (i FlashImage) ErasedValue() byte {
	return i.erased
}

func (i FlashImage) PageSize() uint32 {
	return i.pageSize
}

func (i FlashImage) EraseBlockSize() uint32 {
	return i.eraseBlock
}

func (i FlashImage) Identity() string {
	return i.identity
}

func (i FlashImage) Regions() []FlashRegion {
	regions := make([]FlashRegion, len(i.regions))
	for index := range i.regions {
		regions[index] = i.regions[index].metadata
	}
	return regions
}

func (i FlashImage) Partitions() []Partition {
	return append([]Partition(nil), i.partitions...)
}

func (i FlashImage) ProgressiveELF() ProgressiveELF {
	result := i.progressive
	result.ProgramHeaders = append([]ELF32ProgramHeader(nil), i.progressive.ProgramHeaders...)
	return result
}

// ReadAt implements io.ReaderAt without materializing erased gaps or the
// large identity-transformed resource/font pieces in host memory.
func (i FlashImage) ReadAt(destination []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, fmt.Errorf("flash read offset 0x%x: %w", offset, ErrInvalidFlashLayout)
	}
	if len(destination) == 0 {
		return 0, nil
	}
	start := uint64(offset)
	if start >= i.size {
		return 0, io.EOF
	}
	count := uint64(len(destination))
	partial := false
	if count > i.size-start {
		count = i.size - start
		partial = true
	}
	output := destination[:int(count)]
	for index := range output {
		output[index] = i.erased
	}
	end := start + count
	for _, region := range i.regions {
		if region.metadata.End() <= start || region.metadata.Start >= end {
			continue
		}
		copyStart := max(start, region.metadata.Start)
		copyEnd := min(end, region.metadata.End())
		destinationStart := copyStart - start
		regionStart := copyStart - region.metadata.Start
		regionDestination := output[destinationStart : destinationStart+(copyEnd-copyStart)]
		if region.decoded != nil {
			decodedStart := region.dataOffset + regionStart
			copy(regionDestination, region.decoded[decodedStart:decodedStart+(copyEnd-copyStart)])
			continue
		}
		if _, err := region.piece.ReadAt(
			regionDestination,
			int64(region.metadata.SourceOffset+regionStart),
		); err != nil {
			return 0, err
		}
	}
	if partial {
		return int(count), io.EOF
	}
	return int(count), nil
}

// AssembleFlash validates and reconstructs the downloader package into the
// physical layout described by its selected MIBIB. WBIN starts at the AMSS
// partition origin; the wrapper's encoded download offset is not treated as a
// runtime flash address.
func AssembleFlash(set firmwareset.Set, pkg Package) (FlashImage, error) {
	return AssembleFlashWithOptions(set, pkg, FlashAssemblyOptions{})
}

// AssembleFlashWithOptions reconstructs a physical NAND view from Samsung's
// mixed package: WBT carries physical boot blocks, while WBIN/DAT/FNT carry
// logical data that the handset downloader writes around factory-bad blocks.
func AssembleFlashWithOptions(
	set firmwareset.Set,
	pkg Package,
	options FlashAssemblyOptions,
) (FlashImage, error) {
	if pkg.Family == FamilySCHFlexOneNANDDownload {
		return assembleFlexOneNANDFlash(set, pkg, options)
	}
	layout, err := Normalize(set, pkg)
	if err != nil {
		return FlashImage{}, err
	}
	progressive, err := DecodeWBIN(set, pkg)
	if err != nil {
		return FlashImage{}, err
	}
	amss, ok := findPartition(layout, "0:AMSS")
	if !ok {
		return FlashImage{}, fmt.Errorf("%w: MIBIB has no 0:AMSS partition", ErrInvalidFlashLayout)
	}
	rsrc, ok := findPartition(layout, "0:RSRC")
	if !ok {
		return FlashImage{}, fmt.Errorf("%w: MIBIB has no 0:RSRC partition", ErrInvalidFlashLayout)
	}
	font, ok := findPartition(layout, "0:FONT")
	if !ok {
		return FlashImage{}, fmt.Errorf("%w: MIBIB has no 0:FONT partition", ErrInvalidFlashLayout)
	}

	var logicalFlashEnd uint64
	for _, partition := range layout.Partitions {
		logicalFlashEnd = max(logicalFlashEnd, partition.End())
	}
	if logicalFlashEnd == 0 || logicalFlashEnd > MaxFlashImageBytes {
		return FlashImage{}, fmt.Errorf(
			"%w: flash size 0x%x exceeds limit 0x%x",
			ErrInvalidFlashLayout,
			logicalFlashEnd,
			MaxFlashImageBytes,
		)
	}
	if layout.PageSize == 0 || layout.EraseBlockSize < layout.PageSize ||
		layout.EraseBlockSize%layout.PageSize != 0 {
		return FlashImage{}, fmt.Errorf("%w: invalid raw NAND geometry", ErrInvalidFlashLayout)
	}
	badBlocks, err := normalizeFactoryBadBlocks(
		options.FactoryBadBlocks,
		logicalFlashEnd,
		layout.EraseBlockSize,
	)
	if err != nil {
		return FlashImage{}, err
	}
	bootBlocks, err := reconstructBootBlocks(set, pkg, badBlocks, layout)
	if err != nil {
		return FlashImage{}, err
	}
	flashEnd, err := physicalFlashEnd(logicalFlashEnd, badBlocks, layout.EraseBlockSize)
	if err != nil || flashEnd > MaxFlashImageBytes {
		return FlashImage{}, fmt.Errorf(
			"%w: physical flash size 0x%x exceeds limit 0x%x",
			ErrInvalidFlashLayout,
			flashEnd,
			MaxFlashImageBytes,
		)
	}

	regionSpecs := []struct {
		role      Role
		start     uint64
		size      uint64
		transform Transform
		decoded   []byte
		physical  bool
	}{
		{RoleWBT, 0, uint64(len(bootBlocks)), TransformBootBlocks, bootBlocks, true},
		{RoleWBIN, amss.Start, uint64(len(progressive.Bytes)), layout.Region(RoleWBIN).Transform, progressive.Bytes, false},
		{RoleDAT, rsrc.Start, pkg.Pieces[RoleDAT].Header.PayloadSize, TransformIdentity, nil, false},
		{RoleFont, font.Start, pkg.Pieces[RoleFont].Header.PayloadSize, TransformIdentity, nil, false},
	}
	regions := make([]flashRegion, 0, len(regionSpecs)+len(badBlocks))
	for _, spec := range regionSpecs {
		metadata := pkg.Pieces[spec.role]
		piece, err := set.Piece(metadata.Index)
		if err != nil {
			return FlashImage{}, err
		}
		if spec.size == 0 || spec.start > logicalFlashEnd || spec.size > logicalFlashEnd-spec.start {
			return FlashImage{}, fmt.Errorf(
				"%w: %s range 0x%x..0x%x exceeds flash",
				ErrInvalidFlashLayout,
				spec.role,
				spec.start,
				spec.start+spec.size,
			)
		}
		outputHash := progressive.SHA256
		if spec.role == RoleWBT {
			digest := sha256.Sum256(spec.decoded)
			outputHash = hex.EncodeToString(digest[:])
		} else if spec.decoded == nil {
			region := layout.Region(spec.role)
			if region == nil {
				return FlashImage{}, fmt.Errorf("%w: layout has no %s region", ErrInvalidFlashLayout, spec.role)
			}
			outputHash, err = hashPieceRange(piece, region.SourceOffset, spec.size)
			if err != nil {
				return FlashImage{}, err
			}
		}
		spans := []physicalFlashSpan{{Start: spec.start, Size: spec.size}}
		if !spec.physical {
			spans = mapLogicalFlashSpans(spec.start, spec.size, badBlocks, layout.EraseBlockSize)
		}
		decoded := spec.decoded
		if decoded != nil {
			decoded = append([]byte(nil), decoded...)
		}
		region := layout.Region(spec.role)
		if region == nil {
			return FlashImage{}, fmt.Errorf("%w: layout has no %s region", ErrInvalidFlashLayout, spec.role)
		}
		var dataOffset uint64
		for _, span := range spans {
			sourceOffset := region.SourceOffset
			if decoded == nil {
				sourceOffset += dataOffset
			}
			regions = append(regions, flashRegion{
				metadata: FlashRegion{
					Role: spec.role, Start: span.Start, Size: span.Size,
					SourcePiece: metadata.Index, SourceOffset: sourceOffset,
					Transform: spec.transform, SourceSHA256: metadata.SHA256,
					OutputSHA256: outputHash,
				},
				piece: piece, decoded: decoded, dataOffset: dataOffset,
			})
			dataOffset += span.Size
		}
	}
	sort.Slice(regions, func(left, right int) bool {
		return regions[left].metadata.Start < regions[right].metadata.Start
	})
	for index := 1; index < len(regions); index++ {
		if regions[index-1].metadata.End() > regions[index].metadata.Start {
			return FlashImage{}, fmt.Errorf(
				"%w: %s overlaps %s",
				ErrInvalidFlashLayout,
				regions[index-1].metadata.Role,
				regions[index].metadata.Role,
			)
		}
	}
	identity := identifyFlash(flashEnd, layout.Partitions, regions)
	return FlashImage{
		size: flashEnd, erased: 0xff,
		pageSize: layout.PageSize, eraseBlock: layout.EraseBlockSize,
		partitions: append([]Partition(nil), layout.Partitions...),
		regions:    regions, progressive: progressive.ELF, identity: identity,
	}, nil
}

func reconstructBootBlocks(
	set firmwareset.Set,
	pkg Package,
	badBlocks []uint32,
	layout Layout,
) ([]byte, error) {
	metadata := pkg.Pieces[RoleWBT]
	piece, err := set.Piece(metadata.Index)
	if err != nil {
		return nil, err
	}
	if layout.EraseBlockSize == 0 || metadata.Header.PayloadSize > MaxFlashImageBytes ||
		metadata.Header.PayloadSize%uint64(layout.EraseBlockSize) != 0 {
		return nil, fmt.Errorf("%w: WBT payload is not erase-block aligned", ErrInvalidFlashLayout)
	}
	region := layout.Region(RoleWBT)
	if region == nil {
		return nil, fmt.Errorf("%w: layout has no %s region", ErrInvalidFlashLayout, RoleWBT)
	}
	data := make([]byte, metadata.Header.PayloadSize)
	if _, err := piece.ReadAt(data, int64(region.SourceOffset)); err != nil {
		return nil, err
	}
	mibib, err := parseMIBIBCopies(piece)
	if err != nil {
		return nil, err
	}
	if mibib.PageSize != layout.PageSize || mibib.EraseBlockSize != layout.EraseBlockSize {
		return nil, fmt.Errorf("%w: MIBIB geometry changed during flash assembly", ErrInvalidFlashLayout)
	}
	selected := mibib.Copies[0]
	for _, candidate := range mibib.Copies[1:] {
		if candidate.Generation > selected.Generation {
			selected = candidate
		}
	}
	if uint64(selected.Offset) < region.SourceOffset {
		return nil, fmt.Errorf("%w: MIBIB source precedes WBT payload", ErrInvalidFlashLayout)
	}
	source := uint64(selected.Offset) - region.SourceOffset
	// QCSBL deliberately opens the second usable boot block. Downloader WBT
	// pieces retain multiple generation slots, so normalization promotes the
	// newest valid MIBIB copy into that physical slot without treating an
	// erased predecessor as a factory-bad NAND block.
	eraseBlockSize := uint64(layout.EraseBlockSize)
	target := physicalBlockForLogical(1, badBlocks) * eraseBlockSize
	if source > uint64(len(data))-eraseBlockSize || target > uint64(len(data))-eraseBlockSize {
		return nil, fmt.Errorf("%w: selected MIBIB placement exceeds WBT payload", ErrInvalidFlashLayout)
	}
	copy(data[target:target+eraseBlockSize], data[source:source+eraseBlockSize])
	return data, nil
}

type physicalFlashSpan struct {
	Start uint64
	Size  uint64
}

func normalizeFactoryBadBlocks(blocks []uint32, logicalFlashEnd uint64, eraseBlockSize uint32) ([]uint32, error) {
	if eraseBlockSize == 0 {
		return nil, fmt.Errorf("%w: erase-block size is zero", ErrInvalidFlashLayout)
	}
	result := append([]uint32(nil), blocks...)
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	for index, block := range result {
		if index > 0 && result[index-1] == block {
			return nil, fmt.Errorf("%w: duplicate factory-bad block 0x%x", ErrInvalidFlashLayout, block)
		}
		if uint64(block)*uint64(eraseBlockSize) >= MaxFlashImageBytes ||
			uint64(block) > logicalFlashEnd/uint64(eraseBlockSize)+uint64(len(result)) {
			return nil, fmt.Errorf("%w: factory-bad block 0x%x exceeds flash", ErrInvalidFlashLayout, block)
		}
	}
	return result, nil
}

func physicalBlockForLogical(logical uint64, badBlocks []uint32) uint64 {
	physical := logical
	for _, bad := range badBlocks {
		if uint64(bad) > physical {
			break
		}
		physical++
	}
	return physical
}

func physicalFlashEnd(logicalEnd uint64, badBlocks []uint32, eraseBlockSize uint32) (uint64, error) {
	if logicalEnd == 0 {
		return 0, nil
	}
	if eraseBlockSize == 0 {
		return 0, ErrInvalidFlashLayout
	}
	blockSize := uint64(eraseBlockSize)
	lastLogicalBlock := (logicalEnd - 1) / blockSize
	lastPhysicalBlock := physicalBlockForLogical(lastLogicalBlock, badBlocks)
	physicalEnd := lastPhysicalBlock*blockSize + (logicalEnd-1)%blockSize + 1
	if physicalEnd < logicalEnd {
		return 0, ErrInvalidFlashLayout
	}
	return physicalEnd, nil
}

func mapLogicalFlashSpans(start, size uint64, badBlocks []uint32, eraseBlockSize uint32) []physicalFlashSpan {
	spans := make([]physicalFlashSpan, 0, 1+len(badBlocks))
	blockSize := uint64(eraseBlockSize)
	for logical, remaining := start, size; remaining != 0; {
		logicalBlock := logical / blockSize
		blockOffset := logical % blockSize
		count := min(remaining, blockSize-blockOffset)
		physicalStart := physicalBlockForLogical(logicalBlock, badBlocks)*blockSize + blockOffset
		if len(spans) != 0 && spans[len(spans)-1].Start+spans[len(spans)-1].Size == physicalStart {
			spans[len(spans)-1].Size += count
		} else {
			spans = append(spans, physicalFlashSpan{Start: physicalStart, Size: count})
		}
		logical += count
		remaining -= count
	}
	return spans
}

func findPartition(layout Layout, name string) (Partition, bool) {
	for _, partition := range layout.Partitions {
		if partition.Name == name {
			return partition, true
		}
	}
	return Partition{}, false
}

func hashPieceRange(piece firmwareset.Piece, offset, size uint64) (string, error) {
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	for position := uint64(0); position < size; {
		count := min(uint64(len(buffer)), size-position)
		if _, err := piece.ReadAt(buffer[:int(count)], int64(offset+position)); err != nil {
			return "", err
		}
		_, _ = hash.Write(buffer[:int(count)])
		position += count
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func identifyFlash(size uint64, partitions []Partition, regions []flashRegion) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, "aram-samsung-flash-v1\x00")
	var scalar [8]byte
	binary.LittleEndian.PutUint64(scalar[:], size)
	_, _ = hash.Write(scalar[:])
	for _, partition := range partitions {
		_, _ = io.WriteString(hash, partition.Name)
		_, _ = hash.Write([]byte{0})
		binary.LittleEndian.PutUint64(scalar[:], partition.Start)
		_, _ = hash.Write(scalar[:])
		binary.LittleEndian.PutUint64(scalar[:], partition.Size)
		_, _ = hash.Write(scalar[:])
		binary.LittleEndian.PutUint32(scalar[:4], partition.Attributes)
		_, _ = hash.Write(scalar[:4])
	}
	for _, region := range regions {
		_, _ = io.WriteString(hash, string(region.metadata.Role))
		_, _ = hash.Write([]byte{0})
		binary.LittleEndian.PutUint64(scalar[:], region.metadata.Start)
		_, _ = hash.Write(scalar[:])
		binary.LittleEndian.PutUint64(scalar[:], region.metadata.Size)
		_, _ = hash.Write(scalar[:])
		_, _ = io.WriteString(hash, string(region.metadata.Transform))
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, region.metadata.SourceSHA256)
		_, _ = io.WriteString(hash, region.metadata.OutputSHA256)
	}
	return "samsung-flash-v1:" + hex.EncodeToString(hash.Sum(nil))
}

var _ io.ReaderAt = FlashImage{}
