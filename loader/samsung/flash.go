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
// transform. SourceOffset is measured in the original wrapped piece.
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
}

// FlashImage is a bounded, read-only view of the reconstructed NAND contents.
// Unpopulated ranges read as erased bytes. Writable state belongs in a
// separate copy-on-write overlay and never mutates the user-supplied pieces.
type FlashImage struct {
	size        uint64
	erased      byte
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
			copy(regionDestination, region.decoded[regionStart:regionStart+(copyEnd-copyStart)])
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

	var flashEnd uint64
	for _, partition := range layout.Partitions {
		flashEnd = max(flashEnd, partition.End())
	}
	if flashEnd == 0 || flashEnd > MaxFlashImageBytes {
		return FlashImage{}, fmt.Errorf(
			"%w: flash size 0x%x exceeds limit 0x%x",
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
	}{
		{RoleWBT, 0, pkg.Pieces[RoleWBT].Header.PayloadSize, TransformIdentity, nil},
		{RoleWBIN, amss.Start, uint64(len(progressive.Bytes)), TransformSEEDFeedback, progressive.Bytes},
		{RoleDAT, rsrc.Start, pkg.Pieces[RoleDAT].Header.PayloadSize, TransformIdentity, nil},
		{RoleFont, font.Start, pkg.Pieces[RoleFont].Header.PayloadSize, TransformIdentity, nil},
	}
	regions := make([]flashRegion, 0, len(regionSpecs))
	for _, spec := range regionSpecs {
		metadata := pkg.Pieces[spec.role]
		piece, err := set.Piece(metadata.Index)
		if err != nil {
			return FlashImage{}, err
		}
		if spec.size == 0 || spec.start > flashEnd || spec.size > flashEnd-spec.start {
			return FlashImage{}, fmt.Errorf(
				"%w: %s range 0x%x..0x%x exceeds flash",
				ErrInvalidFlashLayout,
				spec.role,
				spec.start,
				spec.start+spec.size,
			)
		}
		outputHash := progressive.SHA256
		if spec.decoded == nil {
			outputHash, err = hashPieceRange(piece, WrapperSize, spec.size)
			if err != nil {
				return FlashImage{}, err
			}
		}
		region := flashRegion{
			metadata: FlashRegion{
				Role: spec.role, Start: spec.start, Size: spec.size,
				SourcePiece: metadata.Index, SourceOffset: WrapperSize,
				Transform: spec.transform, SourceSHA256: metadata.SHA256,
				OutputSHA256: outputHash,
			},
			piece: piece,
		}
		if spec.decoded != nil {
			region.decoded = append([]byte(nil), spec.decoded...)
		}
		regions = append(regions, region)
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
		partitions: append([]Partition(nil), layout.Partitions...),
		regions:    regions, progressive: progressive.ELF, identity: identity,
	}, nil
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
