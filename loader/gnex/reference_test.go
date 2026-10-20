package gnex

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"testing"
)

// TestReferencePackages validates real GNEX archives without making them
// part of the repository. Point ARAM_TEST_DATA at a directory containing
// GNEX zips (e.g. an aram-test checkout's corpus/gamearchive/SKT/GNEX); every
// other .zip under the tree is walked too and simply skipped via
// ErrNotPackage, matching the sibling loaders' reference tests.
func TestReferencePackages(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	var packages int
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".zip") {
			return nil
		}
		if filter := os.Getenv("ARAM_TEST_FILTER"); filter != "" &&
			!strings.Contains(path, filter) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		pkg, err := Inspect(data)
		if errors.Is(err, ErrNotPackage) {
			return nil
		}
		if err != nil {
			return err
		}
		if pkg.Header.Title == "" || len(pkg.SGS) == 0 {
			t.Errorf("%s: incomplete parsed package", path)
			return nil
		}
		if pkg.Header.ApplicationID != pkg.Header.TitleChecksum {
			t.Errorf("%s: deprecated application ID alias differs", path)
		}
		switch pkg.ManifestKind {
		case ManifestMOD:
			descriptor, decodeErr := DecodeMODDescriptor(pkg.Manifest)
			if decodeErr != nil {
				return fmt.Errorf("%s: decode MOD: %w", path, decodeErr)
			}
			if descriptor.MIMEType != "application/x-gnex-sgs" ||
				!strings.EqualFold(descriptor.PayloadType, "sgs") || descriptor.Version == "" {
				t.Errorf("%s: incomplete MOD identity: %+v", path, descriptor)
			}
		case ManifestINF:
			descriptor, decodeErr := DecodeINFDescriptor(pkg.Manifest)
			if decodeErr != nil {
				return fmt.Errorf("%s: decode INF: %w", path, decodeErr)
			}
			if descriptor.ApplicationKey != pathpkg.Base(pkg.BaseName) {
				t.Errorf("%s: INF key %q does not match %q", path, descriptor.ApplicationKey, pkg.BaseName)
			}
		}
		if name, ok := findCaseInsensitive(pkg.Files, pkg.BaseName+".res"); ok {
			icon, decodeErr := DecodeRESIcon(pkg.Files[name])
			if decodeErr != nil || len(icon.Payload) == 0 {
				return fmt.Errorf("%s: decode RES: %w", path, decodeErr)
			}
		}
		if name, ok := findCaseInsensitive(pkg.Files, pkg.BaseName+".wmr"); ok {
			bundle, decodeErr := DecodeWMRBundle(pkg.Files[name])
			if decodeErr != nil {
				return fmt.Errorf("%s: decode WMR: %w", path, decodeErr)
			}
			if len(bundle.Resources) != 2 || !bytes.HasPrefix(bundle.Resources[0].Data, []byte("BM")) ||
				!bytes.HasPrefix(bundle.Resources[1].Data, []byte("ABM")) {
				t.Errorf("%s: unexpected WMR resources: %+v", path, bundle.Resources)
			}
		}
		legacyApplicationIDs := map[string]uint16{
			".hack//sign": 34064, "놈": 34043, "라그나로크 외전": 34059,
			"마시마로격투 맞고": 34298, "북벌": 34070, "생과일 타이쿤": 34300,
			"스트립맞고(여)": 34206, "짜요짜요타이쿤": 34048, "판타지 블루": 34006,
		}
		if want, ok := legacyApplicationIDs[pkg.Header.Title]; ok && pkg.Header.ApplicationID != want {
			t.Errorf("%s: application ID = %d, want %d", path, pkg.Header.ApplicationID, want)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) == "bf9cd39b1ae14ba2a5f005d66fde5390e53cb400bd36940d4b09dcbfe9f03883" {
			image, decodeErr := DecodeGNEX32Image(pkg.SGS)
			if decodeErr != nil {
				return decodeErr
			}
			if image.Header.FormatVersion != 4 || image.Header.Title != "바이러스무삭제" ||
				image.EntryField != 85462 || image.Entry != 85494 || len(image.Symbols) != 479 || len(image.Media) != 2233 {
				t.Errorf("%s: unexpected GNEX32 layout", path)
			}
			wantPointers := [11]uint32{85494, 0, 15206, 478, 0, 0, 14670, 12882}
			if got := image.Callbacks.Pointers(); got != wantPointers {
				t.Errorf("%s: unexpected GNEX32 code pointers: %v", path, got)
			}
			memory, memoryErr := image.NewSymbolMemory()
			if memoryErr != nil {
				return memoryErr
			}
			var mutable, constant int
			for _, symbol := range image.Symbols {
				switch symbol.Flags {
				case 1:
					mutable++
				case 0x100:
					constant++
				}
			}
			if mutable != 327 || constant != 152 {
				t.Errorf("%s: GNEX32 symbol flags: mutable=%d constant=%d", path, mutable, constant)
			}
			if err := memory.WriteWord(0xe9, 0, 42); err != nil {
				t.Errorf("%s: GNEX32 mutable storage: %v", path, err)
			} else if got, err := memory.ReadWord(0xe9, 0); err != nil || got != 42 || image.Symbols[0xe9].Data[0] != 0 {
				t.Errorf("%s: GNEX32 mutable storage = %d, %v", path, got, err)
			}
			// The first two candidate address operands at the entry point
			// resolve to the first and second code words. A late operand
			// with nonzero high word resolves to the final code word.
			entryOffset := int(image.Entry) - image.Header.BodyOffset
			for _, tc := range []struct{ operand, target int }{
				{entryOffset + 2, 0}, {entryOffset + 8, 2},
				{len(image.Code) - 50, len(image.Code) - 2},
			} {
				got, decodeErr := image.DecodeCodeAddress(tc.operand)
				if decodeErr != nil || got != tc.target {
					t.Errorf("%s: GNEX32 code address at %d = %d, %v; want %d", path, tc.operand, got, decodeErr, tc.target)
				}
			}
			for _, tc := range []struct {
				offset, opcode, word, target, next int
			}{
				{entryOffset, 0x85, 0, 0, entryOffset + 6},
				{int(image.Callbacks.Timeout) - image.Header.BodyOffset + 8, 0x7f, 0, 15250 - image.Header.BodyOffset, 15222 - image.Header.BodyOffset},
				{len(image.Code) - 52, 0x81, 0, len(image.Code) - 2, len(image.Code) - 46},
			} {
				got, decodeErr := image.DecodeAddressInstruction(tc.offset)
				if decodeErr != nil || int(got.Opcode) != tc.opcode || int(got.Word) != tc.word ||
					got.Target != tc.target || got.Next != tc.next {
					t.Errorf("%s: GNEX32 address instruction at %d = %+v, %v", path, tc.offset, got, decodeErr)
				}
			}
		}
		packages++
		t.Logf(
			"%s: title=%q version=%d manifest=%s(%s) sgs_bytes=%d body_offset=%d",
			path,
			pkg.Header.Title,
			pkg.Header.FormatVersion,
			pkg.ManifestName,
			pkg.ManifestKind,
			len(pkg.SGS),
			pkg.Header.BodyOffset,
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if packages == 0 {
		t.Skip("ARAM_TEST_DATA contained no GNEX package")
	}
	t.Logf("parsed %d GNEX packages; this is a recognition/header claim, not an execution claim", packages)
}
