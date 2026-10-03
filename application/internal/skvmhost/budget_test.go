package skvmhost

import (
	"bytes"
	"context"
	"image"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

func TestCrow2BudgetMigratesAnOldSave(t *testing.T) {
	source := machinecore.Source{
		ProfileID: "j2me-1.0/generic/generic",
		SHA256:    crow2ArchiveSHA256,
	}
	if got := skvmRunBudget(source); got != 20_000_000 {
		t.Fatalf("Crow2 budget = %d", got)
	}
	source.SHA256 = strings.Repeat("0", 64)
	if got := skvmRunBudget(source); got != defaultSKVMRunBudget {
		t.Fatalf("unrelated title budget = %d", got)
	}
	source.SHA256 = crow2ArchiveSHA256
	m, err := newJavaMachine(context.Background(), source, Application{}, nil, image.Pt(120, 160), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	old := m.services.Coordinator.Snapshot()
	old.Adapters[0].RunBudget = defaultSKVMRunBudget
	if err := m.services.Coordinator.Restore(old); err != nil {
		t.Fatal(err)
	}
	var saved bytes.Buffer
	if err := m.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	if err := m.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	adapter, err := m.services.Coordinator.Adapter(m.owner)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.RunBudget != 20_000_000 {
		t.Fatalf("restored title budget = %d", adapter.RunBudget)
	}
}
