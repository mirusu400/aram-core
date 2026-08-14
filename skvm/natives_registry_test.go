package skvm

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// registeredNativeList captures every native registered on a fresh VM as
// "class name descriptor" lines. It exists so the block-F natives file moves
// cannot silently drop a registration.
func registeredNativeList(t *testing.T) []string {
	t.Helper()
	vm, err := New(map[string][]byte{})
	if err != nil {
		t.Fatalf("create VM: %v", err)
	}
	lines := make([]string, 0, len(vm.natives))
	for key := range vm.natives {
		lines = append(lines, fmt.Sprintf("%s %s %s", key.class, key.name, key.descriptor))
	}
	sort.Strings(lines)
	return lines
}

func TestNativeRegistrySnapshot(t *testing.T) {
	lines := registeredNativeList(t)
	goldenPath := filepath.Join("testdata", "native_registry.golden")
	if os.Getenv("ARAM_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(
			goldenPath,
			[]byte(strings.Join(lines, "\n")+"\n"),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with ARAM_UPDATE_GOLDEN=1 to create): %v", err)
	}
	// A CRLF-converting checkout (git autocrlf on Windows CI) must not make
	// every golden line mismatch on a trailing carriage return.
	normalized := strings.ReplaceAll(string(raw), "\r\n", "\n")
	want := strings.Split(strings.TrimRight(normalized, "\n"), "\n")
	got := lines
	if len(got) != len(want) {
		t.Errorf("registered native count = %d, want %d", len(got), len(want))
	}
	wantSet := make(map[string]struct{}, len(want))
	for _, line := range want {
		wantSet[line] = struct{}{}
	}
	gotSet := make(map[string]struct{}, len(got))
	for _, line := range got {
		gotSet[line] = struct{}{}
		if _, ok := wantSet[line]; !ok {
			t.Errorf("unexpected native registration: %s", line)
		}
	}
	for _, line := range want {
		if _, ok := gotSet[line]; !ok {
			t.Errorf("missing native registration: %s", line)
		}
	}
}
