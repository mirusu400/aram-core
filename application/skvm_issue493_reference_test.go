package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The racing title reads a Thread field inherited from GameCanvas and starts
// a Thread subclass whose Runnable target is null during its loading sequence.
func TestSKVMIssue493RacingLoadingThread(t *testing.T) {
	const digest = "d1234e0ddb75fe4251471a57c72da5f2f2b99ad306c2b4b6277c67e247e10845"
	path, data := findAuthorizedPackage(t, digest)
	ctx := context.Background()
	created, err := NewFactory().Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Close() })
	if err := created.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 1200; frame++ {
		if err := created.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
}
