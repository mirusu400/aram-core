package samsung

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/firmwareset"
)

func TestSamsungRawDownloadPrivateReferences(t *testing.T) {
	configured := os.Getenv("ARAM_SAMSUNG_RAW_REFERENCE_DIRS")
	if configured == "" {
		t.Skip("ARAM_SAMSUNG_RAW_REFERENCE_DIRS is not configured")
	}
	for index, directory := range filepath.SplitList(configured) {
		directory := directory
		if strings.TrimSpace(directory) == "" {
			continue
		}
		t.Run(fmt.Sprintf("reference-%d", index), func(t *testing.T) {
			set := openRawReferenceSet(t, directory)
			pkg, err := Inspect(set)
			check(t, err)
			if pkg.Family != FamilySCHRawDownload || !pkg.Complete() {
				t.Fatalf("raw package = family %q missing %v", pkg.Family, pkg.MissingRoles())
			}
			profile, err := BuiltinRegistry().Match(pkg)
			check(t, err)
			for _, id := range []string{"qcsbl", "oemsbl"} {
				spec, ok := profile.BootImage(id)
				if !ok {
					t.Fatalf("profile %q has no %s image", profile.ID, id)
				}
				if _, err := ReconstructBootImage(set, pkg, spec); err != nil {
					t.Fatal(err)
				}
			}
			for _, pblID := range []string{"pbl-rom", "pbl-source"} {
				pblSpec, ok := profile.MemoryImage(pblID)
				if ok {
					if _, err := ReconstructMemoryImage(set, pkg, pblSpec); err != nil {
						t.Fatal(err)
					}
				}
			}
			layout, err := Normalize(set, pkg)
			check(t, err)
			progressive, err := DecodeWBIN(set, pkg)
			check(t, err)
			if directory := os.Getenv("ARAM_SAMSUNG_RAW_PROGRESSIVE_DUMP_DIR"); directory != "" {
				check(t, os.MkdirAll(directory, 0o755))
				check(t, os.WriteFile(filepath.Join(directory, profile.ID+".bin"), progressive.Bytes, 0o600))
			}
			if os.Getenv("ARAM_SAMSUNG_RAW_DUMP_HEADERS") != "" {
				for index, header := range progressive.ELF.ProgramHeaders {
					t.Logf("progressive header %d: %+v", index, header)
				}
			}
			if needle := os.Getenv("ARAM_SAMSUNG_RAW_FIND_STRING"); needle != "" {
				logProgressiveStringReferences(t, progressive, needle)
			}
			if text := os.Getenv("ARAM_SAMSUNG_RAW_PEEK_ADDRESS"); text != "" {
				address, parseErr := strconv.ParseUint(text, 0, 32)
				if parseErr != nil {
					t.Fatalf("ARAM_SAMSUNG_RAW_PEEK_ADDRESS is invalid: %q", text)
				}
				logProgressiveAddress(t, progressive, uint32(address))
				var encoded [4]byte
				binary.LittleEndian.PutUint32(encoded[:], uint32(address))
				for from := 0; from < len(progressive.Bytes); {
					offset := bytes.Index(progressive.Bytes[from:], encoded[:])
					if offset < 0 {
						break
					}
					offset += from
					if literalAddress, loaded := progressiveFileOffsetAddress(progressive.ELF.ProgramHeaders, uint32(offset)); loaded {
						t.Logf("progressive address %#08x literal at %#08x", address, literalAddress)
						logProgressiveLiteralReferences(t, progressive, literalAddress)
					}
					from = offset + len(encoded)
				}
			}
			if text := os.Getenv("ARAM_SAMSUNG_RAW_PEEK_PROGRESSIVE_OFFSET"); text != "" {
				offset, parseErr := strconv.ParseUint(text, 0, 32)
				if parseErr != nil {
					t.Fatalf("ARAM_SAMSUNG_RAW_PEEK_PROGRESSIVE_OFFSET is invalid: %q", text)
				}
				address, loaded := progressiveFileOffsetAddress(progressive.ELF.ProgramHeaders, uint32(offset))
				end := min(uint32(len(progressive.Bytes)), uint32(offset)+32)
				if uint32(offset) >= uint32(len(progressive.Bytes)) {
					t.Logf("progressive file offset %#x is past the decoded image", offset)
				} else {
					t.Logf("progressive file offset %#x address=%#08x loaded=%t bytes=%x", offset, address, loaded, progressive.Bytes[uint32(offset):end])
				}
			}
			flash, err := AssembleFlash(set, pkg)
			check(t, err)
			t.Logf(
				"raw download %s: MIBIB version=%d generation=%d partitions=%d packaged-end=%#x flash=%#x logical-end=%#x program-headers=%d",
				profile.ID,
				layout.MIBIBVersion,
				layout.MIBIBGeneration,
				len(layout.Partitions),
				layout.PackagedEnd,
				flash.Size(),
				progressive.ELF.LogicalFileEnd,
				len(progressive.ELF.ProgramHeaders),
			)
			for _, partition := range flash.Partitions() {
				t.Logf("partition %s: %#x..%#x", partition.Name, partition.Start, partition.End())
			}
			for _, region := range flash.Regions() {
				t.Logf("region %s: %#x..%#x source=%#x transform=%s", region.Role, region.Start, region.End(), region.SourceOffset, region.Transform)
			}
		})
	}
}

