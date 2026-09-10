package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// This optional check reads the authorized issue #267 input in place. No
// proprietary bytes or rendered frames are stored in the repository.
func TestReferenceKTFMNClassTableStartsAndPresents(t *testing.T) {
	path, data := findAuthorizedPackage(t, "56d6865251cc6e3bb2e07065d5d460591478b8c7175e2798ce597d2534794cc3")
	factory := NewFactory()
	factory.NewCPU = func() cpu.Backend { return interpreter.New() }
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if machine.ktf == nil {
		t.Fatal("input did not select the KTF runtime")
	}
	mainClass, err := machine.ktf.LoadClass(context.Background(), "ED3")
	check(t, err)
	if mainClass.Address != machine.ktf.JavaClasses["ED3"] || len(mainClass.Methods) == 0 {
		t.Fatal("ED3 was not registered with its guest methods")
	}
	if err := machine.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	const frames = 120
	var nonuniform bool
	for frame := 0; frame < frames; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		image := machine.Framebuffer()
		bounds := image.Bounds()
		first := image.At(bounds.Min.X, bounds.Min.Y)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				if image.At(x, y) != first {
					nonuniform = true
				}
			}
		}
	}
	if !machine.ktfStarted || machine.ktf.MainJlet == 0 || machine.ktf.PresentCount == 0 || !nonuniform {
		t.Fatalf("started=%t main=%08x presents=%d nonuniform=%t",
			machine.ktfStarted, machine.ktf.MainJlet, machine.ktf.PresentCount, nonuniform)
	}
	t.Logf("ED3=%08x methods=%d started=%t frames=%d presents=%d instructions=%d nonuniform=%t",
		mainClass.Address, len(mainClass.Methods), machine.ktfStarted, frames,
		machine.ktf.PresentCount, machine.ktf.TotalInstructions, nonuniform)
}
