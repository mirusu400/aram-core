package skvm_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	skloader "github.com/mirusu400/aram-core/loader/skvm"
	"github.com/mirusu400/aram-core/skvm"
)

func TestReferenceSKVMLifecycleSmoke(t *testing.T) {
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

	var packages int
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
			t.Errorf("%s: inspect: %v", name, err)
			return nil
		}
		classData := make(map[string][]byte, len(pkg.Classes))
		for className, class := range pkg.Classes {
			classData[className] = class.Data
		}
		machine, err := skvm.New(classData)
		if err != nil {
			t.Errorf("%s: create VM: %v", name, err)
			return nil
		}
		var recentTrace []skvm.TraceEvent
		machine.SetTraceHook(func(event skvm.TraceEvent) error {
			recentTrace = append(recentTrace, event)
			if len(recentTrace) > 16 {
				recentTrace = recentTrace[1:]
			}
			return nil
		})
		machine.InstructionLimit = 2_000_000
		machine.SetResources(pkg.Resources)
		machine.SetProperties(pkg.Descriptor.Raw)
		if _, err := machine.Start(context.Background(), pkg.Descriptor.MainClass); err != nil {
			t.Errorf("%s: start: %v; recent trace: %+v", name, err, recentTrace)
			return nil
		}
		if machine.CurrentDisplay() != 0 {
			if err := machine.ShowCurrent(context.Background()); err != nil {
				t.Errorf("%s: show: %v; recent trace: %+v", name, err, recentTrace)
				return nil
			}
			if err := machine.PaintCurrent(context.Background()); err != nil {
				t.Errorf("%s: paint: %v; recent trace: %+v", name, err, recentTrace)
				return nil
			}
		}
		packages++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if packages == 0 {
		t.Fatal("no SKVM packages were exercised")
	}
	t.Logf(
		"booted %d SKVM packages through lifecycle smoke; this is not a gameplay compatibility claim",
		packages,
	)
}

func firstDirectory(candidates ...string) string {
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}
