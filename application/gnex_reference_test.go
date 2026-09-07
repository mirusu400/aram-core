package application

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/gnex"
)

// TestReferenceGNEXPackagesAreRecognizedNotExecuted walks a real GNEX corpus
// (point ARAM_TEST_DATA at a directory containing GNEX zips, e.g. an
// aram-test checkout's corpus/gamearchive/SKT/GNEX) and checks that the
// factory recognizes every title and reports the specific "not yet
// executable" error from machine_load.go rather than a generic or
// container-shaped one. This is a recognition claim, not an execution claim:
// GVM bytecode is not run.
func TestReferenceGNEXPackagesAreRecognizedNotExecuted(t *testing.T) {
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
		pkg, inspectErr := gnex.Inspect(data)
		if errors.Is(inspectErr, gnex.ErrNotPackage) {
			return nil
		}
		if inspectErr != nil {
			return inspectErr
		}
		_, createErr := NewFactory().Create(context.Background(), machinecore.Source{
			Name:     filepath.Base(path),
			ReaderAt: bytes.NewReader(data),
			Size:     int64(len(data)),
		})
		if !errors.Is(createErr, ErrUnsupportedSource) {
			t.Errorf("%s: Create error = %v, want ErrUnsupportedSource", path, createErr)
			return nil
		}
		if !strings.Contains(createErr.Error(), pkg.Header.Title) {
			t.Errorf("%s: error does not name the parsed title %q: %v", path, pkg.Header.Title, createErr)
			return nil
		}
		packages++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if packages == 0 {
		t.Skip("ARAM_TEST_DATA contained no GNEX package")
	}
	t.Logf("confirmed %d GNEX packages are recognized (not executed)", packages)
}
