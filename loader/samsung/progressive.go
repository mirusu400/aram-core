package samsung

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	"github.com/mirusu400/aram-core/firmwareset"
)

const (
	wbinRoundKeyOffset        = 0x00f0
	wbinRoundKeySize          = 0x0080
	wbinEncryptedLengthOffset = 0x0174
	wbinTerminalBlockOffset   = 0x0178
	elf32HeaderSize           = 0x34
	elf32ProgramHeaderSize    = 0x20
	elfMachineARM             = 40
	elfTypeExecutable         = 2
	elfProgramLoad            = 1
	MaxProgressiveImageBytes  = 256 << 20
	MaxProgramHeaders         = 128
)

var (
	ErrInvalidWBINTransform  = errors.New("invalid Samsung WBIN transform")
	ErrInvalidProgressiveELF = errors.New("invalid progressive ELF32 image")
)

// ProgressiveImage is the plaintext logical image consumed by Qualcomm's
// progressive boot loader. Program-header file ranges are relative to the
// AMSS partition origin and may continue into later firmware pieces.
type ProgressiveImage struct {
	Bytes           []byte
	SHA256          string
	EncryptedLength uint32
	ELF             ProgressiveELF
}

type ProgressiveELF struct {
	Entry          uint32
	Flags          uint32
	LogicalFileEnd uint64
	ProgramHeaders []ELF32ProgramHeader
}

type ELF32ProgramHeader struct {
	Type            uint32
	Offset          uint32
	VirtualAddress  uint32
	PhysicalAddress uint32
	FileSize        uint32
	MemorySize      uint32
	Flags           uint32
	Alignment       uint32
}

// DecodeWBIN verifies and decodes a Samsung WBIN payload without consulting a
// memory dump. Its SEED tables are the public RFC 4269 definitions; only the
// per-package round-key schedule stored in the signed wrapper is consumed.
func DecodeWBIN(set firmwareset.Set, pkg Package) (ProgressiveImage, error) {
	if pkg.Family != FamilySCHDownload {
		return ProgressiveImage{}, fmt.Errorf("unsupported Samsung package family %q", pkg.Family)
	}
	metadata, ok := pkg.Pieces[RoleWBIN]
	if !ok {
		return ProgressiveImage{}, fmt.Errorf("%w: missing %s", ErrIncompleteSet, RoleWBIN)
	}
	piece, err := set.Piece(metadata.Index)
	if err != nil {
		return ProgressiveImage{}, err
	}
	if piece.SHA256() != metadata.SHA256 {
		return ProgressiveImage{}, fmt.Errorf("Samsung %s metadata does not match firmware set", RoleWBIN)
	}
	header, err := inspectHeader(piece)
	if err != nil {
		return ProgressiveImage{}, err
	}
	if header != metadata.Header {
		return ProgressiveImage{}, fmt.Errorf("Samsung %s header metadata does not match firmware set", RoleWBIN)
	}
	if header.PayloadSize > MaxProgressiveImageBytes || header.PayloadSize > uint64(math.MaxInt) {
		return ProgressiveImage{}, wbinFormat(
			piece.Index(), WrapperSize, "payload exceeds progressive-image limit", ErrInvalidWBINTransform,
		)
	}

	var wrapper [wbinTerminalBlockOffset + 16]byte
	if _, err := piece.ReadAt(wrapper[:], 0); err != nil {
		return ProgressiveImage{}, err
	}
	var roundKeys [32]uint32
	for index := range roundKeys {
		offset := wbinRoundKeyOffset + index*4
		roundKeys[index] = binary.LittleEndian.Uint32(wrapper[offset : offset+4])
	}
	encryptedLength := binary.LittleEndian.Uint32(
		wrapper[wbinEncryptedLengthOffset : wbinEncryptedLengthOffset+4],
	)
	if encryptedLength == 0 || encryptedLength%16 != 0 || uint64(encryptedLength) > header.PayloadSize {
		return ProgressiveImage{}, wbinFormat(
			piece.Index(), wbinEncryptedLengthOffset,
			fmt.Sprintf("invalid encrypted length 0x%x", encryptedLength),
			ErrInvalidWBINTransform,
		)
	}

	decoded := make([]byte, int(header.PayloadSize))
	if _, err := piece.ReadAt(decoded, WrapperSize); err != nil {
		return ProgressiveImage{}, err
	}
	terminal := wrapper[wbinTerminalBlockOffset : wbinTerminalBlockOffset+16]
	if !bytes.Equal(decoded[encryptedLength-16:encryptedLength], terminal) {
		return ProgressiveImage{}, wbinFormat(
			piece.Index(), wbinTerminalBlockOffset,
			"terminal ciphertext does not match encrypted payload",
			ErrInvalidWBINTransform,
		)
	}
	for offset := uint32(0); offset < encryptedLength; offset += 16 {
		block := decoded[offset : offset+16]
		feedback := binary.LittleEndian.Uint32(block[0:4])
		decryptSEEDBlockLE(block, &roundKeys)
		roundKeys[0] += feedback
		roundKeys[16] += feedback
	}

	elf, err := inspectProgressiveELF(piece.Index(), decoded)
	if err != nil {
		return ProgressiveImage{}, err
	}
	digest := sha256.Sum256(decoded)
	return ProgressiveImage{
		Bytes:           decoded,
		SHA256:          hex.EncodeToString(digest[:]),
		EncryptedLength: encryptedLength,
		ELF:             elf,
	}, nil
}