func logProgressiveAddress(t *testing.T, image ProgressiveImage, address uint32) {
	t.Helper()
	for _, header := range image.ELF.ProgramHeaders {
		if header.Type != elfProgramLoad || address < header.VirtualAddress ||
			address-header.VirtualAddress >= header.FileSize {
			continue
		}
		offset := header.Offset + address - header.VirtualAddress
		end := min(offset+32, uint32(len(image.Bytes)))
		t.Logf("progressive address %#08x header=%+v file-offset=%#x bytes %x", address, header, offset, image.Bytes[offset:end])
		return
	}
	t.Logf("progressive address %#08x is not file-backed", address)
}

func logProgressiveStringReferences(t *testing.T, image ProgressiveImage, needle string) {
	t.Helper()
	encodings := []struct {
		name  string
		bytes []byte
	}{
		{name: "ASCII", bytes: []byte(needle)},
		{name: "UTF-16LE", bytes: encodeUTF16(needle, binary.LittleEndian)},
		{name: "UTF-16BE", bytes: encodeUTF16(needle, binary.BigEndian)},
	}
	for _, encoding := range encodings {
		logProgressiveEncodedStringReferences(t, image, needle, encoding.name, encoding.bytes)
	}
}

func logProgressiveEncodedStringReferences(t *testing.T, image ProgressiveImage, needle, encoding string, encodedNeedle []byte) {
	t.Helper()
	for searchFrom := 0; searchFrom < len(image.Bytes); {
		offset := bytes.Index(image.Bytes[searchFrom:], encodedNeedle)
		if offset < 0 {
			break
		}
		offset += searchFrom
		address, ok := progressiveFileOffsetAddress(image.ELF.ProgramHeaders, uint32(offset))
		if !ok {
			t.Logf("progressive %s string %q at file offset %#x has no load address", encoding, needle, offset)
			searchFrom = offset + len(encodedNeedle)
			continue
		}
		t.Logf("progressive %s string %q at file offset %#x address %#08x", encoding, needle, offset, address)
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], address)
		for literalFrom := 0; literalFrom < len(image.Bytes); {
			literalOffset := bytes.Index(image.Bytes[literalFrom:], encoded[:])
			if literalOffset < 0 {
				break
			}
			literalOffset += literalFrom
			literalAddress, loaded := progressiveFileOffsetAddress(
				image.ELF.ProgramHeaders,
				uint32(literalOffset),
			)
			if loaded {
				logProgressiveLiteralReferences(t, image, literalAddress)
			}
			literalFrom = literalOffset + len(encoded)
		}
		searchFrom = offset + len(encodedNeedle)
	}
}

func encodeUTF16(value string, order binary.ByteOrder) []byte {
	runes := []rune(value)
	encoded := make([]byte, len(runes)*2)
	for index, char := range runes {
		order.PutUint16(encoded[index*2:], uint16(char))
	}
	return encoded
}

func progressiveFileOffsetAddress(headers []ELF32ProgramHeader, offset uint32) (uint32, bool) {
	for _, header := range headers {
		if header.Type != elfProgramLoad || offset < header.Offset || offset-header.Offset >= header.FileSize {
			continue
		}
		return header.VirtualAddress + offset - header.Offset, true
	}
	return 0, false
}

func logProgressiveLiteralReferences(t *testing.T, image ProgressiveImage, literalAddress uint32) {
	t.Helper()
	for _, header := range image.ELF.ProgramHeaders {
		if header.Type != elfProgramLoad || header.Flags&1 == 0 || header.FileSize == 0 {
			continue
		}
		if uint64(header.Offset) >= uint64(len(image.Bytes)) {
			continue
		}
		end := min(uint64(header.Offset)+uint64(header.FileSize), uint64(len(image.Bytes)))
		segment := image.Bytes[header.Offset:uint32(end)]
		for offset := 0; offset+4 <= len(segment); offset += 4 {
			instruction := binary.LittleEndian.Uint32(segment[offset : offset+4])
			if instruction&0x0f7f0000 != 0x051f0000 {
				continue
			}
			instructionAddress := header.VirtualAddress + uint32(offset)
			base := instructionAddress + 8
			immediate := instruction & 0xfff
			loadedAddress := base - immediate
			if instruction&1<<23 != 0 {
				loadedAddress = base + immediate
			}
			if loadedAddress == literalAddress {
				t.Logf("ARM literal %#08x is loaded at %#08x", literalAddress, instructionAddress)
			}
		}
		for offset := 0; offset+2 <= len(segment); offset += 2 {
			instruction := binary.LittleEndian.Uint16(segment[offset : offset+2])
			if instruction&0xf800 != 0x4800 {
				continue
			}
			instructionAddress := header.VirtualAddress + uint32(offset)
			loadedAddress := (instructionAddress+4)&^3 + uint32(instruction&0xff)*4
			if loadedAddress == literalAddress {
				t.Logf("Thumb literal %#08x is loaded at %#08x", literalAddress, instructionAddress)
			}
		}
	}
}

func openRawReferenceSet(t *testing.T, directory string) firmwareset.Set {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("configured raw reference directory: %v", err)
	}
	var sources []firmwareset.Source
	for _, entry := range entries {
		if entry.IsDir() || !isReferencePieceExtension(strings.ToLower(filepath.Ext(entry.Name()))) {
			continue
		}
		file, err := os.Open(filepath.Join(directory, entry.Name()))
		check(t, err)
		t.Cleanup(func() { _ = file.Close() })
		info, err := file.Stat()
		check(t, err)
		sources = append(sources, firmwareset.Source{ReaderAt: file, Size: info.Size()})
	}
	if len(sources) != 4 {
		t.Fatalf("configured raw reference contains %d SCH download pieces, want 4", len(sources))
	}
	set, err := firmwareset.NewSet(sources)
	check(t, err)
	return set
}
