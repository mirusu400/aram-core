// Package samsung parses Samsung feature-phone download pieces into bounded,
// filename-independent metadata. Recognition is separate from platform
// support: a valid package does not imply that its SoC can execute yet.
package samsung

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mirusu400/aram-core/firmwareset"
)

const (
	FamilySCHDownload    = "samsung-sch-download-v1"
	FamilySCHRawDownload = "samsung-sch-download-raw-v1"
	// FamilySCHFlexOneNANDDownload identifies the later dual-processor SCH
	// package whose modem and application ELF images are separate and whose
	// boot piece carries a Samsung Flex-OneNAND partition table.
	FamilySCHFlexOneNANDDownload = "samsung-sch-flex-onenand-download-v1"
	WrapperSize                  = 0x20000
	WrapperMagic                 = 0xef3871ad
	EraseBlockSize               = 0x20000
	PageSize                     = 0x800
	mibibEntrySize               = 28
)

var (
	ErrNotSCHDownload = errors.New("not a Samsung SCH download piece")
	ErrDuplicateRole  = errors.New("duplicate Samsung download role")
	ErrIncompleteSet  = errors.New("incomplete Samsung download set")
	ErrMIBIBNotFound  = errors.New("MIBIB partition table not found")
)

type Role string

const (
	RoleWBT  Role = "wbt"
	RoleWBIN Role = "wbin"
	RoleABIN Role = "abin"
	RoleDAT  Role = "dat"
	RoleFont Role = "font"
)

var requiredRoles = []Role{RoleWBT, RoleWBIN, RoleDAT, RoleFont}
var flexOneNANDRequiredRoles = []Role{RoleWBT, RoleWBIN, RoleABIN, RoleDAT, RoleFont}

var roleTokens = map[Role]uint32{
	RoleWBT:  0xb07b1d96,
	RoleWBIN: 0xed1684c0,
	RoleDAT:  0x7a5484da,
	RoleFont: 0x27e3f0fe,
}

var tokenRoles = func() map[uint32]Role {
	roles := make(map[uint32]Role, len(roleTokens))
	for role, token := range roleTokens {
		roles[token] = role
	}
	return roles
}()

type FormatError struct {
	Role   Role
	Piece  int
	Offset int64
	Reason string
	Err    error
}

func (e *FormatError) Error() string {
	role := string(e.Role)
	if role == "" {
		role = "unknown"
	}
	return fmt.Sprintf(
		"Samsung %s piece %d at offset 0x%x: %s",
		role,
		e.Piece,
		e.Offset,
		e.Reason,
	)
}

func (e *FormatError) Unwrap() error {
	return e.Err
}

type Header struct {
	Role        Role
	Token       uint32
	Build       string
	PayloadSize uint64
}

type Piece struct {
	Index  int
	SHA256 string
	Header Header
}

type Package struct {
	Family string
	Pieces map[Role]Piece
}

func Inspect(set firmwareset.Set) (Package, error) {
	pkg := Package{Pieces: make(map[Role]Piece)}
	for index := 0; index < set.Len(); index++ {
		piece, err := set.Piece(index)
		if err != nil {
			return Package{}, err
		}
		family, header, err := inspectPiece(piece)
		if err != nil {
			return Package{}, err
		}
		if pkg.Family == "" {
			pkg.Family = family
		} else if pkg.Family != family {
			return Package{}, &FormatError{
				Role: header.Role, Piece: piece.Index(), Offset: 0,
				Reason: fmt.Sprintf("piece family %q does not match package family %q", family, pkg.Family),
				Err:    ErrNotSCHDownload,
			}
		}
		if previous, exists := pkg.Pieces[header.Role]; exists {
			return Package{}, fmt.Errorf(
				"%w %q at pieces %d and %d",
				ErrDuplicateRole,
				header.Role,
				previous.Index,
				index,
			)
		}
		pkg.Pieces[header.Role] = Piece{
			Index:  index,
			SHA256: piece.SHA256(),
			Header: header,
		}
	}
	return pkg, nil
}

