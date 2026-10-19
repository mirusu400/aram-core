package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Read the reported package in place; no proprietary bytes are needed by the
// synthetic empty-text tests. Repeated confirmation reproduces the intro hang.
func TestEmuIssue22MiniLovelyIntroContinues(t *testing.T) {
	path, data := findAuthorizedPackage(t, "d448aee681574bffb97907e5ed5cdcf6676825e49711633ebe8ee675a76c4cb9")
	var reference [32]byte
	for _, backend := range []string{"precise", "native"} {
		t.Run(backend, func(t *testing.T) {
			factory := NewFactory()
			var err error
			factory.NewCPU, err = ResolveCPUBackend(backend)
			check(t, err)
			factory.RunBudget = DefaultHandsetRunBudget
			factory.FrameRunBudget = DefaultHandsetRunBudget
			factory.KTFRunBudget = DefaultKTFHandsetRunBudget
			created, err := factory.Create(context.Background(), machinecore.Source{
				Name: filepath.Base(path), Path: path, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
			})
			check(t, err)
			m := created.(*Machine)
			t.Cleanup(func() { _ = m.Close() })
			check(t, m.Start(context.Background()))
			var before [32]byte
			var presents uint32
			for frame := 0; frame < 3600; frame++ {
				if frame >= 80 && (frame%80 == 0 || frame%80 == 2) {
					check(t, m.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: frame%80 == 0}))
				}
				check(t, m.StepFrame(context.Background()))
				if m.ktf.FirstJavaThrowName != "" {
					t.Fatalf("frame %d: intro threw %s", frame, m.ktf.FirstJavaThrowName)
				}
				if frame == 2800 {
					before = sha256.Sum256(m.frame.Pix)
					presents = m.ktf.PresentCount
				}
			}
			if m.ktf.PresentCount <= presents+50 || sha256.Sum256(m.frame.Pix) == before {
				t.Fatal("intro stopped presenting new content")
			}
			snapshot := m.DebugSnapshot(1).KTF
			if len(snapshot.IsolatedTaskFaults) != 0 || len(snapshot.Tasks) <= 2 || snapshot.Tasks[2].Done {
				t.Fatal("the intro's game thread terminated")
			}
			final := sha256.Sum256(m.frame.Pix)
			if backend == "precise" {
				reference = final
			} else if final != reference {
				t.Fatal("native and precise intro output disagree")
			}
		})
	}
}
