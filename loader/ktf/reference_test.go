package ktf

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReferencePackages validates a user-provided corpus without making
// proprietary applications part of the repository. Point ARAM_TEST_DATA at
// the extracted dubigame corpus (or another authorized package tree).
func TestReferencePackages(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	var packages int
	var malformed int
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
		pkg, err := Inspect(data)
		if errors.Is(err, ErrNotPackage) {
			return nil
		}
		if err != nil {
			t.Logf("malformed/unreadable %s: %v", path, err)
			malformed++
			return nil
		}
		if pkg.Descriptor.AID == "" || len(pkg.Client) == 0 {
			t.Errorf("%s: incomplete parsed package", path)
			return nil
		}
		packages++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if packages == 0 {
		t.Fatal("ARAM_TEST_DATA contained no valid KTF packages")
	}
	t.Logf("validated %d KTF packages (%d malformed/unreadable)", packages, malformed)
}
