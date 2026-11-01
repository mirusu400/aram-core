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
	// 리듬스타 is a native Clet. Since issue #570 its card gets each key's
	// press edge as a press, so it acts on the press 5 frames before the
	// release it used to act on. The 600, 900, 1800 and 2000 checkpoints were
	// re-captured then. They show the same screens as before, at a slightly
	// different animation phase.
	goldens := map[int]string{
		300:  "c3a4ead5d4abc916c85aa9903ea1278cabc06b7582e1740f27e94fbb58c7a636",
		600:  "2da29ca9d14f803d145c07b5c302c78504e3a045fd0f46def09c1bcaacadf673",
		900:  "3338a56da00b31841218b990a8945e41e310cb58c8d3a89bb7e17f317e46afa9",
		1200: "d78ae7617d10352071fd85f1e3de908f55245e47e6d6bd1b3a06559b82f205d2",
		1500: "b4a0b9b90c76eeb2b54127f1e99f9f86a2885c522effad668289492e8e6fb412",
		1800: "bd73724acdf665efe4cba1472edafc3cf171ab85c6aabe2e6f65d3b5803d8839",
		2000: "31ddb0cf589ed9bbf11323b2dcb2edae0e0b5597f8f21e5a0625b0dea8f878c0",
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
