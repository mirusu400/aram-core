package ktf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const (
	makjangClientSHA256        = "24628234698dbf5e5e8b2f3dae11c0ce4ef4ba22e510996f708861f7c2a495c6"
	makjangPatchedClientSHA256 = "f02dae90490ee22947bf48a387c91cd60861b958145baf84314796a4a94bdf84"
	makjangCopyGuardOffset     = 0xa570
)

var (
	makjangCopyGuardOriginal = [4]byte{0x00, 0x2b, 0x0a, 0xd0} // cmp r3, #0; beq skip
	makjangCopyGuardPatched  = [4]byte{0x9a, 0x42, 0x0a, 0xd3} // cmp r2, r3; blo skip
)

// patchKTFClient repairs one authenticated title's list-compaction guard in
// the loaded client copy. Its 16-byte terminator can clear the adjacent count;
// compacting count(0) from index(1) otherwise underflows the copy length.
func patchKTFClient(client []byte) ([]byte, bool, error) {
	digest := sha256.Sum256(client)
	return patchKTFClientForDigest(hex.EncodeToString(digest[:]), client)
}

func patchKTFClientForDigest(signature string, client []byte) ([]byte, bool, error) {
	if signature != makjangClientSHA256 && signature != makjangPatchedClientSHA256 {
		return client, false, nil
	}
	if len(client) < makjangCopyGuardOffset+len(makjangCopyGuardOriginal) {
		return nil, false, fmt.Errorf("KTF compatibility client is truncated")
	}
	at := client[makjangCopyGuardOffset : makjangCopyGuardOffset+len(makjangCopyGuardOriginal)]
	if bytes.Equal(at, makjangCopyGuardPatched[:]) {
		return client, true, nil
	}
	if !bytes.Equal(at, makjangCopyGuardOriginal[:]) {
		return nil, false, fmt.Errorf("KTF compatibility client instructions changed")
	}
	patched := bytes.Clone(client)
	copy(patched[makjangCopyGuardOffset:], makjangCopyGuardPatched[:])
	return patched, true, nil
}

func (r *Runtime) mappedClientImage() []byte {
	if r.clientImage != nil {
		return r.clientImage
	}
	return r.Pkg.Client
}

// RestoreKnownClientPatches also upgrades snapshots saved before the title
// patches were installed. Current snapshots already contain these instructions.
func (r *Runtime) RestoreKnownClientPatches() error {
	if err := r.restoreAstonishiaDecisionLabels(); err != nil {
		return err
	}
	if !r.makjangCopyGuard {
		return nil
	}
	var observed [4]byte
	address := ImageBase + makjangCopyGuardOffset
	if err := r.CPU.ReadMemory(address, observed[:]); err != nil {
		return fmt.Errorf("read KTF client copy guard: %w", err)
	}
	if observed == makjangCopyGuardPatched {
		return nil
	}
	if observed != makjangCopyGuardOriginal {
		return fmt.Errorf("KTF saved client copy guard instructions changed")
	}
	if err := r.CPU.WriteMemory(address, makjangCopyGuardPatched[:]); err != nil {
		return fmt.Errorf("restore KTF client copy guard: %w", err)
	}
	return nil
}
