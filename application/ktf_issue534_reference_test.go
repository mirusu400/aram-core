package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader"
	ktfloader "github.com/mirusu400/aram-core/loader/ktf"
)

// The reported input is the inner JAR of this authorized KTF distribution.
// Read it from ARAM_TEST_DATA in place so no proprietary game bytes enter the
// repository, then verify that the exact bare input selects and runs KTF.
func TestIssue534BareKTFJARStarts(t *testing.T) {
	const (
		archiveDigest = "20b1370aa805d2e91b291e82812e710dc229e69e625d659fb63474add803cd11"
		jarDigest     = "45ed5e7ad00607fe13894b26dc9a5e0657befa811304899f052f84e1b65dc9ef"
	)
	_, archive := findAuthorizedPackage(t, archiveDigest)
	pkg, err := ktfloader.Inspect(archive)
	check(t, err)
	jar := pkg.Files[pkg.JARName]
	if got := fmt.Sprintf("%x", sha256.Sum256(jar)); got != jarDigest {
		t.Fatalf("inner JAR digest = %s, want %s", got, jarDigest)
	}

	factory := NewFactory()
	factory.NewCPU, err = ResolveCPUBackend("fastest")
	check(t, err)
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     "010100D2.jar",
		ReaderAt: bytes.NewReader(jar),
		Size:     int64(len(jar)),
		SHA256:   jarDigest,
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if machine.ktf == nil || machine.info.SourceKind != loader.KindKTF ||
		machine.ktf.Pkg.Descriptor.AID != "010100D2" ||
		machine.ktf.Pkg.Descriptor.MainClass != "DeathGame" {
		t.Fatalf("bare JAR selected %+v with KTF runtime %t", machine.info, machine.ktf != nil)
	}
	check(t, machine.Start(context.Background()))
	for frame := 0; frame < 180; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	if !machine.ktfStarted || machine.ktf.MainJlet == 0 {
		t.Fatalf("started=%t main=%08x presents=%d",
			machine.ktfStarted, machine.ktf.MainJlet, machine.ktf.PresentCount)
	}
}