func inspectPiece(piece firmwareset.Piece) (string, Header, error) {
	header, err := inspectHeader(piece)
	if err == nil {
		return FamilySCHDownload, header, nil
	}
	if !errors.Is(err, ErrNotSCHDownload) {
		return "", Header{}, err
	}
	header, flexErr := inspectFlexOneNANDPiece(piece)
	if flexErr == nil {
		return FamilySCHFlexOneNANDDownload, header, nil
	}
	if !errors.Is(flexErr, ErrNotSCHDownload) {
		return "", Header{}, flexErr
	}
	header, rawErr := inspectRawPiece(piece)
	if rawErr == nil {
		return FamilySCHRawDownload, header, nil
	}
	return "", Header{}, rawErr
}

func inspectPieceForFamily(piece firmwareset.Piece, family string) (Header, error) {
	switch family {
	case FamilySCHDownload:
		return inspectHeader(piece)
	case FamilySCHRawDownload:
		return inspectRawPiece(piece)
	case FamilySCHFlexOneNANDDownload:
		return inspectFlexOneNANDPiece(piece)
	default:
		return Header{}, fmt.Errorf("unsupported Samsung package family %q", family)
	}
}

var flexOneNANDBootMagic = [8]byte{0x26, 0x0b, 0x0d, 0x06, 0x34, 0x10, 0xd7, 0x73}

const flexOneNANDDataMagic = 0x00a1ccb9

func inspectFlexOneNANDPiece(piece firmwareset.Piece) (Header, error) {
	if piece.Size() <= 0 {
		return Header{}, rawPieceFormat(piece, "piece is empty")
	}
	var header [elf32HeaderSize]byte
	count := min(int64(len(header)), piece.Size())
	if _, err := piece.ReadAt(header[:count], 0); err != nil {
		return Header{}, err
	}
	if count >= int64(len(flexOneNANDBootMagic)) &&
		bytes.Equal(header[:len(flexOneNANDBootMagic)], flexOneNANDBootMagic[:]) {
		return rawHeader(piece, RoleWBT, "flex-onenand-boot"), nil
	}
	if count >= elf32HeaderSize && rawELF32ARMHeader(header[:]) {
		switch binary.LittleEndian.Uint32(header[24:28]) {
		case 0x00a00000:
			return rawHeader(piece, RoleWBIN, "raw-mbin"), nil
		case 0x10000000:
			return rawHeader(piece, RoleABIN, "raw-abin"), nil
		}
	}
	if count >= 4 && binary.LittleEndian.Uint32(header[:4]) == flexOneNANDDataMagic {
		return rawHeader(piece, RoleDAT, "flex-onenand-dat"), nil
	}
	// CF11 is the exact supported W850 build. Its FONT image retains the
	// older Samsung directory signature, which would otherwise be ambiguous
	// with the four-piece raw-download family when pieces are orderless.
	if count >= 12 && binary.LittleEndian.Uint32(header[:4]) == 1 &&
		bytes.Equal(header[4:12], []byte("CF11brew")) {
		return rawHeader(piece, RoleFont, "CF11"), nil
	}
	return Header{}, rawPieceFormat(piece, "content does not identify a Flex-OneNAND role")
}

func inspectRawPiece(piece firmwareset.Piece) (Header, error) {
	if piece.Size() <= 0 {
		return Header{}, rawPieceFormat(piece, "piece is empty")
	}

	var header [elf32HeaderSize]byte
	if piece.Size() >= int64(len(header)) {
		if _, err := piece.ReadAt(header[:], 0); err != nil {
			return Header{}, err
		}
		if rawELF32ARMHeader(header[:]) {
			return rawHeader(piece, RoleWBIN, "raw-elf"), nil
		}
	}

	var signature [12]byte
	if piece.Size() >= int64(len(signature)) {
		if _, err := piece.ReadAt(signature[:], 0); err != nil {
			return Header{}, err
		}
		switch {
		case binary.LittleEndian.Uint32(signature[0:4]) == 0x00003167:
			return rawHeader(piece, RoleDAT, "raw-dat"), nil
		case binary.LittleEndian.Uint32(signature[0:4]) == 1 &&
			bytes.EqualFold(signature[8:12], []byte("brew")) && printableASCII(signature[4:12]):
			return rawHeader(piece, RoleFont, string(signature[4:8])), nil
		}
	}
	if _, err := parseMIBIBCopies(piece); err == nil {
		return rawHeader(piece, RoleWBT, rawBuildTail(piece)), nil
	} else if !errors.Is(err, ErrMIBIBNotFound) {
		return Header{}, err
	}
	return Header{}, rawPieceFormat(piece, "content does not identify a legacy raw role")
}

