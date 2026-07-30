package application

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	skengine "github.com/mirusu400/aram-core/skvm"
)

func TestReferenceSKVMApplicationFrameSoak(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	sktRoot := firstSKVMReferenceDirectory(
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
		if filter := os.Getenv("ARAM_TEST_FILTER"); filter != "" &&
			!strings.Contains(name, filter) {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		created, err := NewFactory().Create(context.Background(), machinecore.Source{
			Name:     filepath.Base(name),
			ReaderAt: bytes.NewReader(data),
			Size:     int64(len(data)),
		})
		if err != nil {
			t.Errorf("%s: create application machine: %v", name, err)
			return nil
		}
		machine, ok := created.(*skvmMachine)
		if !ok {
			t.Errorf("%s: application machine is %T, want *skvmMachine", name, created)
			return nil
		}
		defer machine.Close()

		var recentTrace []skengine.TraceEvent
		machine.vm.SetTraceHook(func(event skengine.TraceEvent) error {
			recentTrace = append(recentTrace, event)
			if len(recentTrace) > 16 {
				recentTrace = recentTrace[1:]
			}
			return nil
		})
		if err := machine.Start(context.Background()); err != nil {
			t.Errorf("%s: start: %v; recent trace: %+v", name, err, recentTrace)
			return nil
		}
		for frame := range 2_048 {
			if err := machine.StepFrame(context.Background()); err != nil {
				t.Errorf(
					"%s: frame %d: %v; recent trace: %+v",
					name,
					frame,
					err,
					recentTrace,
				)
				return nil
			}
			if frame == 63 {
				if err := roundTripReferenceSKVMState(machine); err != nil {
					t.Errorf(
						"%s: state round trip: %v; recent trace: %+v",
						name,
						err,
						recentTrace,
					)
					return nil
				}
			}
		}
		packages++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if packages == 0 {
		t.Fatal("no SKVM packages completed the application frame soak")
	}
	t.Logf(
		"ran %d SKVM packages for 2048 frames; this is not a gameplay compatibility claim",
		packages,
	)
}

func roundTripReferenceSKVMState(machine *skvmMachine) error {
	if err := machine.Pause(); err != nil {
		return err
	}
	before := append([]byte(nil), machine.vm.FrameRGBA()...)
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		return err
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		return err
	}
	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		return err
	}
	after := machine.vm.FrameRGBA()
	if !bytes.Equal(after, before) {
		return fmt.Errorf("framebuffer changed across state restoration")
	}
	return machine.Resume()
}

func firstSKVMReferenceDirectory(candidates ...string) string {
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}
