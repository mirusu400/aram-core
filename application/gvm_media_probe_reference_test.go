package application

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func TestReferenceGVMEncodedAudioPrefixes(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	counts := make(map[string]int)
	packages := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".zip") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		if _, ok := gvmOperationalCorpora[hex.EncodeToString(digest[:])]; !ok {
			return nil
		}
		pkg, err := gnex.Inspect(data)
		if err != nil {
			return err
		}
		image, err := gnex.DecodeExecutionImage(pkg.SGS)
		if err != nil {
			return err
		}
		packages++
		for _, resource := range image.Media {
			payload := resource.Data
			for offset := 0; offset <= 2 && offset+4 <= len(payload); offset++ {
				signature := string(payload[offset : offset+4])
				if signature == "MMMD" || signature == "MThd" || signature == "RIFF" {
					if offset != 2 || len(payload) < 2 || payload[0] != 2 || payload[1] != 0 {
						t.Errorf("%s media signature %s has unsupported prefix %x", path, signature, payload[:offset])
					}
					key := fmt.Sprintf("prefix=%02x%02x offset=%d signature=%s", payload[0], payload[1], offset, signature)
					counts[key]++
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if packages == 0 {
		t.Skip("ARAM_TEST_DATA contained no qualified GVM package")
	}
	for key, count := range counts {
		t.Logf("%s count=%d", key, count)
	}
}