func rawHeader(piece firmwareset.Piece, role Role, build string) Header {
	return Header{Role: role, Build: build, PayloadSize: uint64(piece.Size())}
}

func rawBuildTail(piece firmwareset.Piece) string {
	if piece.Size() < 4 {
		return "raw-wbt"
	}
	var tail [4]byte
	if _, err := piece.ReadAt(tail[:], piece.Size()-int64(len(tail))); err == nil && printableASCII(tail[:]) {
		return string(tail[:])
	}
	return "raw-wbt"
}

func rawELF32ARMHeader(header []byte) bool {
	return len(header) >= elf32HeaderSize && bytes.Equal(header[:4], []byte{0x7f, 'E', 'L', 'F'}) &&
		header[4] == 1 && header[5] == 1 && header[6] == 1 &&
		binary.LittleEndian.Uint16(header[16:18]) == elfTypeExecutable &&
		binary.LittleEndian.Uint16(header[18:20]) == elfMachineARM &&
		binary.LittleEndian.Uint32(header[20:24]) == 1
}

func printableASCII(data []byte) bool {
	for _, value := range data {
		if value < 0x21 || value > 0x7e {
			return false
		}
	}
	return true
}

func rawPieceFormat(piece firmwareset.Piece, reason string) error {
	return &FormatError{Piece: piece.Index(), Offset: 0, Reason: reason, Err: ErrNotSCHDownload}
}

func inspectHeader(piece firmwareset.Piece) (Header, error) {
	if piece.Size() < WrapperSize {
		return Header{}, &FormatError{
			Piece: piece.Index(), Offset: piece.Size(),
			Reason: fmt.Sprintf("wrapper is shorter than 0x%x bytes", WrapperSize), Err: ErrNotSCHDownload,
		}
	}
	var header [20]byte
	if _, err := piece.ReadAt(header[:], 0); err != nil {
		return Header{}, err
	}
	if magic := binary.LittleEndian.Uint32(header[0:4]); magic != WrapperMagic {
		return Header{}, &FormatError{
			Piece: piece.Index(), Offset: 0,
			Reason: fmt.Sprintf("magic 0x%08x", magic), Err: ErrNotSCHDownload,
		}
	}
	token := binary.LittleEndian.Uint32(header[4:8])
	role, ok := tokenRoles[token]
	if !ok {
		return Header{}, &FormatError{
			Piece: piece.Index(), Offset: 4,
			Reason: fmt.Sprintf("unknown role token 0x%08x", token), Err: ErrNotSCHDownload,
		}
	}
	buildBytes := bytes.TrimRight(header[12:20], "\x00 ")
	if len(buildBytes) == 0 {
		return Header{}, &FormatError{Role: role, Piece: piece.Index(), Offset: 12, Reason: "empty build identifier"}
	}
	for index, value := range buildBytes {
		if value < 0x21 || value > 0x7e {
			return Header{}, &FormatError{
				Role: role, Piece: piece.Index(), Offset: int64(12 + index),
				Reason: "non-ASCII build identifier",
			}
		}
	}
	return Header{
		Role:        role,
		Token:       token,
		Build:       string(buildBytes),
		PayloadSize: uint64(piece.Size() - WrapperSize),
	}, nil
}

