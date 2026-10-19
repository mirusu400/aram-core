package pxo

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	pxoloader "github.com/mirusu400/aram-core/loader/pxo"
)

// TestReferencePXO1GameFrames uses only a user-authorized local executable.
// No PXO1 bytes or absolute path are retained by the repository.
func TestReferencePXO1GameFrames(t *testing.T) {
	path := os.Getenv("ARAM_PXO_DATA")
	if path == "" {
		t.Skip("ARAM_PXO_DATA is not set")
	}
	data, err := os.ReadFile(path)
	check(t, err)
	pkg, err := pxoloader.Inspect(data)
	check(t, err)

	tests := []struct {
		name      string
		selection uint32
		wantHash  string
	}{
		{name: "sanakan", selection: 0, wantHash: "5f761db1883adbb596a7dd1c90816332c1287fc4e67f3c1cbcdc9ece491ee012"},
		{name: "3d-block", selection: 1, wantHash: "9f0d83b6eb6190114573e7d6fc48dc0a6f6fdd8f6259594435a7e19a73b79f50"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := interpreter.New()
			defer backend.Close()
			host := NewHost(backend)
			runtime, err := NewRuntime(backend, pkg, host)
			check(t, err)
			_, err = runtime.CallEvent(context.Background(), 1, 0, 0, 20_000_000)
			check(t, err)
			launcher := host.Handler(1)
			if launcher == 0 {
				t.Fatal("PXO1 launcher handler is not registered")
			}
			_, err = runtime.CallFunction(context.Background(), launcher, []uint32{1, 1, 0}, 100_000_000)
			check(t, err)
			if test.selection != 0 {
				_, err = runtime.CallFunction(context.Background(), launcher, []uint32{3, 3, 0}, 100_000_000)
				check(t, err)
			}
			_, err = runtime.CallFunction(context.Background(), launcher, []uint32{3, 0x14, 0}, 100_000_000)
			check(t, err)
			if host.Active() != 2 {
				t.Fatalf("active handler = %d, want game handler 2", host.Active())
			}
			game := host.Handler(2)
			if game == 0 {
				t.Fatal("PXO1 game handler is not registered")
			}
			_, err = runtime.CallFunction(context.Background(), game, []uint32{1, 1, 0}, 100_000_000)
			check(t, err)
			_, err = runtime.CallFunction(context.Background(), game, []uint32{5, 1, 1}, 100_000_000)
			check(t, err)

			pixels := make([]byte, FramebufferByteSize)
			check(t, backend.ReadMemory(FramebufferBase, pixels))
			gotHash := fmt.Sprintf("%x", sha256.Sum256(pixels))
			if gotHash != test.wantHash {
				t.Fatalf("RGB565 frame SHA-256 = %s, want %s", gotHash, test.wantHash)
			}
		})
	}
}
