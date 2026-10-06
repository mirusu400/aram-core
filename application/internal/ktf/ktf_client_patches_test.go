package ktf

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func TestPatchKTFClientRequiresAuthenticatedInstructions(t *testing.T) {
	client := make([]byte, makjangCopyGuardOffset+4)
	copy(client[makjangCopyGuardOffset:], makjangCopyGuardOriginal[:])
	original := bytes.Clone(client)

	unmatched, enabled, err := patchKTFClient(client)
	if err != nil || enabled || !bytes.Equal(unmatched, original) {
		t.Fatalf("unmatched client patched: enabled=%t err=%v", enabled, err)
	}
	patched, enabled, err := patchKTFClientForDigest(makjangClientSHA256, client)
	if err != nil || !enabled || !bytes.Equal(patched[makjangCopyGuardOffset:], makjangCopyGuardPatched[:]) {
		t.Fatalf("authenticated client was not patched: enabled=%t err=%v", enabled, err)
	}
	if !bytes.Equal(client, original) {
		t.Fatal("patch mutated package client bytes")
	}
	again, enabled, err := patchKTFClientForDigest(makjangPatchedClientSHA256, patched)
	if err != nil || !enabled || !bytes.Equal(again, patched) {
		t.Fatalf("patched client was not accepted: enabled=%t err=%v", enabled, err)
	}
	bad := bytes.Clone(client)
	bad[makjangCopyGuardOffset] ^= 1
	if _, _, err := patchKTFClientForDigest(makjangClientSHA256, bad); err == nil {
		t.Fatal("changed instructions accepted")
	}
	if _, _, err := patchKTFClientForDigest(makjangClientSHA256, client[:makjangCopyGuardOffset]); err == nil {
		t.Fatal("truncated client accepted")
	}
}

func TestRestoreKnownClientPatchesUpgradesOldSnapshots(t *testing.T) {
	for _, test := range []struct {
		name      string
		initial   [4]byte
		enabled   bool
		want      [4]byte
		wantError bool
	}{
		{"old snapshot", makjangCopyGuardOriginal, true, makjangCopyGuardPatched, false},
		{"current snapshot", makjangCopyGuardPatched, true, makjangCopyGuardPatched, false},
		{"other title", makjangCopyGuardOriginal, false, makjangCopyGuardOriginal, false},
		{"changed code", [4]byte{1, 2, 3, 4}, true, [4]byte{1, 2, 3, 4}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := interpreter.New()
			defer backend.Close()
			if err := backend.Map(ImageBase, makjangCopyGuardOffset+4, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
				t.Fatal(err)
			}
			address := ImageBase + makjangCopyGuardOffset
			if err := backend.WriteMemory(address, test.initial[:]); err != nil {
				t.Fatal(err)
			}
			runtime := &Runtime{CPU: backend, makjangCopyGuard: test.enabled}
			err := runtime.RestoreKnownClientPatches()
			if (err != nil) != test.wantError {
				t.Fatalf("restore error=%v, wantError=%t", err, test.wantError)
			}
			var got [4]byte
			if err := backend.ReadMemory(address, got[:]); err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("restored instructions=%x, want %x", got, test.want)
			}
		})
	}
}