func (p Package) MissingRoles() []Role {
	required := requiredRolesForFamily(p.Family)
	missing := make([]Role, 0, len(required))
	for _, role := range required {
		if _, ok := p.Pieces[role]; !ok {
			missing = append(missing, role)
		}
	}
	return missing
}

func requiredRolesForFamily(family string) []Role {
	if family == FamilySCHFlexOneNANDDownload {
		return flexOneNANDRequiredRoles
	}
	return requiredRoles
}

func (p Package) Complete() bool {
	return len(p.MissingRoles()) == 0
}

type Transform string

const (
	TransformIdentity        Transform = "identity"
	TransformBootBlocks      Transform = "samsung-boot-blocks"
	TransformSEEDFeedback    Transform = "seed-feedback"
	TransformFlexOneNANDBoot Transform = "samsung-flex-onenand-boot"
)

type DownloadRegion struct {
	Role         Role
	Start        uint64
	Size         uint64
	SourceOffset uint64
	Transform    Transform
}

type Partition struct {
	Name       string
	StartBlock uint32
	BlockCount uint32
	Attributes uint32
	Start      uint64
	Size       uint64
}

func (p Partition) End() uint64 {
	return p.Start + p.Size
}

type Layout struct {
	Family          string
	MIBIBVersion    uint32
	MIBIBGeneration uint32
	PackagedEnd     uint64
	Partitions      []Partition
	Regions         []DownloadRegion
}

func (l Layout) Region(role Role) *DownloadRegion {
	for index := range l.Regions {
		if l.Regions[index].Role == role {
			return &l.Regions[index]
		}
	}
	return nil
}

