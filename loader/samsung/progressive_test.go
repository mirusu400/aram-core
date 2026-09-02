package samsung

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/firmwareset"
)

var rfc4269B1RoundKeys = [32]uint32{
	0x7c8f8c7e, 0xc737a22c, 0xff276cdb, 0xa7ca684a,
	0x2f9d01a1, 0x70049e41, 0xae59b3c4, 0x4245e90c,
	0xa1d6400f, 0xdbc1394e, 0x85963508, 0x0c5f1fcb,
	0xb684bda7, 0x61a4aeae, 0xd17e0741, 0xfee90aa1,
	0x76cc05d5, 0xe97a7394, 0x50ac6f92, 0x1b2666e5,
	0x65b7904a, 0x8ec3a7b3, 0x2f7e2e22, 0xa2b121b9,
	0x4d0bfde4, 0x4e888d9b, 0x631c8ddc, 0x4378a6c4,
	0x216af65f, 0x7878c031, 0x71891150, 0x98b255b0,
}

func TestDecryptSEEDBlockMatchesRFC4269B1(t *testing.T) {
	// decryptSEEDBlockLE models the OEM's little-endian loads. Word-swap the
	// RFC byte strings so their numeric 32-bit values remain unchanged.
	block := []byte{
		0xe0, 0xc6, 0xba, 0x5e, 0x68, 0x16, 0x4e, 0x05,
		0xcc, 0xf1, 0xaf, 0x19, 0xdb, 0x6c, 0x34, 0x6d,
	}
	want := []byte{
		0x03, 0x02, 0x01, 0x00, 0x07, 0x06, 0x05, 0x04,
		0x0b, 0x0a, 0x09, 0x08, 0x0f, 0x0e, 0x0d, 0x0c,
	}
	decryptSEEDBlockLE(block, &rfc4269B1RoundKeys)
	if !bytes.Equal(block, want) {
		t.Fatalf("SEED plaintext = %x, want %x", block, want)
	}
}

