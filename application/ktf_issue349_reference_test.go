package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

const rhythmStarOneSHA256 = "af219770b1f937750d743eabe3a709531faa9f6aa8d7907016566a5f7bad0258"

func TestKTFIssue349FillLoopPreservesGameAndAudio(t *testing.T) {
	path, data := findAuthorizedPackage(t, rhythmStarOneSHA256)
	var machines [2]*Machine
	for index, newCPU := range []CPUFactory{newPreciseCPU, newJITCPU} {
		factory := NewFactory()
		factory.NewCPU = newCPU
		factory.RunBudget = DefaultKTFHandsetRunBudget
		factory.KTFRunBudget = DefaultKTFHandsetRunBudget
		factory.FrameRunBudget = DefaultKTFHandsetRunBudget
		created, err := factory.Create(context.Background(), machinecore.Source{Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
		if err != nil {
			t.Fatal(err)
		}
		machines[index] = created.(*Machine)
		t.Cleanup(func() { _ = created.Close() })
		if err := created.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// Captured before fill-loop acceleration; the script reaches song play.
	goldens := map[int]string{
		300:  "c3a4ead5d4abc916c85aa9903ea1278cabc06b7582e1740f27e94fbb58c7a636",
		600:  "782ac4c4dd86d479f96e8e6779bda8b36b668ab9c408173b91ba906c8babb0f8",
		900:  "5a5e69a61e26f9d6a6bc6e7224ceb408ee24ea5c55432c094ad1956fc48a3cd7",
		1200: "d78ae7617d10352071fd85f1e3de908f55245e47e6d6bd1b3a06559b82f205d2",
		1500: "b4a0b9b90c76eeb2b54127f1e99f9f86a2885c522effad668289492e8e6fb412",
		1800: "8a25cf7d64265aff4fc1df0ecfb69fb2a388665bd35eb4a116299b515aa394b8",
		2000: "fec763a17f30559a1360cc9a42a8971280859dea8b0fb4a203215689ff919e12",
	}
	var elapsed [2]time.Duration
	var nonzeroAudioSamples uint64
	for frame := 1; frame <= 2000; frame++ {
		var audio [2]machinecore.AudioChunk
		for index, machine := range machines {
			if frame >= 301 && frame <= 1806 && (frame%300 == 1 || frame%300 == 6) {
				if err := machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: frame%300 == 1}); err != nil {
					t.Fatal(err)
				}
			}
			started := time.Now()
			if err := machine.StepFrame(context.Background()); err != nil {
				t.Fatalf("backend %d frame %d: %v", index, frame, err)
			}
			elapsed[index] += time.Since(started)
			audio[index] = machine.DrainPublishedAudio()
		}
		if !bytes.Equal(machines[0].frame.Pix, machines[1].frame.Pix) || machines[0].ktf.PresentCount != machines[1].ktf.PresentCount {
			t.Fatalf("frame %d presentation differs", frame)
		}
		if want, ok := goldens[frame]; ok {
			sum := sha256.Sum256(machines[1].frame.Pix)
			if got := hex.EncodeToString(sum[:]); got != want {
				t.Fatalf("frame %d hash=%s want=%s", frame, got, want)
			}
		}
		if !slices.Equal(audio[0].PCM16, audio[1].PCM16) || audio[0].SampleRate != audio[1].SampleRate || audio[0].Channels != audio[1].Channels || audio[0].StartGuestNS != audio[1].StartGuestNS || audio[0].StartSample != audio[1].StartSample || audio[0].Generation != audio[1].Generation {
			t.Fatalf("frame %d audio differs", frame)
		}
		for _, sample := range audio[0].PCM16 {
			if sample != 0 {
				nonzeroAudioSamples++
			}
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
				t.Fatalf("frame %d register %d=%08x want=%08x", frame, register, got, want)
			}
		}
	}
	stats := machines[1].cpu.(cpu.ExecutionStatisticsBackend).ExecutionStatistics()
	if stats.AcceleratedLoopIterations < 1_000_000 {
		t.Fatalf("hot fill loop did not accelerate: %+v", stats)
	}
	if nonzeroAudioSamples < 1000 {
		t.Fatalf("expected actual music output, nonzero samples=%d", nonzeroAudioSamples)
	}
	t.Logf("2000 frames: precise=%s jit=%s accelerated iterations=%d nonzero audio samples=%d", elapsed[0], elapsed[1], stats.AcceleratedLoopIterations, nonzeroAudioSamples)
}
