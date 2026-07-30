package raptor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReferencePackages validates user-provided Raptor packages without
// making proprietary applications part of the repository.
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
		if pkg.Descriptor.AID == "" || len(pkg.Image.AllocatedSections()) == 0 {
			t.Errorf("%s: incomplete parsed package", path)
			return nil
		}
		packages++
		t.Logf(
			"%s: entry=0x%08x sections=%d relocations=%d resources=%d dependencies=%v",
			path,
			pkg.Image.Entry,
			len(pkg.Image.Sections),
			len(pkg.Image.Relocations),
			len(pkg.Resources),
			pkg.Image.Metadata.Dependencies,
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if packages == 0 {
		t.Fatal("ARAM_TEST_DATA contained no valid Raptor package")
	}
	t.Logf("validated %d Raptor packages", packages)
}
