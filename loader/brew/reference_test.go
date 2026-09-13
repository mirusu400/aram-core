package brew

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReferencePackages reads user-authorized inputs in place. Logs contain
// aggregate counts only. Missing MOD members are counted as incomplete inputs,
// not a successful load or execution. No nested archives are unpacked to disk.
func TestReferencePackages(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	var recognized, incomplete, modules, mifs int
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("cannot walk configured corpus")
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(name), ".zip") {
			return nil
		}
		if filter := os.Getenv("ARAM_TEST_FILTER"); filter != "" && !strings.Contains(name, filter) {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return errors.New("cannot read configured corpus member")
		}
		pkg, err := Inspect(data)
		if errors.Is(err, ErrNotPackage) {
			return nil
		}
		if err != nil {
			var bad *FormatError
			if errors.As(err, &bad) && bad.Reason == "MIF metadata present but MOD member is missing" {
				incomplete++
				return nil
			}
			return fmt.Errorf("corpus container validation failed (%T)", err)
		}
		recognized++
		modules += len(pkg.Modules)
		mifs += len(pkg.MIFs)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if recognized+incomplete == 0 {
		t.Fatal("configured corpus contained no recognized BREW input")
	}
	t.Logf("BREW recognized_containers=%d opaque_modules=%d validated_mif_envelopes=%d incomplete_missing_mod=%d; execution not attempted", recognized, modules, mifs, incomplete)
}
