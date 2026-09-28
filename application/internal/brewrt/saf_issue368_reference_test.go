package brewrt

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Gunbird+ uses a frame setup record on most sprites and an RGB888 palette on
// its ending illustration. Both forms must decode instead of becoming blank.
func TestIssue368GunbirdSAFResourcesDecode(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	path := filepath.Join(root, "KTF BREW 게임파일", "슈팅", "[KTF BREW] 건버드+(작화).zip")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	if got, want := hex.EncodeToString(digest[:]), "c23aac3be59e08d0b4973743b4c9ec3172b69f078df322464a5b3f6d8799c223"; got != want {
		t.Fatalf("Gunbird+ archive SHA-256=%s, want %s", got, want)
	}
	archive, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		t.Fatal(err)
	}
	decoded := 0
	for _, member := range archive.File {
		if !strings.HasSuffix(strings.ToLower(member.Name), ".bar") {
			continue
		}
		reader, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		bar, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(bar) < 24 || binary.LittleEndian.Uint16(bar) != brewResourceMagic {
			continue
		}
		table := int(binary.LittleEndian.Uint32(bar[16:]))
		sections := int(binary.LittleEndian.Uint32(bar[20:]))
		for section := 0; section < sections && table+(section+2)*4 <= len(bar); section++ {
			start := int(binary.LittleEndian.Uint32(bar[table+section*4:]))
			end := int(binary.LittleEndian.Uint32(bar[table+(section+1)*4:]))
			if start < 0 || end > len(bar) || start+2 > end {
				continue
			}
			data := bar[start:end]
			offset := int(binary.LittleEndian.Uint16(data))
			if offset < 2 || offset >= len(data) || string(data[2:offset]) != "image/sis\x00" {
				continue
			}
			if _, ok := decodeSAFImage(data[offset:]); !ok {
				t.Fatalf("Gunbird+ SAF %s section %d did not decode", member.Name, section)
			}
			decoded++
		}
	}
	if decoded != 140 {
		t.Fatalf("decoded %d Gunbird+ SAF images, want 140", decoded)
	}
}
