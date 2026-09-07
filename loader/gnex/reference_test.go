package gnex

import (
	"errors"
	"os"
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