func Normalize(set firmwareset.Set, pkg Package) (Layout, error) {
	if pkg.Family == FamilySCHFlexOneNANDDownload {
		return NormalizeFlexOneNAND(set, pkg)
	}
	if pkg.Family != FamilySCHDownload && pkg.Family != FamilySCHRawDownload {
		return Layout{}, fmt.Errorf("unsupported Samsung package family %q", pkg.Family)
	}
	if missing := pkg.MissingRoles(); len(missing) != 0 {
		return Layout{}, fmt.Errorf("%w: missing %s", ErrIncompleteSet, joinRoles(missing))
	}
	pieces := make(map[Role]firmwareset.Piece, len(requiredRoles))
	for _, role := range requiredRoles {
		metadata := pkg.Pieces[role]
		piece, err := set.Piece(metadata.Index)
		if err != nil {
			return Layout{}, err
		}
		if piece.SHA256() != metadata.SHA256 {
			return Layout{}, fmt.Errorf("Samsung %s metadata does not match firmware set", role)
		}
		header, err := inspectPieceForFamily(piece, pkg.Family)
		if err != nil {
			return Layout{}, err
		}
		if header != metadata.Header {
			return Layout{}, fmt.Errorf("Samsung %s header metadata does not match firmware set", role)
		}
		pieces[role] = piece
	}

	copies, err := parseMIBIBCopies(pieces[RoleWBT])
	if err != nil {
		return Layout{}, err
	}
	selected := copies[0]
	for _, candidate := range copies[1:] {
		if candidate.Generation > selected.Generation {
			selected = candidate
		}
	}
	sourceOffset := uint64(WrapperSize)
	wbinTransform := TransformSEEDFeedback
	if pkg.Family == FamilySCHRawDownload {
		sourceOffset = 0
		wbinTransform = TransformIdentity
	}
	footer, footerOffset, err := readWBINFooterAt(pieces[RoleWBIN], sourceOffset)
	if err != nil {
		return Layout{}, err
	}
	starts := [4]uint64{
		uint64(footer[0]) << 16,
		uint64(footer[1]) << 16,
		uint64(footer[2]) << 16,
		uint64(footer[3]) << 16,
	}
	partitions := append([]Partition(nil), selected.Partitions...)
	if err := resolveOpenEndedPartitions(
		partitions,
		selected.Version,
		starts[3],
		pieces[RoleWBT].Index(),
		pieces[RoleWBIN].Index(),
		footerOffset,
	); err != nil {
		return Layout{}, err
	}
	byName := make(map[string]Partition, len(partitions))
	var flashEnd uint64
	for _, partition := range partitions {
		byName[partition.Name] = partition
		flashEnd = max(flashEnd, partition.End())
	}
	rsrc, ok := byName["0:RSRC"]
	if !ok {
		return Layout{}, &FormatError{Role: RoleWBT, Piece: pieces[RoleWBT].Index(), Reason: "MIBIB has no 0:RSRC partition"}
	}
	font, ok := byName["0:FONT"]
	if !ok {
		return Layout{}, &FormatError{Role: RoleWBT, Piece: pieces[RoleWBT].Index(), Reason: "MIBIB has no 0:FONT partition"}
	}
	wbtSize := pkg.Pieces[RoleWBT].Header.PayloadSize
	if starts[0] != wbtSize {
		return Layout{}, &FormatError{
			Role: RoleWBIN, Piece: pieces[RoleWBIN].Index(), Offset: footerOffset,
			Reason: fmt.Sprintf("WBIN start 0x%x does not follow WBT payload end 0x%x", starts[0], wbtSize),
		}
	}
	packagedEnd := starts[3]
	packagedEndIsPartitionBoundary := packagedEnd == flashEnd
	for _, partition := range partitions {
		if partition.Start == packagedEnd {
			packagedEndIsPartitionBoundary = true
			break
		}
	}
	if starts[1] != font.Start || starts[2] != rsrc.Start ||
		packagedEnd == 0 || packagedEnd > flashEnd || !packagedEndIsPartitionBoundary {
		return Layout{}, &FormatError{
			Role: RoleWBIN, Piece: pieces[RoleWBIN].Index(), Offset: footerOffset,
			Reason: "footer layout does not match MIBIB partitions",
		}
	}

	regions := []DownloadRegion{
		{Role: RoleWBT, Start: 0, Size: pkg.Pieces[RoleWBT].Header.PayloadSize, SourceOffset: sourceOffset, Transform: TransformBootBlocks},
		{Role: RoleWBIN, Start: starts[0], Size: pkg.Pieces[RoleWBIN].Header.PayloadSize, SourceOffset: sourceOffset, Transform: wbinTransform},
		{Role: RoleDAT, Start: rsrc.Start, Size: pkg.Pieces[RoleDAT].Header.PayloadSize, SourceOffset: sourceOffset, Transform: TransformIdentity},
		{Role: RoleFont, Start: font.Start, Size: pkg.Pieces[RoleFont].Header.PayloadSize, SourceOffset: sourceOffset, Transform: TransformIdentity},
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].Start < regions[j].Start })
	for index, region := range regions {
		if region.Size == 0 || region.Start > ^uint64(0)-region.Size ||
			region.Start+region.Size > packagedEnd {
			return Layout{}, fmt.Errorf("Samsung %s region has invalid geometry", region.Role)
		}
		if index > 0 && regions[index-1].Start+regions[index-1].Size > region.Start {
			return Layout{}, fmt.Errorf("Samsung %s region overlaps %s", region.Role, regions[index-1].Role)
		}
	}
	return Layout{
		Family:          pkg.Family,
		MIBIBVersion:    selected.Version,
		MIBIBGeneration: selected.Generation,
		PackagedEnd:     packagedEnd,
		Partitions:      partitions,
		Regions:         regions,
	}, nil
}

