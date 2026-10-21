package application

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
	skengine "github.com/mirusu400/aram-core/skvm"
)

func TestReferenceSKVMIssue542HeroTale2InputRegression(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	name := filepath.Join(
		root,
		"corpus",
		"f-app",
		"SKT",
		"RPG_전략",
		"영웅서기2.zip",
	)
	data, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		t.Skipf("issue #542 reference title is not present: %s", name)
	}
	check(t, err)
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(name),
		SHA256:   "3de4bcb4f5e269fa2fc37786efe41cdd8e05548e772e18f31435300e6ae335ec",
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	check(t, err)
	machine, ok := created.(*skvmhost.Machine)
	if !ok {
		t.Fatalf("issue #542 machine is %T, want *skvmhost.Machine", created)
	}
	t.Cleanup(func() { _ = machine.Close() })

	var recentTrace []skengine.TraceEvent
	machine.VM().SetTraceHook(func(event skengine.TraceEvent) error {
		recentTrace = append(recentTrace, event)
		if len(recentTrace) > 96 {
			recentTrace = recentTrace[1:]
		}
		return nil
	})
	check(t, machine.Start(context.Background()))
	events := map[int]machinecore.InputEvent{
		7:  {Control: "num7", Pressed: true},
		8:  {Control: "num5", Pressed: true},
		12: {Control: "num0", Pressed: true},
		19: {Control: "menu", Pressed: true},
		23: {Control: "num3", Pressed: true},
		26: {Control: "num7", Pressed: false},
		29: {Control: "soft-right", Pressed: true},
		40: {Control: "back", Pressed: true},
		46: {Control: "end", Pressed: true},
		47: {Control: "num2", Pressed: true},
	}
	for frame := range 60 {
		if event, ok := events[frame]; ok {
			check(t, machine.QueueInput(event))
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("issue #542 frame %d: %v\nrecent trace:\n%s", frame, err, formatSKVMTrace(recentTrace))
		}
	}
}

func formatSKVMTrace(trace []skengine.TraceEvent) string {
	var output bytes.Buffer
	for _, event := range trace {
		fmt.Fprintf(
			&output,
			"depth=%d %s.%s%s pc=%d opcode=0x%02x target=%s\n",
			event.Depth,
			event.Class,
			event.Method,
			event.Descriptor,
			event.PC,
			event.Opcode,
			event.Target,
		)
	}
	return output.String()
}
