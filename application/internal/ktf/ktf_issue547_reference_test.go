package ktf

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ktfloader "github.com/mirusu400/aram-core/loader/ktf"
)

func TestIssue547CletInputEdgeABIsFromAuthorizedPackages(t *testing.T) {
	for _, test := range []struct {
		name      string
		digest    string
		javaEdges bool
	}{
		{
			name:      "Chronos Wing",
			digest:    "1fe6c1c897d708dbb587a083dc450727c12de152de5e9e6458af75294a3061bb",
			javaEdges: true,
		},
		{
			name:      "issue 237 native ABI",
			digest:    "6350b347278915217c9a4c56d82970bcf0ff1c686542c6cf9aff38ec42a7eb3d",
			javaEdges: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			pkg := findAuthorizedIssue547Package(t, test.digest)
			if got := cletJavaInputEdges(pkg); got != test.javaEdges {
				t.Fatalf("Java Clet input edges = %t, want %t", got, test.javaEdges)
			}
		})
	}
}

func findAuthorizedIssue547Package(t *testing.T, digest string) ktfloader.Package {
	t.Helper()
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	var pkg ktfloader.Package
	found := errors.New("exact package found")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".zip") {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(payload)
		if hex.EncodeToString(sum[:]) != digest {
			return nil
		}
		pkg, err = ktfloader.Inspect(payload)
		if err != nil {
			return err
		}
		return found
	})
	if err != nil && !errors.Is(err, found) {
		t.Fatal(err)
	}
	if pkg.Client == nil {
		t.Skipf("ARAM_TEST_DATA holds no package with digest %s", digest)
	}
	return pkg
}