func TestDecodeWBINSyntheticProgressiveELF(t *testing.T) {
	pieceBytes, plaintext := syntheticEncodedWBIN(t)
	set, err := firmwareset.NewSet([]firmwareset.Source{{
		ReaderAt: bytes.NewReader(pieceBytes), Size: int64(len(pieceBytes)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	image, err := DecodeWBIN(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if image.EncryptedLength != 0x80 || !bytes.Equal(image.Bytes, plaintext) {
		t.Fatalf("decoded WBIN = length %#x bytesEqual %v", image.EncryptedLength, bytes.Equal(image.Bytes, plaintext))
	}
	if len(image.ELF.ProgramHeaders) != 1 || image.ELF.LogicalFileEnd != 0x90 {
		t.Fatalf("progressive ELF = %+v", image.ELF)
	}
	header := image.ELF.ProgramHeaders[0]
	if header.Type != elfProgramLoad || header.VirtualAddress != 0x00100000 ||
		header.FileSize != 0x10 || header.MemorySize != 0x20 {
		t.Fatalf("program header = %+v", header)
	}
}

func TestDecodeWBINAcceptsRawProgressiveELF(t *testing.T) {
	sources := syntheticRawDownloadSources(t)
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
	image, err := DecodeWBIN(set, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if image.EncryptedLength != 0 || len(image.Bytes) != 0x100 ||
		len(image.ELF.ProgramHeaders) != 1 || image.ELF.LogicalFileEnd != 0x90 {
		t.Fatalf("raw progressive image = %+v", image)
	}
}

func TestDecodeWBINAllowsOnlyExactProfiledOpaqueRawImage(t *testing.T) {
	sources := syntheticSmallPageRawDownloadSources(t)
	opaque := bytes.Repeat([]byte{0xa5, 0x5a, 0x3c, 0xc3}, 0x40)
	sources[RoleWBIN] = firmwareset.Source{ReaderAt: bytes.NewReader(opaque), Size: int64(len(opaque))}
	roles := []Role{RoleWBT, RoleWBIN, RoleDAT, RoleFont}
	setSources := make([]firmwareset.Source, len(roles))
	for index, role := range roles {
		setSources[index] = sources[role]
	}
	set, err := firmwareset.NewSet(setSources)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(set); !errors.Is(err, ErrNotSCHDownload) {
		t.Fatalf("Inspect unknown opaque WBIN error = %v", err)
	}

	hashes := make(map[Role]string, len(roles))
	for index, role := range roles {
		piece, pieceErr := set.Piece(index)
		if pieceErr != nil {
			t.Fatal(pieceErr)
		}
		hashes[role] = piece.SHA256()
	}
	registry, err := NewRegistry(BuildProfile{
		ID: "samsung.synthetic.opaque", Family: FamilySCHRawDownload,
		Manufacturer: "Samsung", Model: "Synthetic", Build: "OPAQUE",
		WBINFormat: WBINFormatOpaque, PieceHashes: hashes,
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := inspectWithRegistry(set, registry)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Pieces[RoleWBIN].Header.Build != string(WBINFormatOpaque) {
		t.Fatalf("opaque WBIN header = %+v", pkg.Pieces[RoleWBIN].Header)
	}
	layout, err := normalizeWithRegistry(set, pkg, registry)
	if err != nil {
		t.Fatal(err)
	}
	if layout.PageSize != smallPageSize || layout.EraseBlockSize != smallEraseBlockSize {
		t.Fatalf("opaque WBIN layout geometry = %#x/%#x", layout.PageSize, layout.EraseBlockSize)
	}
	image, err := decodeWBINWithRegistry(set, pkg, registry)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(opaque)
	if !bytes.Equal(image.Bytes, opaque) || image.SHA256 != hex.EncodeToString(digest[:]) ||
		image.EncryptedLength != 0 || image.ELF.Entry != 0 || image.ELF.LogicalFileEnd != 0 ||
		len(image.ELF.ProgramHeaders) != 0 {
		t.Fatalf("opaque progressive image = %+v", image)
	}

	if _, err := decodeWBINWithRegistry(set, pkg, Registry{}); !errors.Is(err, ErrNotSCHDownload) {
		t.Fatalf("DecodeWBIN unknown opaque profile error = %v", err)
	}
}

func TestDecodeWBINRejectsTerminalCiphertextMismatch(t *testing.T) {
	pieceBytes, _ := syntheticEncodedWBIN(t)
	pieceBytes[wbinTerminalBlockOffset] ^= 0x80
	set, err := firmwareset.NewSet([]firmwareset.Source{{
		ReaderAt: bytes.NewReader(pieceBytes), Size: int64(len(pieceBytes)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Inspect(set)
	if err != nil {
		t.Fatal(err)
	}
	_, err = DecodeWBIN(set, pkg)
	if !errors.Is(err, ErrInvalidWBINTransform) {
		t.Fatalf("DecodeWBIN error = %v", err)
	}
}

func syntheticEncodedWBIN(t *testing.T) ([]byte, []byte) {
	t.Helper()
	const payloadSize = 0x100
	piece := syntheticWrappedPiece(RoleWBIN, "DL211024", payloadSize)
	plaintext := make([]byte, payloadSize)
	copy(plaintext[:16], []byte{0x7f, 'E', 'L', 'F', 1, 1, 1})
	binary.LittleEndian.PutUint16(plaintext[16:18], elfTypeExecutable)
	binary.LittleEndian.PutUint16(plaintext[18:20], elfMachineARM)
	binary.LittleEndian.PutUint32(plaintext[20:24], 1)
	binary.LittleEndian.PutUint32(plaintext[28:32], 0x40)
	binary.LittleEndian.PutUint16(plaintext[40:42], elf32HeaderSize)
	binary.LittleEndian.PutUint16(plaintext[42:44], elf32ProgramHeaderSize)
	binary.LittleEndian.PutUint16(plaintext[44:46], 1)
	phdr := plaintext[0x40:0x60]
	binary.LittleEndian.PutUint32(phdr[0:4], elfProgramLoad)
	binary.LittleEndian.PutUint32(phdr[4:8], 0x80)
	binary.LittleEndian.PutUint32(phdr[8:12], 0x00100000)
	binary.LittleEndian.PutUint32(phdr[12:16], 0x00100000)
	binary.LittleEndian.PutUint32(phdr[16:20], 0x10)
	binary.LittleEndian.PutUint32(phdr[20:24], 0x20)
	binary.LittleEndian.PutUint32(phdr[24:28], 5)
	binary.LittleEndian.PutUint32(phdr[28:32], 0x10)
	copy(plaintext[0x80:0x90], "progressive-data")
	footer := len(plaintext) - 62
	putU32s(plaintext, footer, 0x6, 0xc, 0x8, 0xe)

	for index, value := range rfc4269B1RoundKeys {
		binary.LittleEndian.PutUint32(piece[wbinRoundKeyOffset+index*4:], value)
	}
	binary.LittleEndian.PutUint32(piece[wbinEncryptedLengthOffset:], 0x80)
	ciphertext := append([]byte(nil), plaintext...)
	roundKeys := rfc4269B1RoundKeys
	for offset := 0; offset < 0x80; offset += 16 {
		encryptSEEDBlockLE(ciphertext[offset:offset+16], &roundKeys)
		feedback := binary.LittleEndian.Uint32(ciphertext[offset : offset+4])
		roundKeys[0] += feedback
		roundKeys[16] += feedback
	}
	copy(piece[WrapperSize:], ciphertext)
	copy(piece[wbinTerminalBlockOffset:], ciphertext[0x70:0x80])
	return piece, plaintext
}

func encryptSEEDBlockLE(block []byte, roundKeys *[32]uint32) {
	left0 := binary.LittleEndian.Uint32(block[0:4])
	left1 := binary.LittleEndian.Uint32(block[4:8])
	right0 := binary.LittleEndian.Uint32(block[8:12])
	right1 := binary.LittleEndian.Uint32(block[12:16])
	for roundIndex := 0; roundIndex < 16; roundIndex++ {
		keyIndex := roundIndex * 2
		if roundIndex&1 == 0 {
			left0, left1 = seedRound(
				left0, left1, right0, right1,
				roundKeys[keyIndex], roundKeys[keyIndex+1],
			)
		} else {
			right0, right1 = seedRound(
				right0, right1, left0, left1,
				roundKeys[keyIndex], roundKeys[keyIndex+1],
			)
		}
	}
	binary.LittleEndian.PutUint32(block[0:4], right0)
	binary.LittleEndian.PutUint32(block[4:8], right1)
	binary.LittleEndian.PutUint32(block[8:12], left0)
	binary.LittleEndian.PutUint32(block[12:16], left1)
}
