package ktf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

func TestIssue529TurnTimerKeepsOneWorker(t *testing.T) {
	const digest = "82ad4f8139517a8ae4aa81bef2cde8ea5b6898c262d2e031563949c2bf02c04e"
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	var data []byte
	found := errors.New("exact package found")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".zip" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() != 399950 {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(payload)
		if hex.EncodeToString(sum[:]) == digest {
			data = payload
			return found
		}
		return nil
	})
	if err != nil && !errors.Is(err, found) {
		t.Fatal(err)
	}
	if data == nil {
		t.Skipf("ARAM_TEST_DATA holds no package with digest %s", digest)
	}
	pkg, err := ktf.Inspect(data)
	check(t, err)
	r, err := NewRuntime(interpreter.New(), pkg)
	check(t, err)
	t.Cleanup(func() { _ = r.CPU.Close() })
	r.DeferThreads = true
	check(t, r.MapImageAndHost())
	ctx := context.Background()
	_, _, err = r.Bootstrap(ctx)
	check(t, err)
	check(t, r.Initialize(ctx))
	check(t, r.StartMainClass(ctx))
	var pressed bool
	for ms := uint64(0); ms < 40000; ms += 10 {
		r.TickMS = ms
		check(t, r.Services.Clock.Advance(10*time.Millisecond))
		if ms >= 6000 && (ms%2000 == 0 || ms%2000 == 100) {
			pressed = ms%2000 == 0
			_, err := r.QueueKeyEvent(pressed, -5)
			check(t, err)
		}
		for slice := 0; slice < 32; slice++ {
			result := r.RunTaskSlice(ctx, 100000)
			check(t, result.Err)
			owners := make(map[uint32]bool)
			for _, task := range r.Tasks {
				if task.Done || task.timerOwner == 0 || !task.timerStarted {
					continue
				}
				if owners[task.timerOwner] {
					t.Fatalf("at %dms, the turn Timer started a second callback before the first returned", ms)
				}
				owners[task.timerOwner] = true
			}
			if !r.HasRunnableTask() {
				break
			}
		}
	}
	if r.PresentCount < 100 {
		t.Fatalf("turn replay presented only %d frames", r.PresentCount)
	}
}