func resolveOpenEndedPartitions(
	partitions []Partition,
	version uint32,
	packagedEnd uint64,
	wbtPiece int,
	wbinPiece int,
	footerOffset int64,
) error {
	for index := range partitions {
		partition := &partitions[index]
		if partition.BlockCount != 0 {
			continue
		}
		if version != 1 || index != len(partitions)-1 || partition.Name != "0:EFS2" {
			return &FormatError{
				Role: RoleWBT, Piece: wbtPiece,
				Reason: fmt.Sprintf("partition %q has unsupported open-ended geometry", partition.Name),
			}
		}
		if packagedEnd <= partition.Start || (packagedEnd-partition.Start)%EraseBlockSize != 0 {
			return &FormatError{
				Role: RoleWBIN, Piece: wbinPiece, Offset: footerOffset,
				Reason: fmt.Sprintf("package end does not resolve partition %q", partition.Name),
			}
		}
		blocks := (packagedEnd - partition.Start) / EraseBlockSize
		if blocks > uint64(^uint32(0)) {
			return &FormatError{
				Role: RoleWBIN, Piece: wbinPiece, Offset: footerOffset,
				Reason: fmt.Sprintf("resolved partition %q is too large", partition.Name),
			}
		}
		partition.BlockCount = uint32(blocks)
		partition.Size = blocks * EraseBlockSize
	}
	return nil
}

func readWBINFooter(piece firmwareset.Piece) ([4]uint32, int64, error) {
	return readWBINFooterAt(piece, WrapperSize)
}

func readWBINFooterAt(piece firmwareset.Piece, sourceOffset uint64) ([4]uint32, int64, error) {
	var values [4]uint32
	if sourceOffset > uint64(piece.Size()) || uint64(piece.Size())-sourceOffset < 64 {
		return values, piece.Size(), &FormatError{
			Role: RoleWBIN, Piece: piece.Index(), Offset: piece.Size(), Reason: "payload is too short for layout footer",
		}
	}
	offset := piece.Size() - 62
	var encoded [16]byte
	if _, err := piece.ReadAt(encoded[:], offset); err != nil {
		return values, offset, err
	}
	for index := range values {
		values[index] = binary.LittleEndian.Uint32(encoded[index*4:])
	}
	return values, offset, nil
}

type mibibCopy struct {
	Offset     int64
	Version    uint32
	Generation uint32
	Partitions []Partition
}

var mibibHeaderMagic = [3]uint32{0xfe569fac, 0xcd7f127a, 3}
var primaryTableMagic = [3]uint32{0x55ee73aa, 0xe35ebddb, 3}

func parseMIBIBCopies(piece firmwareset.Piece) ([]mibibCopy, error) {
	var copies []mibibCopy
	for offset := int64(WrapperSize); offset+16 <= piece.Size(); offset += EraseBlockSize {
		var header [16]byte
		if _, err := piece.ReadAt(header[:], offset); err != nil {
			return nil, err
		}
		if binary.LittleEndian.Uint32(header[0:4]) != mibibHeaderMagic[0] ||
			binary.LittleEndian.Uint32(header[4:8]) != mibibHeaderMagic[1] {
			continue
		}
		version := binary.LittleEndian.Uint32(header[8:12])
		if version != 1 && version != mibibHeaderMagic[2] {
			return nil, &FormatError{
				Role: RoleWBT, Piece: piece.Index(), Offset: offset + 8,
				Reason: fmt.Sprintf("unsupported MIBIB version %d", version),
			}
		}
		if offset+EraseBlockSize > piece.Size() {
			return nil, &FormatError{Role: RoleWBT, Piece: piece.Index(), Offset: offset, Reason: "truncated MIBIB erase block"}
		}
		data := make([]byte, EraseBlockSize)
		if _, err := piece.ReadAt(data, offset); err != nil {
			return nil, err
		}
		copy, err := parseMIBIBCopy(piece.Index(), offset, data)
		if err != nil {
			return nil, err
		}
		copies = append(copies, copy)
	}
	if len(copies) == 0 {
		return nil, &FormatError{
			Role: RoleWBT, Piece: piece.Index(), Offset: WrapperSize,
			Reason: ErrMIBIBNotFound.Error(), Err: ErrMIBIBNotFound,
		}
	}
	return copies, nil
}

