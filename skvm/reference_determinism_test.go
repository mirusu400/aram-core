package skvm_test

import (
	"context"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	skloader "github.com/mirusu400/aram-core/loader/skvm"
	"github.com/mirusu400/aram-core/skvm"
)

// Characterization safety net for the block-F natives file moves: every SKT
// corpus package must boot to its first painted frame deterministically, and
// two independent boots must produce identical framebuffer bytes.
func TestReferenceSKVMBootFrameDeterminism(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	sktRoot := firstDirectory(
		filepath.Join(root, "corpus", "dubigame-202403", "SKT"),
		filepath.Join(root, "dubigame-202403", "SKT"),
		filepath.Join(root, "SKT"),
	)
	if sktRoot == "" {
		t.Skip("SKT reference corpus was not found below ARAM_TEST_DATA")
	}

	bootFrame := func(pkg skloader.Package) ([sha256.Size]byte, bool) {
		classData := make(map[string][]byte, len(pkg.Classes))
		for className, class := range pkg.Classes {
			classData[className] = class.Data
		}
		machine, err := skvm.New(classData)
		if err != nil {
			return [sha256.Size]byte{}, false
		}
		machine.InstructionLimit = 2_000_000
		machine.SetResources(pkg.Resources)
		machine.SetProperties(pkg.Descriptor.Raw)
		if _, err := machine.Start(context.Background(), pkg.Descriptor.MainClass); err != nil {
			return [sha256.Size]byte{}, false
		}
		if machine.CurrentDisplay() == 0 {
			return [sha256.Size]byte{}, false
		}
		if err := machine.ShowCurrent(context.Background()); err != nil {
			return [sha256.Size]byte{}, false
		}
		if err := machine.PaintCurrent(context.Background()); err != nil {
			return [sha256.Size]byte{}, false
		}
		return sha256.Sum256(machine.FrameRGBA()), true
	}

	var packages, hashed int
	err := filepath.WalkDir(sktRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(name) != ".zip" {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		pkg, err := skloader.Inspect(data)
		if err != nil {
			return nil
		}
		packages++
		first, ok := bootFrame(pkg)
		if !ok {
			return nil
		}
		second, ok := bootFrame(pkg)
		if !ok {
			t.Errorf("%s: second boot failed where first succeeded", name)
			return nil
		}
		if first != second {
			t.Errorf("%s: boot frame hash differs between runs", name)
		}
		hashed++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if packages == 0 {
		t.Fatal("no SKVM packages were exercised")
	}
	if hashed == 0 {
		t.Fatal("no SKVM package produced a comparable first frame")
	}
	t.Logf("verified boot-frame determinism for %d/%d packages", hashed, packages)
}
