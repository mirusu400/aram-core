package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The reported Raptor intro repeatedly appends C strings and copies opaque
// framebuffers. Keep its exact output pinned while those host-call paths stay
// allocation-light; timing evidence is collected by the fixed-frame benchmark.
func TestRaptorIssue521OptimizedIntroKeepsOutput(t *testing.T) {
	const digest = "619d98bc8f64c3d4b4e7d975c922ab2e6f0daaa3e26352030f02a864074ba790"
	path, data := findAuthorizedPackage(t, digest)
	ctx := context.Background()
	factory := NewFactory()
	factory.NewCPU = newJITCPU
	factory.FrameRunBudget = DefaultHandsetRunBudget
	factory.RaptorFrameRunBudget = DefaultRaptorFrameRunBudget
	created, err := factory.Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.Start(ctx))
	for frame := 1; frame <= 1200; frame++ {
		if err := machine.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	want := "7e65c304a7b56e9718268d9d71cd6c4438685cdaed7193bbac60ce25ca70d5f1"
	if got := fmt.Sprintf("%x", sha256.Sum256(machine.frame.Pix)); got != want {
		t.Fatalf("intro frame hash = %s, want %s", got, want)
	}
	for _, api := range []string{"strcat", "memset", "MC_grpCopyFrameBuffer"} {
		if machine.wipi.Observed[api] == 0 {
			t.Fatalf("intro did not exercise optimized API %s", api)
		}
	}
}