func parseMIBIBCopy(piece int, base int64, data []byte) (mibibCopy, error) {
	fail := func(relative int64, reason string) (mibibCopy, error) {
		return mibibCopy{}, &FormatError{Role: RoleWBT, Piece: piece, Offset: base + relative, Reason: reason}
	}
	if len(data) != EraseBlockSize {
		return fail(0, "invalid MIBIB erase-block size")
	}
	generation := binary.LittleEndian.Uint32(data[12:16])
	version := binary.LittleEndian.Uint32(data[8:12])
	primary := data[PageSize : 2*PageSize]
	for index, magic := range primaryTableMagic[:2] {
		if binary.LittleEndian.Uint32(primary[index*4:]) != magic {
			return fail(PageSize+int64(index*4), "primary partition-table magic mismatch")
		}
	}
	primaryVersion := binary.LittleEndian.Uint32(primary[8:12])
	if primaryVersion != version {
		return fail(PageSize+8, fmt.Sprintf("primary partition-table version %d does not match MIBIB version %d", primaryVersion, version))
	}
	count := binary.LittleEndian.Uint32(primary[12:16])
	maxEntries := uint32((PageSize - 16) / mibibEntrySize)
	if count == 0 || count > maxEntries {
		return fail(PageSize+12, fmt.Sprintf("invalid primary partition count %d", count))
	}
	partitions := make([]Partition, 0, count)
	seen := make(map[string]struct{}, count)
	for index := uint32(0); index < count; index++ {
		relative := 16 + int(index)*mibibEntrySize
		entry := primary[relative : relative+mibibEntrySize]
		nul := bytes.IndexByte(entry[:16], 0)
		if nul <= 0 {
			return fail(PageSize+int64(relative), "partition name is empty or unterminated")
		}
		nameBytes := entry[:nul]
		for byteIndex, value := range nameBytes {
			if value < 0x21 || value > 0x7e {
				return fail(PageSize+int64(relative+byteIndex), "partition name is not printable ASCII")
			}
		}
		name := string(nameBytes)
		if _, duplicate := seen[name]; duplicate {
			return fail(PageSize+int64(relative), fmt.Sprintf("duplicate partition %q", name))
		}
		seen[name] = struct{}{}
		startBlock := binary.LittleEndian.Uint32(entry[16:20])
		blockCount := binary.LittleEndian.Uint32(entry[20:24])
		attributes := binary.LittleEndian.Uint32(entry[24:28])
		if blockCount == 0 && (version != 1 || index != count-1 || name != "0:EFS2") {
			return fail(PageSize+int64(relative+20), fmt.Sprintf("partition %q is empty", name))
		}
		endBlock := uint64(startBlock) + uint64(blockCount)
		if endBlock > 1<<32 {
			return fail(PageSize+int64(relative+20), fmt.Sprintf("partition %q overflows block geometry", name))
		}
		start := uint64(startBlock) * EraseBlockSize
		size := uint64(blockCount) * EraseBlockSize
		partition := Partition{
			Name: name, StartBlock: startBlock, BlockCount: blockCount,
			Attributes: attributes, Start: start, Size: size,
		}
		for _, previous := range partitions {
			if partition.Start >= previous.End() || previous.Start >= partition.End() {
				continue
			}
			if !versionOneFOTAAlias(version, previous, partition) {
				return fail(PageSize+int64(relative+16), fmt.Sprintf("partition %q overlaps its predecessor", name))
			}
		}
		partitions = append(partitions, partition)
	}
	return mibibCopy{Offset: base, Version: version, Generation: generation, Partitions: partitions}, nil
}

func versionOneFOTAAlias(version uint32, left, right Partition) bool {
	if version != 1 {
		return false
	}
	var fota, container Partition
	switch {
	case left.Name == "0:FOTA" && (right.Name == "0:AMSS" || right.Name == "0:DMB"):
		fota, container = left, right
	case right.Name == "0:FOTA" && (left.Name == "0:AMSS" || left.Name == "0:DMB"):
		fota, container = right, left
	default:
		return false
	}
	return fota.Start >= container.Start && fota.End() <= container.End()
}

func joinRoles(roles []Role) string {
	values := make([]string, len(roles))
	for index, role := range roles {
		values[index] = string(role)
	}
	return strings.Join(values, ", ")
}

var _ io.ReaderAt = firmwareset.Piece{}