func inspectProgressiveELF(piece int, data []byte) (ProgressiveELF, error) {
	fail := func(offset int64, reason string) (ProgressiveELF, error) {
		return ProgressiveELF{}, wbinFormat(piece, WrapperSize+offset, reason, ErrInvalidProgressiveELF)
	}
	if len(data) < elf32HeaderSize {
		return fail(int64(len(data)), "ELF32 header is truncated")
	}
	if !bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return fail(0, "ELF magic is missing")
	}
	if data[4] != 1 || data[5] != 1 || data[6] != 1 {
		return fail(4, "only little-endian ELF32 version 1 is supported")
	}
	if binary.LittleEndian.Uint16(data[16:18]) != elfTypeExecutable {
		return fail(16, "ELF is not ET_EXEC")
	}
	if binary.LittleEndian.Uint16(data[18:20]) != elfMachineARM {
		return fail(18, "ELF machine is not ARM")
	}
	if binary.LittleEndian.Uint32(data[20:24]) != 1 {
		return fail(20, "ELF version is not 1")
	}
	if binary.LittleEndian.Uint16(data[40:42]) != elf32HeaderSize {
		return fail(40, "unexpected ELF32 header size")
	}
	programOffset := binary.LittleEndian.Uint32(data[28:32])
	programSize := binary.LittleEndian.Uint16(data[42:44])
	programCount := binary.LittleEndian.Uint16(data[44:46])
	if programSize != elf32ProgramHeaderSize {
		return fail(42, "unexpected program-header size")
	}
	if programCount == 0 || programCount > MaxProgramHeaders {
		return fail(44, fmt.Sprintf("invalid program-header count %d", programCount))
	}
	tableEnd := uint64(programOffset) + uint64(programSize)*uint64(programCount)
	if tableEnd > uint64(len(data)) {
		return fail(int64(programOffset), "program-header table is truncated")
	}

	elf := ProgressiveELF{
		Entry:          binary.LittleEndian.Uint32(data[24:28]),
		Flags:          binary.LittleEndian.Uint32(data[36:40]),
		ProgramHeaders: make([]ELF32ProgramHeader, 0, programCount),
	}
	for index := uint16(0); index < programCount; index++ {
		offset := uint64(programOffset) + uint64(index)*uint64(programSize)
		raw := data[offset : offset+elf32ProgramHeaderSize]
		header := ELF32ProgramHeader{
			Type:            binary.LittleEndian.Uint32(raw[0:4]),
			Offset:          binary.LittleEndian.Uint32(raw[4:8]),
			VirtualAddress:  binary.LittleEndian.Uint32(raw[8:12]),
			PhysicalAddress: binary.LittleEndian.Uint32(raw[12:16]),
			FileSize:        binary.LittleEndian.Uint32(raw[16:20]),
			MemorySize:      binary.LittleEndian.Uint32(raw[20:24]),
			Flags:           binary.LittleEndian.Uint32(raw[24:28]),
			Alignment:       binary.LittleEndian.Uint32(raw[28:32]),
		}
		if header.Type == elfProgramLoad && header.FileSize > header.MemorySize {
			return fail(int64(offset+20), fmt.Sprintf("load segment %d has file size larger than memory size", index))
		}
		if header.Alignment != 0 && header.Alignment&(header.Alignment-1) != 0 {
			return fail(int64(offset+28), fmt.Sprintf("segment %d alignment is not a power of two", index))
		}
		fileEnd := uint64(header.Offset) + uint64(header.FileSize)
		virtualEnd := uint64(header.VirtualAddress) + uint64(header.MemorySize)
		physicalEnd := uint64(header.PhysicalAddress) + uint64(header.MemorySize)
		if fileEnd > 1<<32 || virtualEnd > 1<<32 || physicalEnd > 1<<32 {
			return fail(int64(offset), fmt.Sprintf("segment %d range wraps", index))
		}
		elf.LogicalFileEnd = max(elf.LogicalFileEnd, fileEnd)
		elf.ProgramHeaders = append(elf.ProgramHeaders, header)
	}
	return elf, nil
}

func wbinFormat(piece int, offset int64, reason string, err error) error {
	return &FormatError{Role: RoleWBIN, Piece: piece, Offset: offset, Reason: reason, Err: err}
}
