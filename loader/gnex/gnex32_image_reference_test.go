package gnex

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This checks exact layout on the user-supplied image without retaining media
// bytes in the repository. It does not establish rendered colors or behavior.
func TestGNEX32ReferenceIndexedImageLayouts(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	const wantHash = "bf9cd39b1ae14ba2a5f005d66fde5390e53cb400bd36940d4b09dcbfe9f03883"
	var found bool
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".zip") || !strings.Contains(path, "[GNEX]") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != wantHash {
			return nil
		}
		found = true
		pkg, err := Inspect(data)
		if err != nil {
			return err
		}
		image, err := DecodeGNEX32Image(pkg.SGS)
		if err != nil {
			return err
		}
		var counts, largeCounts [2]int
		var modes, largeModes [2]int
		var rejectedSmall int
		for index, media := range image.Media {
			if media.Flags != 0x100 || len(media.Data) < 7 ||
				(media.Data[0] != 0x0a && media.Data[0] != 0x0b) {
				continue
			}
			decoded, err := DecodeGNEX32IndexedImage(media.Data)
			if err != nil {
				if len(media.Data) >= 80 {
					t.Errorf("media %d: %v", index, err)
				} else {
					rejectedSmall++
				}
				continue
			}
			counts[int(media.Data[0]-0x0a)]++
			modes[int(decoded.Mode)]++
			if len(media.Data) >= 80 {
				largeCounts[int(media.Data[0]-0x0a)]++
				largeModes[int(decoded.Mode)]++
				key := bytes.Equal(decoded.PaletteTriplets[:3], []byte{0x20, 0x90, 0x20})
				if key != (decoded.Mode == 1) {
					t.Errorf("media %d: mode %d and palette-zero key disagree", index, decoded.Mode)
				}
			}
		}
		if counts != [2]int{237, 354} || modes != [2]int{50, 541} || rejectedSmall != 23 {
			t.Errorf("all indexed-image counts = %v modes = %v rejected small = %d; want [237 354], [50 541], 23", counts, modes, rejectedSmall)
		}
		if largeCounts != [2]int{63, 288} || largeModes != [2]int{30, 321} {
			t.Errorf("large indexed-image counts = %v modes = %v; want [63 288], [30 321]", largeCounts, largeModes)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Skip("hash-qualified GNEX32 reference archive is absent")
	}
}
