package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

const heroesLore3SHA256 = "be4b2e2895d3d6bd4305dc02795ff4c8133e2be53d78ef519293b38b65193a2e"

func TestKTFIssue348ColorLoopPreservesGameAndAudio(t *testing.T) {
	path, data := findAuthorizedPackage(t, heroesLore3SHA256)
	var machines [2]*Machine
	for index, newCPU := range []CPUFactory{newPreciseCPU, newJITCPU} {
		factory := NewFactory()
		factory.NewCPU = newCPU
		factory.RunBudget = DefaultKTFHandsetRunBudget
		factory.KTFRunBudget = DefaultKTFHandsetRunBudget
		factory.FrameRunBudget = DefaultKTFHandsetRunBudget
		created, err := factory.Create(context.Background(), machinecore.Source{
			Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
		})
		if err != nil {
			t.Fatal(err)
		}
		machines[index] = created.(*Machine)
		t.Cleanup(func() { _ = created.Close() })
		if err := created.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var elapsed [2]time.Duration
	audioHashes := [2][]byte{}
	for frame := 0; frame < 600; frame++ {
		for index, machine := range machines {
			started := time.Now()
			if err := machine.StepFrame(context.Background()); err != nil {
				t.Fatalf("backend %d frame %d: %v", index, frame, err)
			}
			elapsed[index] += time.Since(started)
			chunk := machine.DrainPublishedAudio()
			pcm := make([]byte, len(chunk.PCM16)*2)
			for i, sample := range chunk.PCM16 {
				binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
			}
			sum := sha256.Sum256(pcm)
			audioHashes[index] = append(audioHashes[index], sum[:]...)
		}
		if !bytes.Equal(machines[0].frame.Pix, machines[1].frame.Pix) {
			t.Fatalf("frame %d framebuffer differs from precise interpreter", frame)
		}
		if machines[0].ktf.PresentCount != machines[1].ktf.PresentCount {
			t.Fatalf("frame %d presentation count differs", frame)
		}
		for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
			want, err := machines[0].cpu.ReadRegister(register)
			if err != nil {
				t.Fatal(err)
			}
			got, err := machines[1].cpu.ReadRegister(register)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("frame %d register %d = %08x, want %08x", frame, register, got, want)
			}
		}
	}
	if !bytes.Equal(audioHashes[0], audioHashes[1]) {
		t.Fatal("published audio differs from precise interpreter")
	}
	stats := machines[1].cpu.(cpu.ExecutionStatisticsBackend).ExecutionStatistics()
	if stats.AcceleratedLoopIterations < 1_000_000 {
		t.Fatalf("expected the game's hot color loop to accelerate: %+v", stats)
	}
	t.Logf("600 frames: precise=%s jit=%s accelerated iterations=%d", elapsed[0], elapsed[1], stats.AcceleratedLoopIterations)
}
