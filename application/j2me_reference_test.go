package application

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader/j2me"
)

func TestJ2MELGTExactArchivePresentsFrame(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	path := filepath.Join(root, "LGT 다운타운 게임파일", "액션", "[LGT 다운타운] 슈퍼액션히어로(공용).zip")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), Path: path, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatalf("create exact LGT J2ME machine: %v", err)
	}
	defer created.Close()
	machine := created.(*skvmhost.Machine)
	if got := machine.SourceInfo().ProfileID; got != j2me.LGTProfileID {
		t.Fatalf("auto profile = %q, want %q", got, j2me.LGTProfileID)
	}
	if err := machine.Start(context.Background()); err != nil {
		t.Fatalf("start exact LGT J2ME title: %v", err)
	}
	for frame := 0; frame < 256; frame++ {
		if snapshot := machine.DebugSnapshot(8); snapshot.SKVM != nil && snapshot.SKVM.Framebuffer != nil {
			framebuffer := snapshot.SKVM.Framebuffer
			if !framebuffer.SnapshotHashOK || !framebuffer.DescriptorValid {
				t.Fatalf("invalid J2ME guest framebuffer after %d frames: %+v", frame, *framebuffer)
			}
			t.Logf("presented after %d frames: %+v", frame, *framebuffer)
			return
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("step exact LGT J2ME title: %v; snapshot=%+v", err, machine.DebugSnapshot(8))
		}
	}
	t.Fatalf("exact LGT J2ME title produced no frame: %+v", machine.DebugSnapshot(8))
}
