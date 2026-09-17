package j2me

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLGTReferenceCorpusPackages(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	root = filepath.Join(root, "LGT 다운타운 게임파일")
	inspected := 0
	rejected := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".zip") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		pkg, inspectErr := Inspect(data)
		if inspectErr != nil {
			rejected++
			if errors.Is(inspectErr, ErrNotPackage) {
				t.Logf("LGT archive was not recognized: %s", path)
			} else {
				t.Logf("rejected damaged or inconsistent archive %s: %v", path, inspectErr)
			}
			return nil
		}
		if pkg.ProfileID != LGTProfileID {
			t.Errorf("profile for %s = %q, want %q", path, pkg.ProfileID, LGTProfileID)
		}
		inspected++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if inspected < 95 || rejected > 10 {
		t.Fatalf("inspected=%d rejected=%d, want at least 95 accepted and at most 10 rejected", inspected, rejected)
	}
}
