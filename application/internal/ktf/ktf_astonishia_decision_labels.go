package ktf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const (
	astonishiaClientSHA256        = "8b2a1b48056c944495271ec47962ac26d203688203dae5cdfb24ff5f9b439889"
	astonishiaPatchedClientSHA256 = "8f78bf2ebc2585081be4fc2fd1cdd5034dfee7df98975da37613d52d9cda246b"
)

// Astonishia ep2 draws Yes/No with its own six-pixel bitmap font. Its native
// DrawDecisionBox passes the button top to both DrawSmallText calls, placing
// the captions above the selection cursor. Move these two captions down 16
// pixels, preserving their X coordinates and the rest of the title's text.
var astonishiaDecisionLabelPatches = [...]struct {
	offset            int
	original, patched []byte
}{
	{
		0x6ad2c,
		[]byte{0x06, 0x36, 0x33, 0x1c}, // adds r6, #6; adds r3, r6, #0
		[]byte{0x10, 0x32, 0xb3, 0x1d}, // adds r2, #16; adds r3, r6, #6
	},
	{
		0x6ad50,
		[]byte{0x0b, 0x98}, // ldr r0, [sp, #44]: original button top
		[]byte{0x00, 0x98}, // ldr r0, [sp]: Yes call's adjusted outgoing Y
	},
}

func patchAstonishiaDecisionLabels(client []byte) ([]byte, bool, error) {
	digest := sha256.Sum256(client)
	return patchAstonishiaDecisionLabelsForDigest(hex.EncodeToString(digest[:]), client)
}

func patchAstonishiaDecisionLabelsForDigest(signature string, client []byte) ([]byte, bool, error) {
	if signature != astonishiaClientSHA256 && signature != astonishiaPatchedClientSHA256 {
		return client, false, nil
	}
	changed := false
	for _, patch := range astonishiaDecisionLabelPatches {
		if len(client) < patch.offset+len(patch.original) {
			return nil, false, fmt.Errorf("KTF Astonishia decision caption client is truncated")
		}
		observed := client[patch.offset : patch.offset+len(patch.original)]
		if bytes.Equal(observed, patch.patched) {
			continue
		}
		if !bytes.Equal(observed, patch.original) {
			return nil, false, fmt.Errorf("KTF Astonishia decision caption instructions at 0x%x changed", patch.offset)
		}
		changed = true
	}
	if !changed {
		return client, true, nil
	}
	patched := bytes.Clone(client)
	for _, patch := range astonishiaDecisionLabelPatches {
		copy(patched[patch.offset:], patch.patched)
	}
	return patched, true, nil
}

func (r *Runtime) restoreAstonishiaDecisionLabels() error {
	if !r.astonishiaDecisionLabels {
		return nil
	}
	var restore [len(astonishiaDecisionLabelPatches)]bool
	// Validate both sites before upgrading any instructions from an old state.
	for index, patch := range astonishiaDecisionLabelPatches {
		observed := make([]byte, len(patch.original))
		address := ImageBase + uint32(patch.offset)
		if err := r.CPU.ReadMemory(address, observed); err != nil {
			return fmt.Errorf("read KTF Astonishia decision caption: %w", err)
		}
		if bytes.Equal(observed, patch.patched) {
			continue
		}
		if !bytes.Equal(observed, patch.original) {
			return fmt.Errorf("KTF saved Astonishia decision caption instructions at 0x%x changed", patch.offset)
		}
		restore[index] = true
	}
	for index, patch := range astonishiaDecisionLabelPatches {
		if restore[index] {
			if err := r.CPU.WriteMemory(ImageBase+uint32(patch.offset), patch.patched); err != nil {
				return fmt.Errorf("restore KTF Astonishia decision caption: %w", err)
			}
		}
	}
	return nil
}
