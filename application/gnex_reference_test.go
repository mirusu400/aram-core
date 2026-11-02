package application

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

// TestReferenceLegacyGNEXPackagesAreQualified walks the authorized corpus and
// requires every recognized version-1/2 package to have an exact operational
// profile. A newly added corpus package therefore remains fail-closed until it
// receives its own execution qualification.
func TestReferenceLegacyGNEXPackagesAreQualified(t *testing.T) {
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
		digest := sha256.Sum256(data)
		pkg, inspectErr := gnex.Inspect(data)
		if errors.Is(inspectErr, gnex.ErrNotPackage) {
			return nil
		}
		if inspectErr != nil {
			return inspectErr
		}
		if pkg.Header.FormatVersion > 2 {
			return nil
		}
		if _, operational := gvmOperationalCorpora[hex.EncodeToString(digest[:])]; !operational {
			t.Errorf("%s: legacy GNEX title %q is not operationally qualified", path, pkg.Header.Title)
		}
		packages++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if packages == 0 {
		t.Skip("ARAM_TEST_DATA contained no legacy GNEX package")
	}
	t.Logf("confirmed %d legacy GNEX packages are operationally qualified", packages)
}
